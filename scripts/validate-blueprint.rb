#!/usr/bin/env ruby

require "yaml"

path = File.expand_path("../render.yaml", __dir__)
blueprint = YAML.safe_load(File.read(path), aliases: false, filename: path)
errors = []
services = blueprint.fetch("services", [])

errors << "services must not be empty" if services.empty?
services.each do |service|
  name = service.fetch("name", "unnamed")
  errors << "#{name}: rootDir is forbidden" if service.key?("rootDir")
  unless service["autoDeployTrigger"] == "checksPass"
    errors << "#{name}: autoDeployTrigger must be checksPass"
  end
  build = service.fetch("buildCommand", "")
  locked = build.include?("--locked") || build.include?("npm ci") ||
    (build.include?("go mod download") && build.include?("go mod verify"))
  errors << "#{name}: buildCommand must use a locked install" unless locked
end

enabled = services.map { |service| service["name"] }
required = %w[
  robintheclaw
  robin-research-collector
  robin-control-api
  robin-execution-coordinator
  robin-lighter-signer
  robin-robinhood-signer
]
required.each do |name|
  errors << "#{name}: service is missing" unless enabled.include?(name)
end

%w[robin-control-api robin-execution-coordinator robin-lighter-signer robin-robinhood-signer].each do |name|
  service = services.find { |item| item["name"] == name }
  errors << "#{name}: must be a private service" unless service&.fetch("type", nil) == "pserv"
end

{
  "robin-execution-coordinator" => "COORDINATOR_ENABLED",
  "robin-lighter-signer" => "LIGHTER_SIGNER_ENABLED",
  "robin-robinhood-signer" => "ROBINHOOD_SIGNER_ENABLED"
}.each do |name, key|
  service = services.find { |item| item["name"] == name }
  setting = service&.fetch("envVars", [])&.find { |variable| variable["key"] == key }
  errors << "#{name}: must enter the Blueprint disabled" unless setting&.fetch("value", nil) == "false"
  errors << "#{name}: disabled liveness check must use /livez" unless service&.fetch("healthCheckPath", nil) == "/livez"
end

control = services.find { |service| service["name"] == "robin-control-api" }
control_database = control&.fetch("envVars", [])&.find { |variable| variable["key"] == "DATABASE_URL" }
unless control_database&.dig("fromDatabase", "property") == "connectionString"
  errors << "robin-control-api: direct database connection required for read-only session policy"
end

database = blueprint.fetch("databases", []).find { |item| item["name"] == "robin-research" }
if database.nil?
  errors << "robin-research database is missing"
else
  errors << "robin-research: Pro database required" unless database["plan"].to_s.start_with?("pro-")
  errors << "robin-research: external access must be disabled" unless database["ipAllowList"] == []
  errors << "robin-research: PgBouncer required" unless database["connectionPool"] == "pgbouncer"
  errors << "robin-research: HA required" unless database.dig("highAvailability", "enabled") == true
  errors << "robin-research: storage autoscaling required" unless database["storageAutoscalingEnabled"] == true
end

abort(errors.join("\n")) unless errors.empty?
puts "Blueprint policy: clean"
