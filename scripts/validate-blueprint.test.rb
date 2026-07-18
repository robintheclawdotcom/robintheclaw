#!/usr/bin/env ruby

require "minitest/autorun"
require "open3"
require "tmpdir"
require "yaml"

ROOT = File.expand_path("..", __dir__)
VALIDATOR = File.join(__dir__, "validate-blueprint.rb")
BLUEPRINT = File.join(ROOT, "render.yaml")

class BlueprintValidatorTest < Minitest::Test
  def setup
    @blueprint = YAML.safe_load(File.read(BLUEPRINT), aliases: false)
  end

  def validate(value)
    Dir.mktmpdir do |directory|
      path = File.join(directory, "render.yaml")
      File.write(path, YAML.dump(value))
      _stdout, stderr, status = Open3.capture3("ruby", VALIDATOR, path)
      return [status.success?, stderr]
    end
  end

  def production
    @blueprint.fetch("projects").first.fetch("environments").first
  end

  def test_accepts_reviewed_blueprint
    success, errors = validate(@blueprint)
    assert success, errors
  end

  def test_rejects_root_resource_duplicates
    @blueprint["services"] = Marshal.load(Marshal.dump(production.fetch("services")))
    success, errors = validate(@blueprint)
    refute success
    assert_includes errors, "services must be nested under Robin the Claw/Production"
  end

  def test_rejects_wrong_project_and_environment
    @blueprint.fetch("projects").first["name"] = "Other"
    production["name"] = "Staging"
    success, errors = validate(@blueprint)
    refute success
    assert_includes errors, "Blueprint project must be Robin the Claw"
    assert_includes errors, "Blueprint environment must be Production"
  end

  def test_rejects_wrong_counts_and_regions
    production.fetch("services").pop
    production.fetch("databases").first["region"] = "oregon"
    success, errors = validate(@blueprint)
    refute success
    assert_includes errors, "Production must contain exactly 20 services"
    assert_includes errors, "robin-app: region must be virginia"
  end

  def test_rejects_cross_environment_database_reference
    service = production.fetch("services").find { |item| item["name"] == "robin-api" }
    variable = service.fetch("envVars").find { |item| item["key"] == "DATABASE_OWNER_URL" }
    variable.fetch("fromDatabase")["name"] = "external-database"
    success, errors = validate(@blueprint)
    refute success
    assert_includes errors, "robin-api: referenced database external-database is outside Production"
  end

  def test_rejects_go_toolchain_drift
    service = production.fetch("services").find { |item| item["runtime"] == "go" }
    variable = service.fetch("envVars").find { |item| item["key"] == "GO_VERSION" }
    variable["value"] = "1.25.0"
    success, errors = validate(@blueprint)
    refute success
    assert_includes errors, "#{service.fetch("name")}: GO_VERSION must be pinned to 1.26.5"
  end

  def test_rejects_runtime_owner_credential_leak
    service = production.fetch("services").find { |item| item["name"] == "robin-api" }
    service["startCommand"] = "./app/target/release/app"
    success, errors = validate(@blueprint)
    refute success
    assert_includes errors, "robin-api: runtime must start through the database credential scrubber"
  end

  def test_rejects_missing_database_role_group
    service = production.fetch("services").find { |item| item["name"] == "robin-live-control" }
    service.fetch("envVars").delete_if { |item| item["fromGroup"] == "robin-db-app-readonly" }
    success, errors = validate(@blueprint)
    refute success
    assert_includes errors, "robin-live-control: database role group robin-db-app-readonly is missing"
  end

  def test_rejects_extra_database_authority_at_runtime
    service = production.fetch("services").find { |item| item["name"] == "robin-api" }
    service.fetch("envVars") << { "fromGroup" => "robin-db-execution-readonly" }
    service.fetch("envVars") << {
      "key" => "EXTRA_DATABASE_OWNER_URL",
      "fromDatabase" => {
        "name" => "robin-execution",
        "property" => "connectionString"
      }
    }
    success, errors = validate(@blueprint)
    refute success
    assert_includes(
      errors,
      "robin-api: migration-owner bindings do not match the reviewed database contract"
    )
    assert_includes(
      errors,
      "robin-api: database role groups do not match the reviewed database contract"
    )
  end

  def test_rejects_unremoved_database_password
    service = production.fetch("services").find do |item|
      item["name"] == "robin-lighter-provisioner"
    end
    service["startCommand"] = service.fetch("startCommand").sub(
      "--remove-env ROBIN_LIGHTER_READONLY_DATABASE_PASSWORD ",
      ""
    )
    success, errors = validate(@blueprint)
    refute success
    assert_includes(
      errors,
      "robin-lighter-provisioner: runtime password scrubbing does not match the reviewed database contract"
    )
  end

  def test_rejects_backend_auto_deploy
    service = production.fetch("services").find { |item| item["name"] == "robin-api" }
    service["autoDeployTrigger"] = "checksPass"
    success, errors = validate(@blueprint)
    refute success
    assert_includes errors, "robin-api: autoDeployTrigger must be off"
  end

  def test_rejects_service_level_previews
    service = production.fetch("services").find { |item| item["name"] == "robintheclaw" }
    service["previews"] = { "generation" => "automatic" }
    success, errors = validate(@blueprint)
    refute success
    assert_includes errors, "robintheclaw: service-level previews are forbidden"
  end

  def test_rejects_public_app_origin_drift
    service = production.fetch("services").find { |item| item["name"] == "robintheclaw" }
    variable = service.fetch("envVars").find { |item| item["key"] == "APP_ORIGIN" }
    variable["value"] = "https://example.invalid"
    success, errors = validate(@blueprint)
    refute success
    assert_includes(
      errors,
      "robintheclaw: APP_ORIGIN must be pinned to https://robintheclaw.com"
    )
  end

  def test_rejects_private_service_health_check
    service = production.fetch("services").find { |item| item["name"] == "robin-api" }
    service["healthCheckPath"] = "/health"
    success, errors = validate(@blueprint)
    refute success
    assert_includes errors, "robin-api: private services cannot declare healthCheckPath"
  end

  def test_rejects_runtime_probe_binding_drift
    service = production.fetch("services").find { |item| item["name"] == "robin-live-control" }
    variable = service.fetch("envVars").find do |item|
      item["key"] == "ROBIN_RUNTIME_PROBE_ROBINHOOD_SIGNER_HOSTPORT"
    end
    variable.fetch("fromService")["name"] = "robin-lighter-signer"
    success, errors = validate(@blueprint)
    refute success
    assert_includes(
      errors,
      "robin-live-control: ROBIN_RUNTIME_PROBE_ROBINHOOD_SIGNER_HOSTPORT must use the reviewed " \
      "robin-robinhood-signer private service binding"
    )
  end
end
