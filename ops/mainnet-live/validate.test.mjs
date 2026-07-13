import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import assert from "node:assert/strict";

import { validateAlertRules, validateRepository } from "./validate.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "../..");
const contract = JSON.parse(readFileSync(resolve(here, "metrics/contract.v1.json"), "utf8"));
const metricNames = new Set(contract.metrics.map(metric => metric.name));
const rules = readFileSync(resolve(here, "prometheus/rules.v1.yaml"), "utf8");

test("v1 observability artifacts are complete", () => {
  assert.deepEqual(validateRepository(root), {alerts: 19, metrics: 22, panels: 13});
});

test("high and critical alerts cannot opt out of stage reset", () => {
  const changed = rules.replace(
    /(alert: RobinKmsFailure[\s\S]*?stage_reset:) "true"/,
    "$1 \"false\""
  );
  assert.throws(
    () => validateAlertRules(changed, metricNames),
    /RobinKmsFailure must reset the clean-observation period/
  );
});

test("margin alert cannot invert its safety threshold", () => {
  const changed = rules.replace("(robin_margin_coverage_ratio) < 2", "(robin_margin_coverage_ratio) > 2");
  assert.throws(
    () => validateAlertRules(changed, metricNames),
    /RobinMarginCoverageLow has an unsafe or unexpected threshold expression/
  );
});
