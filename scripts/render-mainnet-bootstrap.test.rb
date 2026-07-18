#!/usr/bin/env ruby

require "minitest/autorun"
require "stringio"
require "tempfile"
require "tmpdir"

require_relative "render-mainnet-bootstrap"

class RenderMainnetBootstrapTest < Minitest::Test
  WORKSPACE = "tea-00000000000000000000"
  PROJECT = "prj-00000000000000000000"
  ENVIRONMENT = "evm-00000000000000000000"

  def manifest
    @manifest ||= RenderMainnet::Manifest.new(File.expand_path("../render.yaml", __dir__))
  end

  def outputs
    {
      "LighterProvisionerRoleArn" => "arn:aws:iam::123456789012:role/lighter",
      "LighterEnvelopeKeyAlias" => "alias/robin/lighter/credentials",
      "RobinhoodKeyControlPlaneArn" => "arn:aws:lambda:us-east-1:123456789012:function:key-control:17",
      "RobinhoodProvisionerRoleArn" => "arn:aws:iam::123456789012:role/robinhood-provisioner",
      "RobinhoodSignerRoleArn" => "arn:aws:iam::123456789012:role/robinhood-signer"
    }
  end

  def self.prepare_receipt_for(services)
    controlled = RenderMainnet::CONTROLLED_SERVICES.to_h do |name|
      [name, services.fetch(name).fetch("id")]
    end
    {
      "phase" => "prepared",
      "workspaceId" => WORKSPACE,
      "projectId" => PROJECT,
      "environmentId" => ENVIRONMENT,
      "services" => controlled,
      "publicServices" => {
        "robintheclaw" => services.fetch("robintheclaw").fetch("id")
      },
      "awsServices" => controlled.slice(*RenderMainnet::AWS_SERVICES)
    }
  end

  def prepare_receipt(services)
    self.class.prepare_receipt_for(services)
  end

  def test_manifest_uses_one_protected_production_environment
    assert_equal 20, manifest.services.length
    assert_equal 5, manifest.databases.length
    assert_equal 23, manifest.env_groups.length
    assert_equal 46, manifest.referenced_groups.length
    assert_equal "enabled", manifest.environment.dig("networking", "isolation")
    assert_equal "enabled", manifest.environment.dig("permissions", "protection")
  end

  def test_parses_and_validates_aws_outputs
    value = {
      "Stacks" => [{
        "Outputs" => outputs.map { |key, output| { "OutputKey" => key, "OutputValue" => output } }
      }]
    }
    assert_equal outputs, RenderMainnet.parse_outputs(value)
    invalid = outputs.merge("LighterEnvelopeKeyAlias" => "alias/wrong")
    assert_raises(RenderMainnet::Error) { RenderMainnet.parse_outputs(invalid) }
    unqualified = outputs.merge(
      "RobinhoodKeyControlPlaneArn" => "arn:aws:lambda:us-east-1:123456789012:function:key-control"
    )
    assert_raises(RenderMainnet::Error) { RenderMainnet.parse_outputs(unqualified) }
  end

  def test_prepare_receipt_binds_every_service_id
    client = FakeClient.new(
      outputs,
      controlled_services: true,
      web_auto_deploy: "no"
    )
    receipt = prepare_receipt(client.services)
    assert_equal receipt, RenderMainnet.parse_prepare_receipt(receipt)

    receipt["services"]["robin-api"] = receipt["services"]["robin-execution-coordinator"]
    assert_raises(RenderMainnet::Error) do
      RenderMainnet.parse_prepare_receipt(receipt)
    end
  end

  def test_later_phase_rejects_service_replacement_after_prepare
    client = FakeClient.new(
      outputs,
      controlled_services: true,
      web_auto_deploy: "no"
    )
    receipt = prepare_receipt(client.services)
    bootstrap = RenderMainnet::Bootstrap.new(
      client: client,
      manifest: manifest,
      workspace_id: WORKSPACE,
      output: StringIO.new,
      prepare_receipt: receipt
    )
    client.services.fetch("robin-api")["id"] = "srv-replacement"

    error = assert_raises(RenderMainnet::Error) do
      bootstrap.bind(outputs: outputs, service_env: nil, confirmation: "BIND")
    end
    assert_includes error.message, "does not match the prepare receipt"
    refute client.calls.any? { |method, _path, _body| method == :put }
  end

  def test_rejects_conflicting_service_bindings
    base = RenderMainnet.aws_bindings(outputs)
    additions = { "robin-lighter-provisioner" => { "AWS_ROLE_ARN" => "wrong" } }
    assert_raises(RenderMainnet::Error) { RenderMainnet.merge_bindings(base, additions) }
  end

  def test_retries_429_using_retry_after
    responses = [
      RenderMainnet::Response.new(code: 429, headers: { "Retry-After" => "7" }),
      RenderMainnet::Response.new(code: 200, body: JSON.generate("ok" => true))
    ]
    sleeps = []
    client = RenderMainnet::Client.new(
      "token",
      sleeper: ->(seconds) { sleeps << seconds },
      transport: ->(_method, _path, _body) { responses.shift }
    )
    assert_equal({ "ok" => true }, client.get("/resource"))
    assert_equal [7], sleeps
  end

  def test_service_environment_file_requires_nonempty_values
    assert_equal(
      { "service" => { "KEY" => "value" } },
      RenderMainnet.parse_service_env("services" => { "service" => { "KEY" => "value" } })
    )
    assert_raises(RenderMainnet::Error) do
      RenderMainnet.parse_service_env("services" => { "service" => { "KEY" => "" } })
    end
  end

  def test_environment_group_config_requires_every_reviewed_value
    groups = RenderMainnet::CONFIG_GROUP_KEYS.to_h do |name, keys|
      [name, keys.to_h { |key| [key, "reviewed-value"] }]
    end
    assert_equal groups, RenderMainnet.parse_env_group_config("groups" => groups)

    groups.fetch("robin-lighter-market-config").delete("LIGHTER_AAPL_MARKET_INDEX")
    assert_raises(RenderMainnet::Error) do
      RenderMainnet.parse_env_group_config("groups" => groups)
    end
  end

  def test_readonly_database_url_requires_exact_role_and_tls
    runner = RenderMainnet::PsqlQueryRunner.new
    connection = runner.send(
      :parse_url,
      "postgres://robin_app_readonly:a+b%2Fc@db.example/robin_app?sslmode=require",
      "robin_app_readonly"
    )
    assert_equal "a+b/c", connection.fetch(:password)
    assert_equal "require", connection.fetch(:sslmode)

    assert_raises(RenderMainnet::Error) do
      runner.send(
        :parse_url,
        "postgres://robin_app_api:password@db.example/robin_app?sslmode=require",
        "robin_app_readonly"
      )
    end
    assert_raises(RenderMainnet::Error) do
      runner.send(
        :parse_url,
        "postgres://robin_app_readonly:password@db.example/robin_app?sslmode=disable",
        "robin_app_readonly"
      )
    end
  end

  def test_external_database_isolation_fails_closed_on_invalid_connection_info
    client = Object.new
    client.define_singleton_method(:get) { |_path| nil }
    bootstrap = RenderMainnet::Bootstrap.new(
      client: client,
      manifest: manifest,
      workspace_id: WORKSPACE,
      output: StringIO.new
    )

    error = assert_raises(RenderMainnet::Error) do
      bootstrap.send(
        :verify_external_database_block!,
        "robin-app",
        { "id" => "dpg-00000000000000000000", "ipAllowList" => nil }
      )
    end
    assert_equal "robin-app external isolation could not be verified", error.message
  end

  def test_internal_runtime_probe_binds_commit_nonce_and_private_endpoints
    commit = "1" * 40
    names = %w[robin-api robin-lighter-signer]
    environment = {
      "RENDER" => "true",
      "RENDER_GIT_COMMIT" => commit,
      "ROBIN_RUNTIME_PROBE_NONCE" => "a" * 64,
      "ROBIN_RUNTIME_PROBE_API_HOSTPORT" => "robin-api:10000",
      "ROBIN_RUNTIME_PROBE_LIGHTER_SIGNER_HOSTPORT" => "robin-lighter-signer:10000"
    }
    output = StringIO.new
    status = {
      "/health" => "ok",
      "/readyz" => "ready"
    }
    RenderMainnet.emit_internal_runtime_probe(
      commit: commit,
      services: names,
      environment: environment,
      output: output,
      requester: lambda { |uri|
        RenderMainnet::Response.new(
          code: 200,
          body: JSON.generate("status" => status.fetch(uri.path))
        )
      }
    )

    encoded = output.string.strip.delete_prefix(RenderMainnet::RuntimeReadiness::RESULT_PREFIX)
    envelope = JSON.parse(Base64.strict_decode64(encoded))
    assert_equal commit, envelope.fetch("commit")
    assert_equal "a" * 64, envelope.fetch("nonce")
    assert_equal names, envelope.fetch("services")

    environment["RENDER_GIT_COMMIT"] = "2" * 40
    assert_raises(RenderMainnet::Error) do
      RenderMainnet.emit_internal_runtime_probe(
        commit: commit,
        services: names,
        environment: environment,
        output: StringIO.new,
        requester: ->(_uri) { RenderMainnet::Response.new(code: 200) }
      )
    end

    environment["RENDER_GIT_COMMIT"] = commit
    error = assert_raises(RenderMainnet::Error) do
      RenderMainnet.emit_internal_runtime_probe(
        commit: commit,
        services: names,
        environment: environment,
        output: StringIO.new,
        timeout: 0,
        requester: lambda { |_uri|
          RenderMainnet::Response.new(
            code: 200,
            body: JSON.generate("status" => "unready")
          )
        }
      )
    end
    assert_equal "internal runtime endpoints did not become ready: #{names.join(", ")}", error.message
  end

  def test_runtime_readiness_requires_fresh_stable_instances_and_private_probe
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    client = RuntimeReadinessClient.new(now)
    verifier = RenderMainnet::RuntimeReadiness.new(
      client: client,
      workspace_id: WORKSPACE,
      clock: -> { now },
      sleeper: ->(_seconds) {}
    )
    services = {
      "robin-paper-agent" => { "id" => "srv-worker" },
      "robin-lighter-signer" => { "id" => "srv-signer" },
      RenderMainnet::RUNTIME_PROBE_SERVICE => { "id" => "srv-probe" }
    }
    evidence = verifier.verify(
      services: services,
      names: %w[robin-paper-agent robin-lighter-signer],
      commit: "1" * 40,
      resumed_at: now
    )

    assert_equal(
      {
        "robin-paper-agent" => "srv-worker-instance",
        "robin-lighter-signer" => "srv-signer-instance"
      },
      evidence.fetch("instances")
    )
    assert_equal "job-runtime-0", evidence.fetch("privateProbeJobId")
    assert_equal 4, client.instance_reads

    stale = RuntimeReadinessClient.new(now - 120)
    verifier = RenderMainnet::RuntimeReadiness.new(
      client: stale,
      workspace_id: WORKSPACE,
      clock: -> { now },
      sleeper: ->(_seconds) {}
    )
    assert_raises(RenderMainnet::Error) do
      verifier.verify(
        services: services,
        names: ["robin-paper-agent"],
        commit: "1" * 40,
        resumed_at: now
      )
    end

    invalid = RuntimeReadinessClient.new(now, instance_id: nil)
    verifier = RenderMainnet::RuntimeReadiness.new(
      client: invalid,
      workspace_id: WORKSPACE,
      clock: -> { now },
      sleeper: ->(_seconds) {}
    )
    assert_raises(RenderMainnet::Error) do
      verifier.verify(
        services: services,
        names: ["robin-paper-agent"],
        commit: "1" * 40,
        resumed_at: now
      )
    end

    rotating = RuntimeReadinessClient.new(now, rotate_instance: true)
    verifier = RenderMainnet::RuntimeReadiness.new(
      client: rotating,
      workspace_id: WORKSPACE,
      clock: -> { now },
      sleeper: ->(_seconds) {}
    )
    error = assert_raises(RenderMainnet::Error) do
      verifier.verify(
        services: services,
        names: ["robin-paper-agent"],
        commit: "1" * 40,
        resumed_at: now
      )
    end
    assert_equal "runtime instances changed during the stability window", error.message
  end

  def test_prepare_creates_suspended_noop_shells_in_production
    client = FakeClient.new(outputs, web_suspended: true)
    output = StringIO.new
    bootstrap = RenderMainnet::Bootstrap.new(
      client: client,
      manifest: manifest,
      workspace_id: WORKSPACE,
      output: output
    )
    bootstrap.prepare(confirmation: "PREPARE")

    creates = client.calls.select { |method, path, _body| method == :post && path == "/services" }
    assert_equal RenderMainnet::CONTROLLED_SERVICES.length, creates.length
    creates.each do |_method, _path, body|
      assert_equal "no", body.fetch("autoDeploy")
      assert_equal ENVIRONMENT, body.fetch("environmentId")
      assert_equal "true", body.dig("serviceDetails", "envSpecificDetails", "buildCommand")
      assert_equal "sleep infinity", body.dig("serviceDetails", "envSpecificDetails", "startCommand")
    end
    receipt = JSON.parse(output.string)
    assert_equal "prepared", receipt.fetch("phase")
    assert_equal RenderMainnet::CONTROLLED_SERVICES.sort, receipt.fetch("services").keys.sort
    assert_equal RenderMainnet::AWS_SERVICES.sort, receipt.fetch("awsServices").keys.sort
    assert_equal({ "robintheclaw" => "srv-web" }, receipt.fetch("publicServices"))
    assert_includes client.calls, [:patch, "/services/srv-web", { "autoDeploy" => "no" }]
    assert_includes client.calls, [:post, "/services/srv-web/resume", nil]
  end

  def test_bind_uses_only_per_key_updates
    client = FakeClient.new(
      outputs,
      controlled_services: true,
      web_auto_deploy: "no"
    )
    output = StringIO.new
    bootstrap = RenderMainnet::Bootstrap.new(
      client: client,
      manifest: manifest,
      workspace_id: WORKSPACE,
      output: output,
      prepare_receipt: prepare_receipt(client.services)
    )
    bootstrap.bind(outputs: outputs, service_env: nil, confirmation: "BIND")

    puts = client.calls.select { |method, _path, _body| method == :put }
    assert_equal 5, puts.length
    assert puts.all? { |_method, path, _body| path.match?(%r{/env-vars/[^/]+\z}) }
    refute_includes output.string, outputs.fetch("LighterProvisionerRoleArn")
    assert_equal "bound", JSON.parse(output.string).fetch("phase")
  end

  def test_prepare_suspends_every_discovered_controlled_service_after_failure
    client = FakeClient.new(
      outputs,
      fail_create_name: "robin-strategy-runner"
    )
    bootstrap = RenderMainnet::Bootstrap.new(
      client: client,
      manifest: manifest,
      workspace_id: WORKSPACE,
      output: StringIO.new
    )

    error = assert_raises(RenderMainnet::Error) do
      bootstrap.prepare(confirmation: "PREPARE")
    end
    assert_includes error.message, "all discovered controlled services are confirmed suspended"
    controlled = client.services.values.select do |service|
      RenderMainnet::CONTROLLED_SERVICES.include?(service.fetch("name"))
    end
    refute_empty controlled
    assert controlled.all? { |service| service["suspended"] == "suspended" }
    assert_equal "not_suspended", client.services.fetch("robintheclaw").fetch("suspended")
  end

  def test_quiescence_receipt_binds_release_and_zero_execution_state
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    key = "a" * 32
    receipt = RenderMainnet::QuiescenceReceipt.sign(quiescence_payload(now), key, now: now)

    payload = RenderMainnet::QuiescenceReceipt.verify(
      receipt,
      key,
      workspace_id: WORKSPACE,
      environment_id: ENVIRONMENT,
      commit: "1" * 40,
      now: now
    )
    assert_equal "HALTED", payload.fetch("globalControlMode")

    receipt["payload"]["executionActionsLeased"] = 1
    assert_raises(RenderMainnet::Error) do
      RenderMainnet::QuiescenceReceipt.verify(
        receipt,
        key,
        workspace_id: WORKSPACE,
        environment_id: ENVIRONMENT,
        commit: "1" * 40,
        now: now
      )
    end
  end

  def test_quiescence_receipt_rejects_stale_or_forged_evidence
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    key = "a" * 32
    stale = quiescence_payload(now - 600)
    stale["expiresAt"] = (now + 60).iso8601
    assert_raises(RenderMainnet::Error) do
      RenderMainnet::QuiescenceReceipt.sign(stale, key, now: now)
    end

    receipt = RenderMainnet::QuiescenceReceipt.sign(quiescence_payload(now), key, now: now)
    receipt["signature"]["value"] = "0" * 64
    assert_raises(RenderMainnet::Error) do
      RenderMainnet::QuiescenceReceipt.verify(
        receipt,
        key,
        workspace_id: WORKSPACE,
        environment_id: ENVIRONMENT,
        commit: "1" * 40,
        now: now
      )
    end
  end

  def test_quiescence_collector_derives_counts_from_readonly_database_fixtures
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    fixtures = quiescence_sources(now)
    runner = FixtureQueryRunner.new(fixtures)
    collector = RenderMainnet::QuiescenceCollector.new(
      query_runner: runner,
      clock: -> { now }
    )

    evidence, payload = collector.collect(
      urls: quiescence_urls,
      workspace_id: WORKSPACE,
      environment_id: ENVIRONMENT,
      commit: "1" * 40
    )
    assert_equal fixtures.keys.sort, evidence.fetch("sources").keys.sort
    assert_equal "HALTED", payload.fetch("globalControlMode")
    assert_equal 4, runner.calls.length
    execution_query = runner.calls.find { |call| call.fetch(:source) == "execution" }.fetch(:sql)
    assert_includes execution_query, "snapshot.observed_at >= now() - interval '5 seconds'"
    assert_includes execution_query, "snapshot.observed_at <= now()"
    assert_includes execution_query, "snapshot.expires_at > now()"
    assert_includes execution_query, "JOIN execution_account_registrations registration"
    assert_includes execution_query, "account.status <> 'closed'"
    custody_query = runner.calls.find { |call| call.fetch(:source) == "custody" }.fetch(:sql)
    assert_includes custody_query, "status IN ('ambiguous', 'quarantined')"

    fixtures.fetch("execution")["executionActionsPending"] = 1
    error = assert_raises(RenderMainnet::Error) do
      collector.collect(
        urls: quiescence_urls,
        workspace_id: WORKSPACE,
        environment_id: ENVIRONMENT,
        commit: "1" * 40
      )
    end
    assert_includes error.message, "executionActionsPending must be zero"
  end

  def test_quiescence_collector_rejects_registered_accounts_before_release
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    fixtures = quiescence_sources(now)
    fixtures.fetch("execution")["registeredAccounts"] = 1
    collector = RenderMainnet::QuiescenceCollector.new(
      query_runner: FixtureQueryRunner.new(fixtures),
      clock: -> { now }
    )

    error = assert_raises(RenderMainnet::Error) do
      collector.collect(
        urls: quiescence_urls,
        workspace_id: WORKSPACE,
        environment_id: ENVIRONMENT,
        commit: "1" * 40
      )
    end
    assert_equal RenderMainnet::REGISTERED_ACCOUNT_RELEASE_ERROR, error.message
  end

  def test_quiescence_collector_rejects_non_readonly_database_fixture
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    fixtures = quiescence_sources(now)
    fixtures.fetch("custody")["role"] = "robin_custody_signer"
    collector = RenderMainnet::QuiescenceCollector.new(
      query_runner: FixtureQueryRunner.new(fixtures),
      clock: -> { now }
    )

    assert_raises(RenderMainnet::Error) do
      collector.collect(
        urls: quiescence_urls,
        workspace_id: WORKSPACE,
        environment_id: ENVIRONMENT,
        commit: "1" * 40
      )
    end
  end

  def test_rollback_control_confirmation_is_fresh_and_control_scoped
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    fixtures = quiescence_sources(now)
    fixtures.fetch("execution")["executionActionsPending"] = 1
    collector = RenderMainnet::QuiescenceCollector.new(
      query_runner: FixtureQueryRunner.new(fixtures),
      clock: -> { now }
    )

    confirmation = collector.confirm_controls_halted(
      urls: quiescence_urls,
      environment_id: ENVIRONMENT,
      commit: "1" * 40
    )
    assert_equal "HALTED", confirmation.fetch("globalControlMode")

    fixtures.fetch("execution")["globalControlMode"] = "ACTIVE"
    assert_raises(RenderMainnet::Error) do
      collector.confirm_controls_halted(
        urls: quiescence_urls,
        environment_id: ENVIRONMENT,
        commit: "1" * 40
      )
    end
  end

  def test_render_jobs_collect_every_database_source_inside_the_environment
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    client = EvidenceJobClient.new(quiescence_sources(now))
    runner = RenderMainnet::RenderJobQueryRunner.new(
      client: client,
      workspace_id: WORKSPACE,
      service_ids: EvidenceJobClient.service_ids,
      sleeper: ->(_seconds) {}
    )
    collector = RenderMainnet::QuiescenceCollector.new(
      query_runner: runner,
      clock: -> { now }
    )

    evidence, payload = collector.collect(
      urls: RenderMainnet::QuiescenceCollector::URL_ENV.keys.to_h do |source|
        [source, "render-job://#{source}"]
      end,
      workspace_id: WORKSPACE,
      environment_id: ENVIRONMENT,
      commit: "1" * 40
    )

    assert_equal RenderMainnet::INTERNAL_SOURCE_BINDINGS.keys.sort, evidence.fetch("sources").keys.sort
    assert_equal "HALTED", payload.fetch("globalControlMode")
    posts = client.calls.select { |method, path, _body| method == :post && path.end_with?("/jobs") }
    assert_equal 4, posts.length
    first_job_get = client.calls.index { |method, path, _body| method == :get && path.include?("/jobs/") }
    last_job_post = client.calls.rindex { |method, path, _body| method == :post && path.end_with?("/jobs") }
    assert_operator first_job_get, :>, last_job_post
  end

  def test_render_jobs_reject_wrong_environment_or_release_artifact
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    urls = RenderMainnet::QuiescenceCollector::URL_ENV.keys.to_h do |source|
      [source, "render-job://#{source}"]
    end

    wrong_environment = RenderMainnet::QuiescenceCollector.new(
      query_runner: RenderMainnet::RenderJobQueryRunner.new(
        client: EvidenceJobClient.new(
          quiescence_sources(now),
          environment_id: "evm-wrong"
        ),
        workspace_id: WORKSPACE,
        service_ids: EvidenceJobClient.service_ids,
        sleeper: ->(_seconds) {}
      ),
      clock: -> { now }
    )
    error = assert_raises(RenderMainnet::Error) do
      wrong_environment.collect(
        urls: urls,
        workspace_id: WORKSPACE,
        environment_id: ENVIRONMENT,
        commit: "1" * 40
      )
    end
    assert_includes error.message, "does not match the prepare receipt"

    wrong_commit = RenderMainnet::QuiescenceCollector.new(
      query_runner: RenderMainnet::RenderJobQueryRunner.new(
        client: EvidenceJobClient.new(
          quiescence_sources(now),
          commit: "2" * 40
        ),
        workspace_id: WORKSPACE,
        service_ids: EvidenceJobClient.service_ids,
        sleeper: ->(_seconds) {}
      ),
      clock: -> { now }
    )
    error = assert_raises(RenderMainnet::Error) do
      wrong_commit.collect(
        urls: urls,
        workspace_id: WORKSPACE,
        environment_id: ENVIRONMENT,
        commit: "1" * 40
      )
    end
    assert_includes error.message, "is not live on"
  end

  def test_render_jobs_refuse_suspended_base_services
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    client = EvidenceJobClient.new(
      quiescence_sources(now),
      suspended: "suspended"
    )
    collector = RenderMainnet::QuiescenceCollector.new(
      query_runner: RenderMainnet::RenderJobQueryRunner.new(
        client: client,
        workspace_id: WORKSPACE,
        service_ids: EvidenceJobClient.service_ids,
        sleeper: ->(_seconds) {}
      ),
      clock: -> { now }
    )

    error = assert_raises(RenderMainnet::Error) do
      collector.collect(
        urls: RenderMainnet::QuiescenceCollector::URL_ENV.keys.to_h do |source|
          [source, "render-job://#{source}"]
        end,
        workspace_id: WORKSPACE,
        environment_id: ENVIRONMENT,
        commit: "1" * 40
      )
    end
    assert_includes error.message, "must be running before job creation"
    refute client.calls.any? { |method, path, _body| method == :post && path.end_with?("/jobs") }
  end

  def test_initialize_databases_runs_only_reviewed_migrations_with_no_real_process
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    client = ActivationClient.new(manifest)
    output = StringIO.new
    collector = RenderMainnet::QuiescenceCollector.new(
      query_runner: FixtureQueryRunner.new(quiescence_sources(now)),
      clock: -> { now }
    )
    bootstrap = ActivationBootstrap.new(
      client: client,
      manifest: manifest,
      workspace_id: WORKSPACE,
      output: output,
      clock: -> { now },
      quiescence_collector: collector
    )

    Dir.mktmpdir do |directory|
      evidence_path = File.join(directory, "evidence.json")
      receipt_path = File.join(directory, "receipt.json")
      bootstrap.initialize_databases(
        commit: "1" * 40,
        receipt_key: "a" * 32,
        evidence_output: evidence_path,
        receipt_output: receipt_path,
        confirmation: "INITIALIZE-DATABASES"
      )

      receipt = RenderMainnet::QuiescenceReceipt.read_receipt(receipt_path)
      RenderMainnet::QuiescenceReceipt.verify(
        receipt,
        "a" * 32,
        workspace_id: WORKSPACE,
        environment_id: ENVIRONMENT,
        commit: "1" * 40,
        now: now
      )
      assert_equal 0o600, File.stat(evidence_path).mode & 0o777
      assert_equal 0o600, File.stat(receipt_path).mode & 0o777
    end

    RenderMainnet::DATABASE_INITIALIZERS.each do |name|
      service = client.services.fetch(name)
      assert_equal "suspended", service.fetch("suspended")
      assert_equal(
        manifest.services.fetch(name).fetch("startCommand"),
        service.dig("serviceDetails", "envSpecificDetails", "startCommand")
      )
      patches = client.calls.select do |method, path, body|
        method == :patch &&
          client.service_name(path) == name &&
          body.dig("serviceDetails", "envSpecificDetails", "startCommand") == "sleep infinity"
      end
      assert_equal 2, patches.length
    end
    assert_equal "suspended", client.services.fetch("robintheclaw").fetch("suspended")
    assert_equal "databases-initialized", JSON.parse(output.string).fetch("phase")
  end

  def test_initialize_databases_restores_commands_after_migration_deploy_failure
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    client = ActivationClient.new(
      manifest,
      fail_deploy_name: "robin-live-control",
      fail_deploy_status: "pre_deploy_failed"
    )
    bootstrap = ActivationBootstrap.new(
      client: client,
      manifest: manifest,
      workspace_id: WORKSPACE,
      output: StringIO.new,
      clock: -> { now },
      quiescence_collector: fixture_collector(now)
    )

    Dir.mktmpdir do |directory|
      error = assert_raises(RenderMainnet::Error) do
        bootstrap.initialize_databases(
          commit: "1" * 40,
          receipt_key: "a" * 32,
          evidence_output: File.join(directory, "evidence.json"),
          receipt_output: File.join(directory, "receipt.json"),
          confirmation: "INITIALIZE-DATABASES"
        )
      end
      assert_includes error.message, "controls are HALTED"
    end

    assert client.services.values.all? { |service| service["suspended"] == "suspended" }
    RenderMainnet::DATABASE_INITIALIZERS.each do |name|
      assert_equal(
        manifest.services.fetch(name).fetch("startCommand"),
        client.services.fetch(name).dig("serviceDetails", "envSpecificDetails", "startCommand")
      )
    end
  end

  def test_activate_stages_each_release_then_resumes_in_dependency_order
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    client = ActivationClient.new(manifest)
    output = StringIO.new
    bootstrap = ActivationBootstrap.new(
      client: client,
      manifest: manifest,
      workspace_id: WORKSPACE,
      output: output,
      clock: -> { now },
      quiescence_collector: fixture_collector(now)
    )
    key = "a" * 32
    receipt = RenderMainnet::QuiescenceReceipt.sign(quiescence_payload(now), key, now: now)

    bootstrap.activate(
      outputs: outputs,
      service_env: {},
      env_group_config: {},
      commit: "1" * 40,
      receipt: receipt,
      receipt_key: key,
      confirmation: "ACTIVATE"
    )

    controlled_deploys = client.calls.each_index.select do |index|
      method, path, body = client.calls.fetch(index)
      next false unless method == :post && path.end_with?("/deploys") && body == { "commitId" => "1" * 40 }

      RenderMainnet::CONTROLLED_SERVICES.include?(client.service_name(path))
    end
    assert_equal RenderMainnet::CONTROLLED_SERVICES.length, controlled_deploys.length
    last_staged_deploy = controlled_deploys.max
    final_resume_start = client.calls.each_index.find do |index|
      index > last_staged_deploy &&
        client.calls.fetch(index)[0] == :post &&
        client.calls.fetch(index)[1].end_with?("/resume")
    end
    final_resumes = bootstrap.runtime_readiness_fixture.calls.flat_map { |call| call.fetch(:names) }
    assert_equal RenderMainnet::STARTUP_BATCHES.flatten, final_resumes
    release_deploys = client.calls.each_with_object([]) do |(method, path, body), names|
      if method == :post && path.end_with?("/deploys") && body == { "commitId" => "1" * 40 }
        names << client.service_name(path)
      end
    end
    assert_equal "robintheclaw", release_deploys.last
    assert_operator(
      final_resumes.index("robin-quote-authority"),
      :<,
      final_resumes.index("robin-exit-quote-publisher")
    )
    assert_operator(
      final_resumes.index("robin-lighter-provisioner"),
      :<,
      final_resumes.index("robin-api")
    )
    assert_operator(
      final_resumes.index("robin-strategy-runner"),
      :<,
      final_resumes.index("robin-live-control")
    )
    RenderMainnet::CONTROLLED_SERVICES.each do |name|
      deploy_index = controlled_deploys.find { |index| client.service_name(client.calls.fetch(index)[1]) == name }
      noop_patches = client.calls[0..deploy_index].select do |method, path, body|
        method == :patch &&
          client.service_name(path) == name &&
          body.dig("serviceDetails", "envSpecificDetails", "startCommand") == "sleep infinity"
      end
      expected_patches = RenderMainnet::EVIDENCE_BASE_SERVICES.include?(name) ? 2 : 1
      assert_equal expected_patches, noop_patches.length, "#{name} was not staged with a no-op process"
      assert client.calls[(deploy_index + 1)...final_resume_start].any? { |method, path, _body|
        method == :post && path.end_with?("/suspend") && client.service_name(path) == name
      }, "#{name} was not re-suspended after staging"
      assert_equal(
        manifest.services.fetch(name).fetch("startCommand"),
        client.services.fetch(name).dig("serviceDetails", "envSpecificDetails", "startCommand")
      )
    end
    assert RenderMainnet::CONTROLLED_SERVICES.all? { |name| client.services.fetch(name)["suspended"] == "not_suspended" }
    assert_equal "not_suspended", client.services.fetch("robintheclaw")["suspended"]
    activation = JSON.parse(output.string)
    assert_equal "activated", activation.fetch("phase")
    assert_equal RenderMainnet::STARTUP_BATCHES.length + 2, activation.fetch("quiescence").length
    assert_equal RenderMainnet::STARTUP_BATCHES, bootstrap.runtime_readiness_fixture.calls.map { |call| call.fetch(:names) }
    assert_equal RenderMainnet::STARTUP_BATCHES.length, activation.fetch("runtimeReadiness").length
  end

  def test_activate_suspends_every_controlled_service_after_mid_phase_failure
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    client = ActivationClient.new(manifest, fail_resume_name: "robin-aapl-relay-2")
    bootstrap = ActivationBootstrap.new(
      client: client,
      manifest: manifest,
      workspace_id: WORKSPACE,
      output: StringIO.new,
      clock: -> { now },
      quiescence_collector: fixture_collector(now)
    )
    key = "a" * 32
    receipt = RenderMainnet::QuiescenceReceipt.sign(quiescence_payload(now), key, now: now)

    error = assert_raises(RenderMainnet::Error) do
      bootstrap.activate(
        outputs: outputs,
        service_env: {},
        env_group_config: {},
        commit: "1" * 40,
        receipt: receipt,
        receipt_key: key,
        confirmation: "ACTIVATE"
      )
    end
    assert_includes error.message, "all controls are HALTED"
    assert RenderMainnet::CONTROLLED_SERVICES.all? { |name| client.services.fetch(name)["suspended"] == "suspended" }
  end

  def test_activate_rolls_back_when_a_resumed_runtime_is_not_ready
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    client = ActivationClient.new(manifest)
    verifier = RuntimeReadinessFixture.new(fail_on: "robin-account-publisher")
    bootstrap = ActivationBootstrap.new(
      client: client,
      manifest: manifest,
      workspace_id: WORKSPACE,
      output: StringIO.new,
      clock: -> { now },
      quiescence_collector: fixture_collector(now),
      runtime_readiness: verifier
    )
    key = "a" * 32
    receipt = RenderMainnet::QuiescenceReceipt.sign(quiescence_payload(now), key, now: now)

    error = assert_raises(RenderMainnet::Error) do
      bootstrap.activate(
        outputs: outputs,
        service_env: {},
        env_group_config: {},
        commit: "1" * 40,
        receipt: receipt,
        receipt_key: key,
        confirmation: "ACTIVATE"
      )
    end
    assert_includes error.message, "injected runtime readiness failure"
    assert RenderMainnet::CONTROLLED_SERVICES.all? { |name| client.services.fetch(name)["suspended"] == "suspended" }
  end

  def test_activate_refuses_registered_accounts_before_staging
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    client = ActivationClient.new(manifest)
    fixtures = quiescence_sources(now)
    fixtures.fetch("execution")["registeredAccounts"] = 1
    bootstrap = ActivationBootstrap.new(
      client: client,
      manifest: manifest,
      workspace_id: WORKSPACE,
      output: StringIO.new,
      clock: -> { now },
      quiescence_collector: fixture_collector(now, fixtures)
    )
    key = "a" * 32
    receipt = RenderMainnet::QuiescenceReceipt.sign(quiescence_payload(now), key, now: now)

    error = assert_raises(RenderMainnet::Error) do
      bootstrap.activate(
        outputs: outputs,
        service_env: {},
        env_group_config: {},
        commit: "1" * 40,
        receipt: receipt,
        receipt_key: key,
        confirmation: "ACTIVATE"
      )
    end

    assert_includes error.message, RenderMainnet::REGISTERED_ACCOUNT_RELEASE_ERROR
    release_mutations = client.calls.select do |method, path, _body|
      [:put, :delete].include?(method) ||
        (method == :post && path.end_with?("/deploys"))
    end
    assert_empty release_mutations
    assert RenderMainnet::CONTROLLED_SERVICES.all? do |name|
      client.services.fetch(name)["suspended"] == "suspended"
    end
  end

  def test_activate_suspends_every_controlled_service_after_terminal_deploy_failure
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    client = ActivationClient.new(
      manifest,
      fail_deploy_name: "robintheclaw",
      fail_deploy_status: "pre_deploy_failed"
    )
    bootstrap = ActivationBootstrap.new(
      client: client,
      manifest: manifest,
      workspace_id: WORKSPACE,
      output: StringIO.new,
      clock: -> { now },
      quiescence_collector: fixture_collector(now)
    )
    key = "a" * 32
    receipt = RenderMainnet::QuiescenceReceipt.sign(quiescence_payload(now), key, now: now)

    assert_raises(RenderMainnet::Error) do
      bootstrap.activate(
        outputs: outputs,
        service_env: {},
        env_group_config: {},
        commit: "1" * 40,
        receipt: receipt,
        receipt_key: key,
        confirmation: "ACTIVATE"
      )
    end
    assert RenderMainnet::CONTROLLED_SERVICES.all? { |name| client.services.fetch(name)["suspended"] == "suspended" }
    assert_equal "suspended", client.services.fetch("robintheclaw")["suspended"]
  end

  def test_activate_rolls_back_when_fresh_database_evidence_changes
    now = Time.utc(2026, 7, 17, 12, 0, 0)
    client = ActivationClient.new(manifest)
    runner = FixtureQueryRunner.new(
      quiescence_sources(now),
      execution_pending_on_call: 2
    )
    collector = RenderMainnet::QuiescenceCollector.new(
      query_runner: runner,
      clock: -> { now }
    )
    bootstrap = ActivationBootstrap.new(
      client: client,
      manifest: manifest,
      workspace_id: WORKSPACE,
      output: StringIO.new,
      clock: -> { now },
      quiescence_collector: collector
    )
    key = "a" * 32
    receipt = RenderMainnet::QuiescenceReceipt.sign(quiescence_payload(now), key, now: now)

    assert_raises(RenderMainnet::Error) do
      bootstrap.activate(
        outputs: outputs,
        service_env: {},
        env_group_config: {},
        commit: "1" * 40,
        receipt: receipt,
        receipt_key: key,
        confirmation: "ACTIVATE"
      )
    end
    assert RenderMainnet::CONTROLLED_SERVICES.all? { |name| client.services.fetch(name)["suspended"] == "suspended" }
  end

  def quiescence_payload(now)
    {
      "activeEpisodes" => 0,
      "custodyTransactionsInFlight" => 0,
      "environmentId" => ENVIRONMENT,
      "evidenceSha256" => "2" * 64,
      "executionActionsLeased" => 0,
      "executionActionsPending" => 0,
      "executionCommandsInFlight" => 0,
      "expiresAt" => (now + 120).iso8601,
      "globalControlMode" => "HALTED",
      "globalControlVersion" => 7,
      "inflightCommands" => 0,
      "lighterSigningClaims" => 0,
      "nonHaltedAccountControls" => 0,
      "nonHaltedStrategyControls" => 0,
      "nonFlatAccounts" => 0,
      "observedAt" => now.iso8601,
      "outboxItemsPending" => 0,
      "releaseCommit" => "1" * 40,
      "schedulerWorkInFlight" => 0,
      "schemaVersion" => 1,
      "signedUnsentTransactions" => 0,
      "unresolvedAmbiguities" => 0,
      "workspaceId" => WORKSPACE
    }
  end

  def quiescence_urls
    RenderMainnet::QuiescenceCollector::URL_ENV.keys.to_h do |source|
      [source, "postgres://unused/#{source}"]
    end
  end

  def fixture_collector(now, fixtures = quiescence_sources(now))
    RenderMainnet::QuiescenceCollector.new(
      query_runner: FixtureQueryRunner.new(fixtures),
      clock: -> { now }
    )
  end

  def quiescence_sources(now)
    observed_at = now.iso8601
    {
      "app" => {
        "commandsInFlight" => 0,
        "observedAt" => observed_at,
        "outboxItemsPending" => 0,
        "role" => "robin_app_readonly",
        "transactionReadOnly" => "on"
      },
      "custody" => {
        "ambiguousTransactions" => 0,
        "observedAt" => observed_at,
        "role" => "robin_custody_readonly",
        "signedUnsentTransactions" => 0,
        "transactionReadOnly" => "on",
        "transactionsInFlight" => 0
      },
      "execution" => {
        "activeEpisodes" => 0,
        "ambiguities" => 0,
        "executionActionsLeased" => 0,
        "executionActionsPending" => 0,
        "executionCommandsInFlight" => 0,
        "globalControlMode" => "HALTED",
        "globalControlVersion" => 7,
        "nonFlatAccounts" => 0,
        "nonHaltedAccountControls" => 0,
        "nonHaltedStrategyControls" => 0,
        "observedAt" => observed_at,
        "registeredAccounts" => 0,
        "role" => "robin_execution_readonly",
        "schedulerWorkInFlight" => 0,
        "transactionReadOnly" => "on"
      },
      "lighter" => {
        "observedAt" => observed_at,
        "role" => "robin_lighter_readonly",
        "signingClaims" => 0,
        "transactionReadOnly" => "on"
      }
    }
  end

  class FixtureQueryRunner
    attr_reader :calls

    def initialize(fixtures, execution_pending_on_call: nil)
      @fixtures = fixtures
      @execution_pending_on_call = execution_pending_on_call
      @execution_calls = 0
      @calls = []
    end

    def call(source:, url:, role:, sql:)
      @calls << { source: source, url: url, role: role, sql: sql }
      if source == "execution"
        @execution_calls += 1
        if @execution_calls == @execution_pending_on_call
          return @fixtures.fetch(source).merge("executionActionsPending" => 1)
        end
      end
      @fixtures.fetch(source)
    end
  end

  class EvidenceJobClient
    attr_reader :calls

    def self.service_ids
      service_names = RenderMainnet::INTERNAL_SOURCE_BINDINGS.values
        .map { |binding| binding.fetch(:service) }
        .uniq
      service_names.each_with_index.to_h do |name, index|
        [name, "srv-evidence-#{index}"]
      end
    end

    def initialize(
      fixtures,
      environment_id: ENVIRONMENT,
      commit: "1" * 40,
      suspended: "not_suspended"
    )
      @fixtures = fixtures
      @calls = []
      @jobs = {}
      @commit = commit
      service_names = RenderMainnet::INTERNAL_SOURCE_BINDINGS.values
        .map { |binding| binding.fetch(:service) }
        .uniq
      @services = service_names.each_with_index.to_h do |name, index|
        [
          name,
          {
            "id" => "srv-evidence-#{index}",
            "name" => name,
            "environmentId" => environment_id,
            "repo" => RenderMainnet::REPOSITORY_URL,
            "branch" => "main",
            "autoDeploy" => "no",
            "suspended" => suspended
          }
        ]
      end
    end

    def get(path)
      @calls << [:get, path, nil]
      case path
      when %r{\A/services\?}
        @services.values.map { |service| { "service" => service } }
      when %r{\A/services/([^/]+)/deploys\?limit=1\z}
        service = @services.values.find { |value| value["id"] == Regexp.last_match(1) }
        raise "unknown evidence service" unless service

        [{
          "deploy" => {
            "id" => "dep-#{service.fetch("id")}",
            "status" => "live",
            "commit" => { "id" => @commit }
          }
        }]
      when %r{\A/services/([^/]+)/jobs/([^/]+)\z}
        @jobs.fetch(Regexp.last_match(2)).fetch(:job)
      when %r{\A/logs\?}
        query = URI.decode_www_form(path.split("?", 2).fetch(1)).to_h
        job = @jobs.fetch(query.fetch("resource"))
        { "logs" => [{ "message" => job.fetch(:message) }] }
      else
        raise "unexpected GET #{path}"
      end
    end

    def post(path, body)
      @calls << [:post, path, body]
      match = path.match(%r{\A/services/([^/]+)/jobs\z})
      raise "unexpected POST #{path}" unless match

      source = body.fetch("startCommand").match(/--source ([a-z]+)/)&.captures&.first
      nonce = body.fetch("startCommand").match(/ROBIN_RELEASE_EVIDENCE_NONCE=([0-9a-f]{64})/)&.captures&.first
      raise "invalid evidence command" unless source && nonce

      id = "job-#{@jobs.length}"
      envelope = {
        "schemaVersion" => 1,
        "nonce" => nonce,
        "source" => source,
        "evidence" => @fixtures.fetch(source)
      }
      job = {
        "id" => id,
        "serviceId" => match[1],
        "status" => "succeeded"
      }
      @jobs[id] = {
        job: job,
        message: "#{RenderMainnet::RenderJobQueryRunner::RESULT_PREFIX}" \
                 "#{Base64.strict_encode64(JSON.generate(envelope))}"
      }
      job
    end
  end

  class RuntimeReadinessClient
    attr_reader :instance_reads

    def initialize(created_at, instance_id: :generated, rotate_instance: false)
      @created_at = created_at
      @instance_id = instance_id
      @rotate_instance = rotate_instance
      @instance_reads = 0
      @jobs = {}
    end

    def get(path)
      case path
      when %r{\A/services/([^/]+)/instances\z}
        @instance_reads += 1
        service_id = Regexp.last_match(1)
        suffix = @rotate_instance ? @instance_reads : nil
        id =
          if @instance_id == :generated
            ["#{service_id}-instance", suffix].compact.join("-")
          else
            @instance_id
        end
        [{ "id" => id, "createdAt" => @created_at.iso8601 }]
      when %r{\A/services/([^/]+)/deploys\?limit=1\z}
        [{
          "deploy" => {
            "id" => "dep-runtime-probe",
            "status" => "live",
            "commit" => { "id" => "1" * 40 }
          }
        }]
      when %r{\A/services/([^/]+)/jobs/([^/]+)\z}
        @jobs.fetch(Regexp.last_match(2)).fetch(:job)
      when %r{\A/services/([^/]+)\z}
        { "id" => Regexp.last_match(1), "suspended" => "not_suspended" }
      when %r{\A/logs\?}
        query = URI.decode_www_form(path.split("?", 2).fetch(1)).to_h
        { "logs" => [{ "message" => @jobs.fetch(query.fetch("resource")).fetch(:message) }] }
      else
        raise "unexpected GET #{path}"
      end
    end

    def post(path, body)
      match = path.match(%r{\A/services/([^/]+)/jobs\z})
      raise "unexpected POST #{path}" unless match

      command = body.fetch("startCommand")
      nonce = command.match(/ROBIN_RUNTIME_PROBE_NONCE=([0-9a-f]{64})/)&.captures&.first
      commit = command.match(/--commit ([0-9a-f]{40})/)&.captures&.first
      services = command.match(/--services ([a-z0-9,-]+)/)&.captures&.first&.split(",")
      raise "invalid runtime probe command" unless nonce && commit && services

      id = "job-runtime-#{@jobs.length}"
      envelope = {
        "schemaVersion" => 1,
        "nonce" => nonce,
        "commit" => commit,
        "services" => services
      }
      job = {
        "id" => id,
        "serviceId" => match[1],
        "status" => "succeeded"
      }
      @jobs[id] = {
        job: job,
        message: "#{RenderMainnet::RuntimeReadiness::RESULT_PREFIX}" \
                 "#{Base64.strict_encode64(JSON.generate(envelope))}"
      }
      { "job" => job }
    end
  end

  class RuntimeReadinessFixture
    attr_reader :calls

    def initialize(fail_on: nil)
      @calls = []
      @fail_on = fail_on
    end

    def verify(services:, names:, commit:, resumed_at:)
      raise RenderMainnet::Error, "injected runtime readiness failure" if names.include?(@fail_on)

      @calls << {
        services: services,
        names: names,
        commit: commit,
        resumed_at: resumed_at
      }
      {
        "schemaVersion" => 1,
        "observedAt" => resumed_at.iso8601,
        "services" => names,
        "instances" => names.to_h { |name| [name, "#{name}-instance"] },
        "privateProbeJobId" => nil
      }
    end
  end

  class ActivationBootstrap < RenderMainnet::Bootstrap
    attr_reader :runtime_readiness_fixture

    def initialize(runtime_readiness: nil, **arguments)
      @runtime_readiness_fixture = runtime_readiness || RuntimeReadinessFixture.new
      receipt = arguments.delete(:prepare_receipt) ||
                RenderMainnetBootstrapTest.prepare_receipt_for(
                  arguments.fetch(:client).services
                )
      super(
        **arguments,
        runtime_readiness: @runtime_readiness_fixture,
        prepare_receipt: receipt
      )
    end

    private

    def guard_context
      {
        "project" => { "id" => RenderMainnetBootstrapTest::PROJECT },
        "environment" => {
          "id" => RenderMainnetBootstrapTest::ENVIRONMENT,
          "protectedStatus" => "protected",
          "networkIsolationEnabled" => true
        }
      }
    end

    def service_index
      @client.services
    end

    def require_managed_services!(_services, _environment_id); end

    def require_controlled_services!(_services, _environment_id, suspended:); end

    def verify_databases!(_environment_id); end

    def verify_generated_groups!(_environment_id, config_groups: nil); end

    def verify_deployed_aws_configuration!(_services); end

    def verify_sync_false!(_services); end

    def verify_sync_false_exact!(_services, _outputs, _service_env); end

    def verify_aws_bindings!(_services, _outputs); end

    def delete_legacy_variables!(_services)
      []
    end
  end

  class ActivationClient
    attr_reader :calls, :services

    def initialize(manifest, fail_resume_name: nil, fail_deploy_name: nil, fail_deploy_status: "build_failed")
      @manifest = manifest
      @fail_resume_name = fail_resume_name
      @fail_deploy_name = fail_deploy_name
      @fail_deploy_status = fail_deploy_status
      @calls = []
      @deploys = {}
      @services = manifest.services.keys.each_with_index.to_h do |name, index|
        [
          name,
          {
            "id" => "srv-#{index}",
            "name" => name,
            "environmentId" => ENVIRONMENT,
            "repo" => RenderMainnet::REPOSITORY_URL,
            "branch" => "main",
            "autoDeploy" => "no",
            "suspended" => RenderMainnet::CONTROLLED_SERVICES.include?(name) ? "suspended" : "not_suspended",
            "serviceDetails" => {
              "healthCheckPath" => RenderMainnet::HEALTH_PATHS[name],
              "envSpecificDetails" => {
                "startCommand" => manifest.services.fetch(name).fetch("startCommand")
              }
            }
          }
        ]
      end
      RenderMainnet::DATABASE_INITIALIZERS.each_with_index do |name, index|
        service = @services.fetch(name)
        id = "dep-initial-#{index}"
        @deploys[id] = {
          "id" => id,
          "serviceId" => service.fetch("id"),
          "status" => "live",
          "commit" => { "id" => "1" * 40 }
        }
      end
    end

    def get(path)
      @calls << [:get, path, nil]
      case path
      when %r{\A/services/([^/]+)/deploys/([^/]+)\z}
        @deploys.fetch(Regexp.last_match(2))
      when %r{\A/services/([^/]+)/deploys\?limit=1\z}
        service = by_id(Regexp.last_match(1))
        latest = @deploys.values.reverse.find { |deploy| deploy["serviceId"] == service["id"] }
        latest ? [{ "deploy" => latest }] : []
      when %r{\A/services/([^/]+)\z}
        by_id(Regexp.last_match(1))
      else
        raise "unexpected GET #{path}"
      end
    end

    def post(path, body = nil)
      @calls << [:post, path, body]
      case path
      when %r{\A/services/([^/]+)/deploys\z}
        service = by_id(Regexp.last_match(1))
        if service["suspended"] == "suspended"
          raise RenderMainnet::Error, "Render API POST deploy failed with 409"
        end
        deploy = {
          "id" => "dep-#{@deploys.length}",
          "serviceId" => service.fetch("id"),
          "status" => service.fetch("name") == @fail_deploy_name ? @fail_deploy_status : "live",
          "commit" => { "id" => body.fetch("commitId") }
        }
        @deploys[deploy.fetch("id")] = deploy
        { "deploy" => deploy }
      when %r{\A/services/([^/]+)/resume\z}
        service = by_id(Regexp.last_match(1))
        raise RenderMainnet::Error, "injected resume failure" if service["name"] == @fail_resume_name

        service["suspended"] = "not_suspended"
        {}
      when %r{\A/services/([^/]+)/suspend\z}
        by_id(Regexp.last_match(1))["suspended"] = "suspended"
        {}
      else
        raise "unexpected POST #{path}"
      end
    end

    def patch(path, body)
      @calls << [:patch, path, body]
      service = by_id(path.split("/").last)
      if body.key?("autoDeploy")
        service["autoDeploy"] = body.fetch("autoDeploy")
      end
      details = body["serviceDetails"]
      if details
        service["serviceDetails"]["preDeployCommand"] = details["preDeployCommand"] if details.key?("preDeployCommand")
        if details.dig("envSpecificDetails", "startCommand")
          service["serviceDetails"]["envSpecificDetails"]["startCommand"] =
            details.dig("envSpecificDetails", "startCommand")
        end
      end
      service
    end

    def by_id(id)
      @services.values.find { |service| service["id"] == id } || raise("missing service #{id}")
    end

    def service_name(path)
      match = path.match(%r{\A/services/([^/]+)})
      match && by_id(match[1])["name"]
    end
  end

  class FakeClient
    attr_reader :calls, :services

    def initialize(
      _outputs,
      controlled_services: false,
      web_auto_deploy: "yes",
      web_suspended: false,
      fail_create_name: nil
    )
      @calls = []
      @fail_create_name = fail_create_name
      @env = Hash.new { |hash, key| hash[key] = {} }
      @services = {
        "robintheclaw" => service("robintheclaw", "srv-web", type: "web_service", runtime: "node")
      }
      @services.fetch("robintheclaw")["autoDeploy"] = web_auto_deploy
      @services.fetch("robintheclaw")["suspended"] = "suspended" if web_suspended
      return unless controlled_services

      manifest = RenderMainnet::Manifest.new(File.expand_path("../render.yaml", __dir__))
      RenderMainnet::CONTROLLED_SERVICES.each_with_index do |name, index|
        expected = manifest.services.fetch(name)
        type = {
          "pserv" => "private_service",
          "worker" => "background_worker"
        }.fetch(expected.fetch("type"))
        value = service(name, "srv-c#{index}", type: type, runtime: expected.fetch("runtime"))
        value["suspended"] = "suspended"
        @services[name] = value
      end
    end

    def get(path)
      @calls << [:get, path, nil]
      case path
      when "/owners?limit=100"
        [{ "owner" => { "id" => WORKSPACE } }]
      when %r{\A/projects\?}
        [{ "project" => { "id" => PROJECT, "name" => RenderMainnet::PROJECT_NAME } }]
      when %r{\A/environments\?}
        [{
          "environment" => {
            "id" => ENVIRONMENT,
            "name" => RenderMainnet::ENVIRONMENT_NAME,
            "projectId" => PROJECT,
            "protectedStatus" => "unprotected",
            "networkIsolationEnabled" => false
          }
        }]
      when %r{\A/services\?}
        @services.values.map { |value| { "service" => value } }
      when %r{\A/services/([^/]+)/env-vars\?}
        id = Regexp.last_match(1)
        @env[id].map { |key, value| { "envVar" => { "key" => key, "value" => value } } }
      when %r{\A/services/([^/]+)\z}
        id = Regexp.last_match(1)
        @services.values.find { |value| value["id"] == id }
      else
        raise "unexpected GET #{path}"
      end
    end

    def post(path, body = nil)
      @calls << [:post, path, body]
      case path
      when "/services"
        raise RenderMainnet::Error, "injected service creation failure" if body.fetch("name") == @fail_create_name

        id = "srv-created#{@services.length}"
        value = service(body.fetch("name"), id, type: body.fetch("type"), runtime: body.dig("serviceDetails", "runtime"))
        value["repo"] = body.fetch("repo")
        value["branch"] = body.fetch("branch")
        value["autoDeploy"] = body.fetch("autoDeploy")
        @services[value.fetch("name")] = value
        { "service" => value }
      when %r{\A/services/([^/]+)/suspend\z}
        by_id(Regexp.last_match(1))["suspended"] = "suspended"
        {}
      when %r{\A/services/([^/]+)/resume\z}
        by_id(Regexp.last_match(1))["suspended"] = "not_suspended"
        {}
      else
        raise "unexpected POST #{path}"
      end
    end

    def patch(path, body)
      @calls << [:patch, path, body]
      service = by_id(path.split("/").last)
      service["autoDeploy"] = body.fetch("autoDeploy")
      service
    end

    def put(path, body)
      @calls << [:put, path, body]
      match = path.match(%r{\A/services/([^/]+)/env-vars/([^/]+)\z})
      raise "unexpected PUT #{path}" unless match

      @env[match[1]][URI.decode_www_form_component(match[2])] = body.fetch("value")
      {}
    end

    private

    def service(name, id, type:, runtime:)
      {
        "id" => id,
        "name" => name,
        "type" => type,
        "ownerId" => WORKSPACE,
        "repo" => RenderMainnet::REPOSITORY_URL,
        "branch" => "main",
        "autoDeploy" => "no",
        "environmentId" => ENVIRONMENT,
        "suspended" => "not_suspended",
        "serviceDetails" => {
          "runtime" => runtime,
          "env" => runtime,
          "region" => "virginia"
        }
      }
    end

    def by_id(id)
      @services.values.find { |value| value["id"] == id } || raise("missing service #{id}")
    end
  end
end
