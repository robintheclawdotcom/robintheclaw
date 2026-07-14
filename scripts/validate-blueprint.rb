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
  robin-live-evaluation
  robin-exit-quote-publisher
  robin-aapl-relay-1
  robin-aapl-relay-2
  robin-aapl-relay-3
  robin-lighter-provisioner
  robin-lighter-signer
  robin-robinhood-provisioner
  robin-robinhood-signer
]
required.each do |name|
  errors << "#{name}: service is missing" unless enabled.include?(name)
end

manual_inputs = {
  "robintheclaw" => %w[NEXT_PUBLIC_PRIVY_APP_ID PRIVY_VERIFICATION_KEY],
  "robin-api" => %w[
    APP_RPC_URL RH_MAINNET_RPC RH_RPC_FALLBACK VAULT_ADDRESS ANCHOR_ADDRESS GUARD_ADDRESS
    PRIVY_APP_ID PRIVY_APP_SECRET PRIVY_VERIFICATION_KEY
  ],
  "robin-research-collector" => %w[
    ROBINHOOD_RPC_URL R2_BUCKET AWS_ENDPOINT_URL AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY
    AWS_SESSION_TOKEN
  ],
  "robin-paper-agent" => %w[ROBINHOOD_RPC_URL],
  "robin-account-publisher" => %w[
    ACCOUNT_PUBLISHER_COORDINATOR_DATABASE_URL ACCOUNT_PUBLISHER_ROBINHOOD_DATABASE_URL
    ACCOUNT_PUBLISHER_ROBINHOOD_JOURNAL_DATABASE_URL ACCOUNT_PUBLISHER_PRIMARY_RPC_URL
    ACCOUNT_PUBLISHER_SECONDARY_RPC_URL ACCOUNT_PUBLISHER_MINIMUM_COLLATERAL_RAW
    ACCOUNT_PUBLISHER_MINIMUM_SETTLEMENT_RAW ACCOUNT_PUBLISHER_MINIMUM_OWNER_GAS_RAW
    ACCOUNT_PUBLISHER_MINIMUM_SIGNER_GAS_RAW
  ],
  "robin-quote-authority" => %w[ROBIN_QUOTE_AUTHORITY_ED25519_PRIVATE_KEY],
  "robin-lighter-provisioner" => %w[
    AWS_REGION AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_KMS_KEY_ID
  ],
  "robin-robinhood-provisioner" => %w[
    ROBINHOOD_RPC_URL ROBINHOOD_RECONCILIATION_RPC_URL ROBINHOOD_USER_VAULT_FACTORY
    ROBINHOOD_EXECUTION_REGISTRY ROBINHOOD_POLICY_DIGEST ROBINHOOD_FACTORY_CODE_HASH
    ROBINHOOD_REGISTRY_CODE_HASH ROBINHOOD_USER_VAULT_CODE_HASH
    ROBINHOOD_RISK_MANAGER_CODE_HASH ROBINHOOD_SPOT_ADAPTER_CODE_HASH AWS_REGION
    AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY
  ],
  "robin-robinhood-signer" => %w[
    ROBINHOOD_RPC_URL ROBINHOOD_RECONCILIATION_RPC_URL AWS_REGION AWS_ACCESS_KEY_ID
    AWS_SECRET_ACCESS_KEY ROBINHOOD_MAX_GAS_LIMIT ROBINHOOD_MAX_PRIORITY_FEE_WEI
    ROBINHOOD_MAX_FEE_PER_GAS_WEI ROBINHOOD_MAX_TRANSACTION_COST_WEI
    ROBINHOOD_MINIMUM_GAS_RESERVE_WEI ROBINHOOD_MAX_REPLACEMENTS
    ROBINHOOD_MAX_REPLACEMENT_AGE SIGNER_MAX_REQUESTS_PER_MINUTE
    SIGNER_MAX_CONCURRENT_REQUESTS
  ],
  "robin-aapl-relay-1" => %w[
    AAPL_RELAY_ARBITRUM_RPC_1 AAPL_RELAY_ARBITRUM_RPC_2 AAPL_RELAY_ROBINHOOD_RPC
    AAPL_RELAY_PUBLISHER_PRIVATE_KEY
  ],
  "robin-aapl-relay-2" => %w[
    AAPL_RELAY_ARBITRUM_RPC_1 AAPL_RELAY_ARBITRUM_RPC_2 AAPL_RELAY_ROBINHOOD_RPC
    AAPL_RELAY_PUBLISHER_PRIVATE_KEY
  ],
  "robin-aapl-relay-3" => %w[
    AAPL_RELAY_ARBITRUM_RPC_1 AAPL_RELAY_ARBITRUM_RPC_2 AAPL_RELAY_ROBINHOOD_RPC
    AAPL_RELAY_PUBLISHER_PRIVATE_KEY
  ]
}
manual_inputs.each do |name, keys|
  service = services.find { |item| item["name"] == name }
  env = service&.fetch("envVars", []) || []
  keys.each do |key|
    variable = env.find { |item| item["key"] == key }
    errors << "#{name}: required canary input #{key} must be declared sync:false" unless variable&.fetch("sync", nil) == false
  end
end

blueprint.fetch("envVarGroups", []).each do |group|
  variables = group.fetch("envVars", [])
  errors << "#{group.fetch("name")}: secret group must not be empty" if variables.empty?
  variables.each do |variable|
    errors << "#{group.fetch("name")}: #{variable.fetch("key", "unknown")} must be declared sync:false" unless variable["sync"] == false
  end
end

groups = blueprint.fetch("envVarGroups", []).to_h { |group| [group.fetch("name"), group] }
expected_group_keys = {
  "robin-quote-authority-robinhood-rpc" => %w[
    ROBINHOOD_RPC_URL ROBINHOOD_RECONCILIATION_RPC_URL
  ],
  "robin-lighter-market-spec" => %w[
    LIGHTER_AAPL_BASE_DECIMALS LIGHTER_AAPL_PRICE_DECIMALS
  ]
}
expected_group_keys.each do |name, expected|
  actual = groups.fetch(name, {}).fetch("envVars", []).map { |variable| variable["key"] }.compact
  errors << "#{name}: must contain only #{expected.join(", ")}" unless actual.sort == expected.sort
end

%w[robin-lighter-provisioner robin-lighter-signer].each do |name|
  service = services.find { |item| item["name"] == name }
  env = service&.fetch("envVars", []) || []
  unless env.any? { |variable| variable["fromGroup"] == "robin-lighter-market-spec" }
    errors << "#{name}: reviewed Lighter market specification is missing"
  end
  if env.any? do |variable|
       variable["fromGroup"] == "robin-quote-authority-robinhood-rpc" ||
         %w[ROBINHOOD_RPC_URL ROBINHOOD_RECONCILIATION_RPC_URL].include?(variable["key"])
     end
    errors << "#{name}: must not receive Robinhood RPC credentials"
  end
end

%w[
  robin-research-collector robin-paper-agent robin-live-scheduler robin-live-evaluation
  robin-exit-quote-publisher robin-aapl-relay-1 robin-aapl-relay-2 robin-aapl-relay-3
].each do |name|
  service = services.find { |item| item["name"] == name }
  errors << "#{name}: must be a background worker" unless service&.fetch("type", nil) == "worker"
  next if %w[
    robin-live-scheduler robin-live-evaluation robin-exit-quote-publisher
    robin-aapl-relay-1 robin-aapl-relay-2 robin-aapl-relay-3
  ].include?(name)

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

relay_services = %w[robin-aapl-relay-1 robin-aapl-relay-2 robin-aapl-relay-3].map do |name|
  services.find { |service| service["name"] == name }
end
unless relay_services.map { |service| service&.fetch("region", nil) }.uniq.length == 3
  errors << "AAPL relay publishers must run in three distinct regions"
end
relay_services.each do |service|
  env = service&.fetch("envVars", []) || []
  unless env.any? { |variable| variable["fromGroup"] == "robin-aapl-reference-feed-config" }
    errors << "#{service&.fetch("name", "AAPL relay")}: reference feed binding is missing"
  end
end

collector = services.find { |service| service["name"] == "robin-research-collector" }
collector_env = collector&.fetch("envVars", [])&.map { |variable| variable["key"] }.compact || []
%w[ROBINHOOD_RPC_URL R2_BUCKET AWS_ENDPOINT_URL AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN].each do |key|
  errors << "robin-research-collector: #{key} is missing" unless collector_env.include?(key)
end

paper_agent = services.find { |service| service["name"] == "robin-paper-agent" }
paper_env = paper_agent&.fetch("envVars", [])&.map { |variable| variable["key"] }.compact || []
%w[AGENT_DATABASE_URL PAPER_AGENT_CONFIG ROBINHOOD_RPC_URL].each do |key|
  errors << "robin-paper-agent: #{key} is missing" unless paper_env.include?(key)
end
unless paper_agent&.fetch("envVars", [])&.any? { |variable| variable["fromGroup"] == "robin-aapl-strategy-policy" }
  errors << "robin-paper-agent: shared AAPL strategy policy is missing"
end
unless paper_agent&.fetch("startCommand", "")&.include?("PAPER_MINIMUM_NET_EDGE_PPM=$AAPL_MINIMUM_NET_EDGE_PPM")
  errors << "robin-paper-agent: shared AAPL threshold mapping is missing"
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
  errors << "#{name}: canary service must be enabled" unless setting&.fetch("value", nil) == "true"
  errors << "#{name}: canary bootstrap health check must use #{health_path}" unless service&.fetch("healthCheckPath", nil) == health_path
end

scheduler = services.find { |service| service["name"] == "robin-live-scheduler" }
scheduler_enabled = scheduler&.fetch("envVars", [])&.find do |variable|
  variable["key"] == "ROBIN_LIVE_SCHEDULER_ENABLED"
end

evaluation = services.find { |service| service["name"] == "robin-live-evaluation" }
evaluation_env = evaluation&.fetch("envVars", []) || []
evaluation_enabled = evaluation_env.find do |variable|
  variable["key"] == "ROBIN_LIVE_EVALUATION_ENABLED"
end
unless evaluation_enabled&.fetch("value", nil) == "true"
  errors << "robin-live-evaluation: canary worker must be enabled"
end
{
  "ROBIN_LIVE_EVALUATION_RESEARCH_DATABASE_URL" => "robin-research",
  "ROBIN_LIVE_EVALUATION_PRODUCT_DATABASE_URL" => "robin-app",
  "ROBIN_LIVE_EVALUATION_EXECUTION_DATABASE_URL" => "robin-execution"
}.each do |key, database|
  setting = evaluation_env.find { |variable| variable["key"] == key }
  unless setting&.dig("fromDatabase", "name") == database &&
         setting&.dig("fromDatabase", "property") == "connectionString"
    errors << "robin-live-evaluation: #{key} must use the direct #{database} database"
  end
end
unless evaluation&.fetch("startCommand", "")&.include?("ROBIN_LIVE_EVALUATION_LIGHTER_AAPL_MARKET_INDEX=$LIGHTER_AAPL_MARKET_INDEX")
  errors << "robin-live-evaluation: reviewed Lighter market binding is missing"
end
unless evaluation_env.any? { |variable| variable["fromGroup"] == "robin-aapl-strategy-policy" }
  errors << "robin-live-evaluation: shared AAPL strategy policy is missing"
end
unless scheduler_enabled&.fetch("value", nil) == "true"
  errors << "robin-live-scheduler: canary worker must be enabled"
end

coordinator = services.find { |service| service["name"] == "robin-execution-coordinator" }
coordinator_env = coordinator&.fetch("envVars", []) || []
unless coordinator_env.any? { |variable| variable["key"] == "DATABASE_URL" && variable.dig("fromDatabase", "name") == "robin-execution" && variable.dig("fromDatabase", "property") == "connectionPoolString" }
  errors << "robin-execution-coordinator: pooled execution database binding is missing"
end
unless coordinator&.fetch("preDeployCommand", nil) == 'bash scripts/migrate-execution.sh "$DATABASE_MIGRATIONS_URL"'
  errors << "robin-execution-coordinator: execution migration pre-deploy command is missing"
end
coordinator_paths = coordinator&.dig("buildFilter", "paths") || []
%w[
  coordinator/** runtime/live-evaluation/migrations/** runtime/live-scheduler/migrations/**
  scripts/migrate-execution.sh
].each do |path|
  errors << "robin-execution-coordinator: build filter is missing #{path}" unless coordinator_paths.include?(path)
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
expected_product_config = {
  "APP_CHAIN_ID" => "4663",
  "RH_CHAIN_ID" => "4663",
  "AGENT_STRATEGY_VERSION" => "basis-aapl-v1",
  "EVM_ENABLED" => "true",
  "GEO_BLOCKING_ENABLED" => "false"
}
expected_product_config.each do |key, value|
  setting = product_env.find { |variable| variable["key"] == key }
  errors << "robin-api: #{key} must be #{value}" unless setting&.fetch("value", nil) == value
end
if services.any? { |service| service.fetch("envVars", []).any? { |variable| %w[ALCHEMY_API_KEY ALCHEMY_POLICY_ID].include?(variable["key"]) } }
  errors << "Alchemy must not be a live-canary deployment dependency"
end
unless product_env.any? { |variable| variable["fromGroup"] == "robin-coordinator-registration-auth" }
  errors << "robin-api: coordinator registration auth group is missing"
end
unless product_env.any? { |variable| variable["key"] == "COORDINATOR_REGISTRATION_URL" && variable.dig("fromService", "name") == "robin-execution-coordinator" }
  errors << "robin-api: coordinator registration service binding is missing"
end
unless product_env.any? { |variable| variable["key"] == "COORDINATOR_REGISTRATION_CALLER_ID" && variable["value"] == "product-account-provisioner" }
  errors << "robin-api: coordinator registration caller must be product-account-provisioner"
end

robinhood_signer = services.find { |service| service["name"] == "robin-robinhood-signer" }
unless robinhood_signer&.fetch("preDeployCommand", nil) == 'bash scripts/migrate-robinhood-signer.sh "$DATABASE_URL"'
  errors << "robin-robinhood-signer: signer journal migration pre-deploy command is missing"
end
signer_database = robinhood_signer&.fetch("envVars", [])&.find { |variable| variable["key"] == "DATABASE_URL" }
unless signer_database&.dig("fromDatabase", "name") == "robin-robinhood-custody" &&
       signer_database&.dig("fromDatabase", "property") == "connectionString"
  errors << "robin-robinhood-signer: signer journal migration requires a direct custody database binding"
end
signer_paths = robinhood_signer&.dig("buildFilter", "paths") || []
errors << "robin-robinhood-signer: build filter is missing scripts/migrate-robinhood-signer.sh" unless signer_paths.include?("scripts/migrate-robinhood-signer.sh")

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
