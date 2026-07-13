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
  robin-paper-agent
  robin-api
  robin-execution-coordinator
  robin-lighter-signer
  robin-robinhood-signer
]
required.each do |name|
  errors << "#{name}: service is missing" unless enabled.include?(name)
end

%w[robin-research-collector robin-paper-agent].each do |name|
  service = services.find { |item| item["name"] == name }
  errors << "#{name}: must be a background worker" unless service&.fetch("type", nil) == "worker"
  database = service&.fetch("envVars", [])&.find { |variable| variable["key"] == "DATABASE_URL" }
  migrations = service&.fetch("envVars", [])&.find do |variable|
    variable["key"] == "DATABASE_MIGRATIONS_URL"
  end
  unless database&.dig("fromDatabase", "property") == "connectionPoolString"
    errors << "#{name}: runtime database must use PgBouncer"
  end
  unless migrations&.dig("fromDatabase", "property") == "connectionString"
    errors << "#{name}: migrations require a direct database connection"
  end
end

collector = services.find { |service| service["name"] == "robin-research-collector" }
collector_env = collector&.fetch("envVars", [])&.map { |variable| variable["key"] }.compact || []
%w[ROBINHOOD_RPC_URL R2_BUCKET AWS_ENDPOINT_URL AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN].each do |key|
  errors << "robin-research-collector: #{key} is missing" unless collector_env.include?(key)
end

paper_agent = services.find { |service| service["name"] == "robin-paper-agent" }
paper_env = paper_agent&.fetch("envVars", [])&.map { |variable| variable["key"] }.compact || []
%w[PAPER_AGENT_CONFIG PAPER_MINIMUM_NET_EDGE_PPM ROBINHOOD_RPC_URL].each do |key|
  errors << "robin-paper-agent: #{key} is missing" unless paper_env.include?(key)
end

%w[robin-api robin-execution-coordinator robin-lighter-signer robin-robinhood-signer].each do |name|
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

product_api = services.find { |service| service["name"] == "robin-api" }
product_database = product_api&.fetch("envVars", [])&.find { |variable| variable["key"] == "DATABASE_URL" }
unless product_database&.dig("fromDatabase", "property") == "connectionString"
  errors << "robin-api: direct database connection required for migrations"
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

app_database = blueprint.fetch("databases", []).find { |item| item["name"] == "robin-app" }
if app_database.nil?
  errors << "robin-app database is missing"
else
  errors << "robin-app: Pro database required" unless app_database["plan"].to_s.start_with?("pro-")
  errors << "robin-app: external access must be disabled" unless app_database["ipAllowList"] == []
  errors << "robin-app: HA required" unless app_database.dig("highAvailability", "enabled") == true
  unless app_database["storageAutoscalingEnabled"] == true
    errors << "robin-app: storage autoscaling required"
  end
end

abort(errors.join("\n")) unless errors.empty?
puts "Blueprint policy: clean"
