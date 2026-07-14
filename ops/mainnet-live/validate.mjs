#!/usr/bin/env node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const REQUIRED_ALERTS = {
  RobinUnhedgedExposureHigh: ["robin_unhedged_duration_seconds", /> 15$/],
  RobinSourceStateStale: ["robin_source_age_seconds", /> 5$/],
  RobinSourceStreamGap: ["robin_source_gap_open", /> 0$/],
  RobinMarginCoverageLow: ["robin_margin_coverage_ratio", /< 2$/],
  RobinUnknownVenueState: ["robin_unknown_positions", "robin_unknown_orders", /> 0 or .* > 0$/],
  RobinNonceDrift: ["robin_nonce_drift", /abs\(robin_nonce_drift\)\) > 0$/],
  RobinSignerFailure: ["robin_signer_failures_total", /increase\(robin_signer_failures_total\[5m\]\)\) > 0$/],
  RobinKmsFailure: ["robin_kms_failures_total", /increase\(robin_kms_failures_total\[5m\]\)\) > 0$/],
  RobinFinalityRpcDisagreement: ["robin_rpc_finality_disagreement", /> 0$/],
  RobinReorgDetected: ["robin_reorgs_total", /increase\(robin_reorgs_total\[5m\]\)\) > 0$/],
  RobinCommandLagHigh: ["robin_command_oldest_pending_seconds", /> 30$/],
  RobinOutboxLagHigh: ["robin_outbox_oldest_pending_seconds", /> 30$/],
  RobinExecutionGasRunwayLow: ["robin_gas_runway_seconds", /wallet_role="execution_signer"/, /< 3600$/],
  RobinAccountGasNotReady: ["robin_gas_ready", /wallet_role="user"/, /== 0$/],
  RobinRestrictiveControlActive: ["robin_control_mode", /REDUCE_ONLY\|HALTED/, /== 1$/],
  RobinUnresolvedAmbiguity: ["robin_unresolved_ambiguity", /> 0$/],
  RobinCrossAccountIncident: ["robin_cross_account_incidents_total", /increase\(robin_cross_account_incidents_total\[5m\]\)\) > 0$/],
  RobinStageObservationReset: ["robin_incident_open", /severity=~"high\|critical"/, /> 0$/],
  RobinMainnetTelemetryAbsent: ["robin_control_mode", /absent\(robin_control_mode/, /== 1$/]
};

const REQUIRED_KILL_PATHS = ["Global", "Strategy", "Account", "Guardian", "User"];

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

export function parseAlertRules(text) {
  const starts = [...text.matchAll(/^      - alert: ([A-Za-z0-9]+)$/gm)];
  return new Map(starts.map((match, index) => {
    const end = starts[index + 1]?.index ?? text.length;
    const block = text.slice(match.index, end);
    const value = key => block.match(new RegExp(`^          ${key}: ["']?([^"'\\n]+)["']?$`, "m"))?.[1];
    return [match[1], {
      block,
      expr: block.match(/^        expr: (.+)$/m)?.[1],
      severity: value("severity"),
      scope: value("scope"),
      stageReset: value("stage_reset"),
      summary: value("summary"),
      runbook: value("runbook")
    }];
  }));
}

export function validateAlertRules(text, metricNames) {
  const alerts = parseAlertRules(text);
  assert(alerts.size === Object.keys(REQUIRED_ALERTS).length, `expected ${Object.keys(REQUIRED_ALERTS).length} alerts, found ${alerts.size}`);

  for (const [name, requirements] of Object.entries(REQUIRED_ALERTS)) {
    const alert = alerts.get(name);
    assert(alert, `missing alert ${name}`);
    assert(alert.expr, `${name} has no expression`);
    assert(["warning", "high", "critical"].includes(alert.severity), `${name} has invalid severity`);
    assert(alert.scope, `${name} has no scope label`);
    assert(["true", "false"].includes(alert.stageReset), `${name} has invalid stage_reset label`);
    assert(alert.summary, `${name} has no summary`);
    assert(alert.runbook?.startsWith("docs/mainnet-operations-runbook.md#"), `${name} has no local runbook anchor`);

    for (const requirement of requirements) {
      if (typeof requirement === "string") {
        assert(metricNames.has(requirement), `${name} references metric absent from the v1 contract: ${requirement}`);
        assert(alert.expr.includes(requirement), `${name} does not reference ${requirement}`);
      } else {
        assert(requirement.test(alert.expr), `${name} has an unsafe or unexpected threshold expression: ${alert.expr}`);
      }
    }

    if (alert.severity === "high" || alert.severity === "critical") {
      assert(alert.stageReset === "true", `${name} must reset the clean-observation period`);
    } else {
      assert(alert.stageReset === "false", `${name} warning must not reset the clean-observation period`);
    }
  }

  return alerts;
}

export function validateRepository(root) {
  const base = resolve(root, "ops/mainnet-live");
  const contract = JSON.parse(readFileSync(resolve(base, "metrics/contract.v1.json"), "utf8"));
  assert(contract.schemaVersion === "robin.mainnet.metrics.v1", "unexpected metric contract version");
  assert(contract.status === "canary-active", "metric contract must declare the active canary");
  assert(contract.assumptions?.capitalEnabled === true, "metric contract must declare canary capital enabled");
  assert(contract.assumptions?.servicesEnabled === true, "metric contract must declare canary services enabled");

  const metricNames = new Set();
  for (const metric of contract.metrics ?? []) {
    assert(/^robin_[a-z0-9_]+$/.test(metric.name), `invalid metric name: ${metric.name}`);
    assert(!metricNames.has(metric.name), `duplicate metric: ${metric.name}`);
    metricNames.add(metric.name);
    assert(["counter", "gauge"].includes(metric.type), `${metric.name} has invalid type`);
    assert(metric.unit && metric.producer && metric.semantics, `${metric.name} has an incomplete contract`);
    assert(Array.isArray(metric.labels), `${metric.name} labels must be an array`);
  }

  const rules = readFileSync(resolve(base, "prometheus/rules.v1.yaml"), "utf8");
  const alerts = validateAlertRules(rules, metricNames);

  const dashboardText = readFileSync(resolve(base, "grafana/dashboards/mainnet-live-execution.json"), "utf8");
  const dashboard = JSON.parse(dashboardText);
  assert(dashboard.uid === "robin-mainnet-live-v1", "unexpected dashboard UID");
  assert(dashboard.editable === false, "dashboard must be provisioned read-only");
  assert(dashboard.schemaVersion >= 39, "dashboard schema is too old");
  assert(dashboard.tags?.includes("active-canary"), "dashboard must declare the active canary state");
  assert(dashboard.description?.includes("not that the system is healthy"), "dashboard must fail closed on empty telemetry");

  const variables = new Set((dashboard.templating?.list ?? []).map(variable => variable.name));
  for (const variable of ["environment", "strategy_version", "execution_account_id"]) {
    assert(variables.has(variable), `dashboard is missing ${variable} filter`);
  }
  for (const metric of metricNames) {
    assert(dashboardText.includes(metric), `dashboard has no query for ${metric}`);
  }

  const datasource = readFileSync(resolve(base, "grafana/provisioning/datasources/prometheus.yaml"), "utf8");
  assert(datasource.includes("uid: robin-prometheus"), "provisioned Prometheus UID does not match dashboard");
  assert(datasource.includes("url: ${PROMETHEUS_URL}"), "Prometheus URL must come from the deployment environment");
  assert(!/url: https?:\/\//.test(datasource), "Prometheus provisioning must not embed an endpoint");

  const provider = readFileSync(resolve(base, "grafana/provisioning/dashboards/mainnet-live.yaml"), "utf8");
  assert(provider.includes("disableDeletion: true"), "dashboard deletion must be disabled");
  assert(provider.includes("allowUiUpdates: false"), "dashboard UI updates must be disabled");

  const runbook = readFileSync(resolve(root, "docs/mainnet-operations-runbook.md"), "utf8");
  assert(runbook.includes("The repository policy authorizes one capped account"), "runbook must state the canary authorization boundary");
  assert(/An empty dashboard is\s+missing evidence, not a healthy system\./.test(runbook), "runbook must fail closed on missing telemetry");
  for (const path of REQUIRED_KILL_PATHS) {
    assert(runbook.includes(`### ${path} kill path`), `runbook is missing the ${path.toLowerCase()} kill path`);
  }
  assert((runbook.match(/Expected evidence:/g) ?? []).length === REQUIRED_KILL_PATHS.length, "each kill path must define expected evidence");
  assert(runbook.includes("stage_reset: \"true\""), "runbook must define stage reset handling");
  const anchors = new Set([...runbook.matchAll(/^### (.+)$/gm)].map(match => match[1]
    .toLowerCase()
    .replace(/[^a-z0-9 -]/g, "")
    .trim()
    .replace(/ +/g, "-")));
  for (const [name, alert] of alerts) {
    const anchor = alert.runbook.slice(alert.runbook.indexOf("#") + 1);
    assert(anchors.has(anchor), `${name} references a missing runbook anchor: ${anchor}`);
  }

  return {alerts: Object.keys(REQUIRED_ALERTS).length, metrics: metricNames.size, panels: dashboard.panels.length};
}

const here = dirname(fileURLToPath(import.meta.url));
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    const result = validateRepository(resolve(here, "../.."));
    console.log(`mainnet observability: ${result.metrics} metrics, ${result.alerts} alerts, ${result.panels} panels valid`);
  } catch (error) {
    console.error(`mainnet observability: ${error.message}`);
    process.exitCode = 1;
  }
}
