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
  robin-account-publisher
  robin-quote-authority
  robin-strategy-runner
  robin-live-scheduler
  robin-lighter-provisioner
  robin-lighter-signer
  robin-robinhood-provisioner
  robin-robinhood-signer
]
required.each do |name|
  errors << "#{name}: service is missing" unless enabled.include?(name)
end

%w[robin-research-collector robin-paper-agent robin-live-scheduler].each do |name|
  service = services.find { |item| item["name"] == name }
  errors << "#{name}: must be a background worker" unless service&.fetch("type", nil) == "worker"
  next if name == "robin-live-scheduler"

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
%w[AGENT_DATABASE_URL PAPER_AGENT_CONFIG PAPER_MINIMUM_NET_EDGE_PPM ROBINHOOD_RPC_URL].each do |key|
  errors << "robin-paper-agent: #{key} is missing" unless paper_env.include?(key)
end
agent_database = paper_agent&.fetch("envVars", [])&.find { |variable| variable["key"] == "AGENT_DATABASE_URL" }
unless agent_database&.dig("fromDatabase", "name") == "robin-app" &&
       agent_database&.dig("fromDatabase", "property") == "connectionString"
  errors << "robin-paper-agent: agent fanout requires the direct product database"
end

%w[
  robin-api
  robin-execution-coordinator
  robin-account-publisher
  robin-quote-authority
  robin-strategy-runner
  robin-lighter-provisioner
  robin-lighter-signer
  robin-robinhood-provisioner
  robin-robinhood-signer
].each do |name|
  service = services.find { |item| item["name"] == name }
  errors << "#{name}: must be a private service" unless service&.fetch("type", nil) == "pserv"
end

{
  "robin-execution-coordinator" => ["COORDINATOR_ENABLED", "/livez"],
  "robin-account-publisher" => ["ACCOUNT_PUBLISHER_ENABLED", "/healthz"],
  "robin-quote-authority" => ["ROBIN_QUOTE_AUTHORITY_ENABLED", "/health"],
  "robin-strategy-runner" => ["ROBIN_STRATEGY_RUNNER_ENABLED", "/health"],
  "robin-lighter-provisioner" => ["LIGHTER_PROVISIONER_ENABLED", "/livez"],
  "robin-lighter-signer" => ["LIGHTER_SIGNER_ENABLED", "/livez"],
  "robin-robinhood-provisioner" => ["ROBINHOOD_PROVISIONER_ENABLED", "/livez"],
  "robin-robinhood-signer" => ["ROBINHOOD_SIGNER_ENABLED", "/livez"]
}.each do |name, (key, health_path)|
  service = services.find { |item| item["name"] == name }
  setting = service&.fetch("envVars", [])&.find { |variable| variable["key"] == key }
  errors << "#{name}: must enter the Blueprint disabled" unless setting&.fetch("value", nil) == "false"
  errors << "#{name}: disabled liveness check must use #{health_path}" unless service&.fetch("healthCheckPath", nil) == health_path
end

scheduler = services.find { |service| service["name"] == "robin-live-scheduler" }
scheduler_enabled = scheduler&.fetch("envVars", [])&.find do |variable|
  variable["key"] == "ROBIN_LIVE_SCHEDULER_ENABLED"
end
unless scheduler_enabled&.fetch("value", nil) == "false"
  errors << "robin-live-scheduler: must enter the Blueprint disabled"
end

coordinator = services.find { |service| service["name"] == "robin-execution-coordinator" }
coordinator_env = coordinator&.fetch("envVars", []) || []
unless coordinator_env.any? { |variable| variable["key"] == "DATABASE_URL" && variable.dig("fromDatabase", "name") == "robin-execution" && variable.dig("fromDatabase", "property") == "connectionPoolString" }
  errors << "robin-execution-coordinator: pooled execution database binding is missing"
end
unless coordinator_env.any? { |variable| variable["fromGroup"] == "robin-coordinator-registration-auth" }
  errors << "robin-execution-coordinator: registration auth group is missing"
end
unless coordinator_env.any? { |variable| variable["key"] == "REGISTRATION_CALLER_ID" && variable["value"] == "product-account-provisioner" }
  errors << "robin-execution-coordinator: registration caller must be product-account-provisioner"
end
unless coordinator_env.any? { |variable| variable["key"] == "INTENT_CALLER_ID" && variable["value"] == "strategy-runner" }
  errors << "robin-execution-coordinator: intent caller must be strategy-runner"
end
unless coordinator_env.any? { |variable| variable["key"] == "EXIT_CALLER_ID" && variable["value"] == "strategy-runner-exit" }
  errors << "robin-execution-coordinator: exit caller must be strategy-runner-exit"
end
unless coordinator_env.any? { |variable| variable["key"] == "MARKET_QUOTE_CALLER_ID" && variable["value"] == "quote-authority-market" }
  errors << "robin-execution-coordinator: market caller must be quote-authority-market"
end

publisher = services.find { |service| service["name"] == "robin-account-publisher" }
publisher_env = publisher&.fetch("envVars", []) || []
%w[
  ACCOUNT_PUBLISHER_COORDINATOR_DATABASE_URL
  ACCOUNT_PUBLISHER_ROBINHOOD_DATABASE_URL
  ACCOUNT_PUBLISHER_ROBINHOOD_JOURNAL_DATABASE_URL
  ACCOUNT_PUBLISHER_PRIMARY_RPC_URL
  ACCOUNT_PUBLISHER_SECONDARY_RPC_URL
  ACCOUNT_PUBLISHER_LIGHTER_BRIDGE_URL
  ACCOUNT_PUBLISHER_COORDINATOR_URL
  ACCOUNT_PUBLISHER_APPLICATION_URL
  ACCOUNT_PUBLISHER_MINIMUM_COLLATERAL_RAW
  ACCOUNT_PUBLISHER_MINIMUM_SETTLEMENT_RAW
  ACCOUNT_PUBLISHER_MINIMUM_OWNER_GAS_RAW
  ACCOUNT_PUBLISHER_MINIMUM_SIGNER_GAS_RAW
].each do |key|
  errors << "robin-account-publisher: #{key} is missing" unless publisher_env.any? { |variable| variable["key"] == key }
end
%w[
  robin-lighter-publisher-bridge-auth
  robin-lighter-market-config
  robin-coordinator-account-auth
  robin-readiness-publisher-auth
].each do |group|
  errors << "robin-account-publisher: #{group} is missing" unless publisher_env.any? { |variable| variable["fromGroup"] == group }
end

runner = services.find { |service| service["name"] == "robin-strategy-runner" }
runner_env = runner&.fetch("envVars", []) || []
unless runner_env.any? { |variable| variable["fromGroup"] == "robin-coordinator-intent-auth" }
  errors << "robin-strategy-runner: coordinator intent auth group is missing"
end
unless runner_env.any? { |variable| variable["fromGroup"] == "robin-coordinator-exit-auth" }
  errors << "robin-strategy-runner: coordinator exit auth group is missing"
end
unless runner_env.any? { |variable| variable["key"] == "ROBIN_COORDINATOR_HOSTPORT" && variable.dig("fromService", "name") == "robin-execution-coordinator" }
  errors << "robin-strategy-runner: coordinator service binding is missing"
end
unless runner_env.any? { |variable| variable["key"] == "ROBIN_COORDINATOR_INTENT_CALLER" && variable["value"] == "strategy-runner" }
  errors << "robin-strategy-runner: coordinator caller must be strategy-runner"
end
unless runner_env.any? { |variable| variable["key"] == "ROBIN_COORDINATOR_EXIT_CALLER" && variable["value"] == "strategy-runner-exit" }
  errors << "robin-strategy-runner: coordinator exit caller must be strategy-runner-exit"
end

quote = services.find { |service| service["name"] == "robin-quote-authority" }
quote_env = quote&.fetch("envVars", []) || []
unless quote_env.any? { |variable| variable["key"] == "ROBIN_COORDINATOR_MARKET_CALLER" && variable["value"] == "quote-authority-market" }
  errors << "robin-quote-authority: coordinator market caller must be quote-authority-market"
end
unless quote_env.any? { |variable| variable["fromGroup"] == "robin-coordinator-market-auth" }
  errors << "robin-quote-authority: coordinator market auth group is missing"
end

scheduler_env = scheduler&.fetch("envVars", []) || []
unless scheduler_env.any? { |variable| variable["key"] == "ROBIN_LIVE_SCHEDULER_DATABASE_URL" && variable.dig("fromDatabase", "name") == "robin-execution" && variable.dig("fromDatabase", "property") == "connectionString" }
  errors << "robin-live-scheduler: direct coordinator database binding is missing"
end
unless scheduler_env.any? { |variable| variable["key"] == "ROBIN_LIVE_SCHEDULER_QUOTE_CALLER" && variable["value"] == "live-scheduler-quote" }
  errors << "robin-live-scheduler: quote caller is invalid"
end
unless scheduler_env.any? { |variable| variable["key"] == "ROBIN_LIVE_SCHEDULER_RUNNER_CALLER" && variable["value"] == "live-scheduler-runner" }
  errors << "robin-live-scheduler: runner caller is invalid"
end
%w[
  robin-quote-authority-request-auth
  robin-strategy-runner-request-auth
  robin-quote-authority-public-key
  robin-lighter-market-config
].each do |group|
  errors << "robin-live-scheduler: #{group} is missing" unless scheduler_env.any? { |variable| variable["fromGroup"] == group }
end
unless scheduler&.fetch("startCommand", "")&.include?("ROBIN_LIVE_SCHEDULER_LIGHTER_AAPL_MARKET_INDEX=$LIGHTER_AAPL_MARKET_INDEX")
  errors << "robin-live-scheduler: reviewed Lighter market binding is missing"
end

product_api = services.find { |service| service["name"] == "robin-api" }
product_database = product_api&.fetch("envVars", [])&.find { |variable| variable["key"] == "DATABASE_URL" }
unless product_database&.dig("fromDatabase", "property") == "connectionString"
  errors << "robin-api: direct database connection required for migrations"
end
product_env = product_api&.fetch("envVars", []) || []
unless product_env.any? { |variable| variable["fromGroup"] == "robin-coordinator-registration-auth" }
  errors << "robin-api: coordinator registration auth group is missing"
end
unless product_env.any? { |variable| variable["key"] == "COORDINATOR_REGISTRATION_URL" && variable.dig("fromService", "name") == "robin-execution-coordinator" }
  errors << "robin-api: coordinator registration service binding is missing"
end
unless product_env.any? { |variable| variable["key"] == "COORDINATOR_REGISTRATION_CALLER_ID" && variable["value"] == "product-account-provisioner" }
  errors << "robin-api: coordinator registration caller must be product-account-provisioner"
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

execution_database = blueprint.fetch("databases", []).find { |item| item["name"] == "robin-execution" }
if execution_database.nil?
  errors << "robin-execution database is missing"
else
  errors << "robin-execution: Pro database required" unless execution_database["plan"].to_s.start_with?("pro-")
  errors << "robin-execution: external access must be disabled" unless execution_database["ipAllowList"] == []
  errors << "robin-execution: PgBouncer required" unless execution_database["connectionPool"] == "pgbouncer"
  errors << "robin-execution: HA required" unless execution_database.dig("highAvailability", "enabled") == true
  unless execution_database["storageAutoscalingEnabled"] == true
    errors << "robin-execution: storage autoscaling required"
  end
end

abort(errors.join("\n")) unless errors.empty?
puts "Blueprint policy: clean"
