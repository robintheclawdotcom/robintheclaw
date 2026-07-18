#!/usr/bin/env ruby

require "yaml"
require "shellwords"

path = ARGV.fetch(0, File.expand_path("../render.yaml", __dir__))
blueprint = YAML.safe_load(File.read(path), aliases: false, filename: path)
errors = []

%w[services databases envVarGroups].each do |section|
  errors << "#{section} must be nested under Robin the Claw/Production" if blueprint.key?(section)
end

projects = blueprint.fetch("projects", [])
errors << "Blueprint must manage exactly one project" unless projects.length == 1
project = projects.find { |item| item["name"] == "Robin the Claw" }
errors << "Blueprint project must be Robin the Claw" unless project
project ||= projects.first || {}
environments = project.fetch("environments", [])
errors << "Robin the Claw must have exactly one managed environment" unless environments.length == 1
environment = environments.find { |item| item["name"] == "Production" }
errors << "Blueprint environment must be Production" unless environment
environment ||= environments.first || {}
unless environment.dig("networking", "isolation") == "enabled"
  errors << "Production private-network isolation must be enabled"
end
unless environment.dig("permissions", "protection") == "enabled"
  errors << "Production environment protection must be enabled"
end
unless blueprint.dig("previews", "generation") == "off"
  errors << "root preview generation must be off"
end

services = environment.fetch("services", [])
databases = environment.fetch("databases", [])
declared_groups = environment.fetch("envVarGroups", [])
errors << "Production must contain exactly 20 services" unless services.length == 20
errors << "Production must contain exactly 5 databases" unless databases.length == 5
errors << "Production must contain exactly 23 generated environment groups" unless declared_groups.length == 23
errors << "service names must be unique" unless services.map { |service| service["name"] }.uniq.length == services.length
errors << "database names must be unique" unless databases.map { |database| database["name"] }.uniq.length == databases.length
unless services.count { |service| service["plan"] == "starter" } == 18 &&
       services.count { |service| service["plan"] == "standard" } == 2
  errors << "Production service plans must be 18 starter and 2 standard"
end

database_regions = databases.to_h { |database| [database["name"], database["region"]] }
required_databases = %w[
  robin-app
  robin-research
  robin-lighter-credentials
  robin-execution
  robin-robinhood-custody
]
database_versions = {
  "robin-app" => "16",
  "robin-research" => "17",
  "robin-lighter-credentials" => "18",
  "robin-execution" => "18",
  "robin-robinhood-custody" => "18"
}
unless databases.map { |database| database["name"] }.sort == required_databases.sort
  errors << "Production database set does not match the reviewed topology"
end
databases.each do |database|
  name = database.fetch("name", "unnamed")
  errors << "#{name}: region must be virginia" unless database["region"] == "virginia"
  errors << "#{name}: plan must be pro-4gb" unless database["plan"] == "pro-4gb"
  errors << "#{name}: storage must start at 100 GB" unless database["diskSizeGB"] == 100
  errors << "#{name}: storage autoscaling required" unless database["storageAutoscalingEnabled"] == true
  unless database["postgresMajorVersion"] == database_versions[name]
    errors << "#{name}: PostgreSQL major version does not match the reviewed topology"
  end
  errors << "#{name}: HA required" unless database.dig("highAvailability", "enabled") == true
  errors << "#{name}: external access must be disabled" unless database["ipAllowList"] == []
end

errors << "services must not be empty" if services.empty?
services.each do |service|
  name = service.fetch("name", "unnamed")
  errors << "#{name}: region must be virginia" unless service["region"] == "virginia"
  errors << "#{name}: rootDir is forbidden" if service.key?("rootDir")
  errors << "#{name}: service-level previews are forbidden" if service.key?("previews")
  expected_trigger = "off"
  unless service["autoDeployTrigger"] == expected_trigger
    errors << "#{name}: autoDeployTrigger must be #{expected_trigger}"
  end
  build = service.fetch("buildCommand", "")
  locked = build.include?("--locked") || build.include?("npm ci") ||
    (build.include?("go mod download") && build.include?("go mod verify"))
  errors << "#{name}: buildCommand must use a locked install" unless locked
  if service["runtime"] == "go"
    versions = service.fetch("envVars", []).select { |item| item["key"] == "GO_VERSION" }
    unless versions == [{ "key" => "GO_VERSION", "value" => "1.26.5" }]
      errors << "#{name}: GO_VERSION must be pinned to 1.26.5"
    end
  end
  service.fetch("envVars", []).each do |variable|
    database = variable["fromDatabase"]
    next unless database

    database_name = database["name"]
    database_region = database_regions[database_name]
    errors << "#{name}: referenced database #{database_name} is outside Production" unless database_region
    if database_region && service["region"] != database_region
      errors << "#{name}: database #{database_name} must be in service region #{service["region"]}"
    end
  end
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
  robin-live-control
  robin-exit-quote-publisher
  robin-sequencer-publisher-1
  robin-sequencer-publisher-2
  robin-sequencer-publisher-3
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

web = services.find { |service| service["name"] == "robintheclaw" }
app_origin = web&.fetch("envVars", [])&.find { |variable| variable["key"] == "APP_ORIGIN" }
unless app_origin == { "key" => "APP_ORIGIN", "value" => "https://robintheclaw.com" }
  errors << "robintheclaw: APP_ORIGIN must be pinned to https://robintheclaw.com"
end

resource_count = services.length + databases.length
errors << "Render resource limit exceeded: #{resource_count}/25" if resource_count > 25

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
    ACCOUNT_PUBLISHER_PRIMARY_RPC_URL ACCOUNT_PUBLISHER_SECONDARY_RPC_URL
    ACCOUNT_PUBLISHER_MINIMUM_COLLATERAL_RAW
    ACCOUNT_PUBLISHER_MINIMUM_SETTLEMENT_RAW ACCOUNT_PUBLISHER_MINIMUM_OWNER_GAS_RAW
    ACCOUNT_PUBLISHER_MINIMUM_SIGNER_GAS_RAW
  ],
  "robin-quote-authority" => %w[ROBIN_QUOTE_AUTHORITY_ED25519_PRIVATE_KEY],
  "robin-lighter-provisioner" => %w[
    AWS_ROLE_ARN AWS_KMS_KEY_ID
  ],
  "robin-robinhood-provisioner" => %w[
    ROBINHOOD_RPC_URL ROBINHOOD_RECONCILIATION_RPC_URL ROBINHOOD_USER_VAULT_FACTORY
    ROBINHOOD_EXECUTION_REGISTRY ROBINHOOD_POLICY_DIGEST ROBINHOOD_FACTORY_CODE_HASH
    ROBINHOOD_REGISTRY_CODE_HASH ROBINHOOD_USER_VAULT_CODE_HASH
    ROBINHOOD_RISK_MANAGER_CODE_HASH ROBINHOOD_SPOT_ADAPTER_CODE_HASH AWS_ROLE_ARN
    ROBINHOOD_KMS_PROVISION_FUNCTION_ARN
  ],
  "robin-robinhood-signer" => %w[
    ROBINHOOD_RPC_URL ROBINHOOD_RECONCILIATION_RPC_URL AWS_ROLE_ARN
    ROBINHOOD_MAX_GAS_LIMIT ROBINHOOD_MAX_PRIORITY_FEE_WEI
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

%w[robin-lighter-provisioner robin-robinhood-provisioner robin-robinhood-signer].each do |name|
  service = services.find { |item| item["name"] == name }
  env = service&.fetch("envVars", []) || []
  region = env.find { |item| item["key"] == "AWS_REGION" }
  errors << "#{name}: AWS_REGION must be pinned to us-east-1" unless region&.fetch("value", nil) == "us-east-1"
  %w[AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_WEB_IDENTITY_TOKEN_FILE].each do |key|
    errors << "#{name}: static or synthetic AWS credential #{key} is forbidden" if env.any? { |item| item["key"] == key }
  end
end

generated_group_keys = {
  "robin-quote-authority-exit-auth" => %w[ROBIN_QUOTE_AUTHORITY_EXIT_HMAC_KEY],
  "robin-quote-authority-request-auth" => %w[ROBIN_QUOTE_AUTHORITY_HMAC_KEY],
  "robin-strategy-runner-request-auth" => %w[ROBIN_STRATEGY_RUNNER_HMAC_KEY],
  "robin-db-app-api" => %w[ROBIN_APP_API_DATABASE_PASSWORD],
  "robin-db-app-paper" => %w[ROBIN_APP_PAPER_DATABASE_PASSWORD],
  "robin-db-app-readonly" => %w[ROBIN_APP_READONLY_DATABASE_PASSWORD],
  "robin-db-research-collector" => %w[ROBIN_RESEARCH_COLLECTOR_DATABASE_PASSWORD],
  "robin-db-research-paper" => %w[ROBIN_RESEARCH_PAPER_DATABASE_PASSWORD],
  "robin-db-research-readonly" => %w[ROBIN_RESEARCH_READONLY_DATABASE_PASSWORD],
  "robin-db-execution-coordinator" => %w[ROBIN_EXECUTION_COORDINATOR_DATABASE_PASSWORD],
  "robin-db-execution-live-control" => %w[ROBIN_EXECUTION_LIVE_CONTROL_DATABASE_PASSWORD],
  "robin-db-execution-sequencer-1" => %w[ROBIN_EXECUTION_SEQUENCER_1_DATABASE_PASSWORD],
  "robin-db-execution-sequencer-2" => %w[ROBIN_EXECUTION_SEQUENCER_2_DATABASE_PASSWORD],
  "robin-db-execution-sequencer-3" => %w[ROBIN_EXECUTION_SEQUENCER_3_DATABASE_PASSWORD],
  "robin-db-execution-aapl-relay-1" => %w[ROBIN_EXECUTION_AAPL_RELAY_1_DATABASE_PASSWORD],
  "robin-db-execution-aapl-relay-2" => %w[ROBIN_EXECUTION_AAPL_RELAY_2_DATABASE_PASSWORD],
  "robin-db-execution-aapl-relay-3" => %w[ROBIN_EXECUTION_AAPL_RELAY_3_DATABASE_PASSWORD],
  "robin-db-execution-readonly" => %w[ROBIN_EXECUTION_READONLY_DATABASE_PASSWORD],
  "robin-db-lighter-provisioner" => %w[ROBIN_LIGHTER_PROVISIONER_DATABASE_PASSWORD],
  "robin-db-lighter-readonly" => %w[ROBIN_LIGHTER_READONLY_DATABASE_PASSWORD],
  "robin-db-custody-provisioner" => %w[ROBIN_CUSTODY_PROVISIONER_DATABASE_PASSWORD],
  "robin-db-custody-signer" => %w[ROBIN_CUSTODY_SIGNER_DATABASE_PASSWORD],
  "robin-db-custody-readonly" => %w[ROBIN_CUSTODY_READONLY_DATABASE_PASSWORD]
}
external_group_keys = {
  "robin-lighter-provisioner-auth" => %w[LIGHTER_PROVISIONER_HMAC_KEY],
  "robin-readiness-publisher-auth" => %w[READINESS_HMAC_KEY],
  "robin-lighter-signer-auth" => %w[LIGHTER_SIGNER_HMAC_KEY],
  "robin-lighter-signer-bridge-auth" => %w[LIGHTER_SIGNER_BRIDGE_HMAC_KEY],
  "robin-lighter-publisher-bridge-auth" => %w[LIGHTER_PUBLISHER_BRIDGE_HMAC_KEY],
  "robin-lighter-market-config" => %w[LIGHTER_AAPL_MARKET_INDEX],
  "robin-aapl-strategy-policy" => %w[AAPL_MINIMUM_NET_EDGE_PPM AAPL_STRATEGY_POLICY_SALT],
  "robin-coordinator-episode-auth" => %w[COORDINATOR_EPISODE_HMAC_KEY],
  "robin-quote-authority-robinhood-rpc" => %w[
    ROBINHOOD_RPC_URL ROBINHOOD_RECONCILIATION_RPC_URL
  ],
  "robin-lighter-market-spec" => %w[
    LIGHTER_AAPL_BASE_DECIMALS LIGHTER_AAPL_PRICE_DECIMALS
  ],
  "robin-aapl-reference-feed-config" => %w[
    AAPL_REFERENCE_FEED AAPL_REFERENCE_FEED_CODE_HASH AAPL_SOURCE_FEED_CODE_HASH
    AAPL_SOURCE_AGGREGATOR AAPL_SOURCE_AGGREGATOR_CODE_HASH
    AAPL_REFERENCE_FEED_DECIMALS AAPL_REFERENCE_FEED_HEARTBEAT_SECONDS
  ],
  "robin-robinhood-signer-auth" => %w[ROBINHOOD_SIGNER_HMAC_KEY],
  "robin-robinhood-provisioner-auth" => %w[ROBINHOOD_PROVISIONER_HMAC_KEY],
  "robin-robinhood-signer-bridge-auth" => %w[ROBINHOOD_SIGNER_BRIDGE_HMAC_KEY],
  "robin-coordinator-intent-auth" => %w[COORDINATOR_INTENT_HMAC_KEY],
  "robin-coordinator-exit-auth" => %w[COORDINATOR_EXIT_HMAC_KEY],
  "robin-coordinator-venue-auth" => %w[COORDINATOR_VENUE_HMAC_KEY],
  "robin-coordinator-market-auth" => %w[COORDINATOR_MARKET_HMAC_KEY],
  "robin-coordinator-account-auth" => %w[COORDINATOR_ACCOUNT_HMAC_KEY],
  "robin-coordinator-control-auth" => %w[COORDINATOR_CONTROL_HMAC_KEY],
  "robin-coordinator-registration-auth" => %w[COORDINATOR_REGISTRATION_HMAC_KEY],
  "robin-quote-authority-public-key" => %w[ROBIN_QUOTE_AUTHORITY_ED25519_PUBLIC_KEY],
  "robin-sequencer-feed-config" => %w[
    SEQUENCER_FEED_ADDRESS SEQUENCER_FEED_CODE_HASH
    SEQUENCER_USDG_PROXY_CODE_HASH SEQUENCER_USDG_IMPLEMENTATION_ADDRESS
    SEQUENCER_USDG_IMPLEMENTATION_CODE_HASH SEQUENCER_AAPL_PROXY_CODE_HASH
    SEQUENCER_AAPL_BEACON_ADDRESS SEQUENCER_AAPL_BEACON_CODE_HASH
    SEQUENCER_AAPL_IMPLEMENTATION_ADDRESS SEQUENCER_AAPL_IMPLEMENTATION_CODE_HASH
  ]
}
groups = declared_groups.to_h { |group| [group.fetch("name"), group] }
unless groups.keys.sort == generated_group_keys.keys.sort
  errors << "Blueprint generated environment groups do not match the reviewed auth and database roles"
end
generated_group_keys.each do |name, expected|
  variables = groups.fetch(name, {}).fetch("envVars", [])
  actual = variables.map { |variable| variable["key"] }.compact
  errors << "#{name}: must contain only #{expected.join(", ")}" unless actual.sort == expected.sort
  variables.each do |variable|
    unless variable["generateValue"] == true && variable.keys.sort == %w[generateValue key]
      errors << "#{name}: #{variable.fetch("key", "unknown")} must use only generateValue:true"
    end
  end
end
external_group_keys.each_key do |name|
  errors << "#{name}: externally managed group must not be declared in the Blueprint" if groups.key?(name)
end

allowed_groups = generated_group_keys.keys + external_group_keys.keys
referenced_groups = services.flat_map do |service|
  service.fetch("envVars", []).map { |variable| variable["fromGroup"] }.compact
end.uniq
(referenced_groups - allowed_groups).each do |name|
  errors << "#{name}: service references an unknown environment group"
end
(allowed_groups - referenced_groups).each do |name|
  errors << "#{name}: required environment group is not referenced"
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
  robin-research-collector robin-paper-agent robin-live-control
  robin-exit-quote-publisher robin-aapl-relay-1 robin-aapl-relay-2 robin-aapl-relay-3
].each do |name|
  service = services.find { |item| item["name"] == name }
  errors << "#{name}: must be a background worker" unless service&.fetch("type", nil) == "worker"
end

relay_services = %w[robin-aapl-relay-1 robin-aapl-relay-2 robin-aapl-relay-3].map do |name|
  services.find { |service| service["name"] == name }
end
unless relay_services.all? { |service| service&.fetch("region", nil) == "virginia" }
  errors << "AAPL relay publishers must share the execution database region"
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
%w[AGENT_DATABASE_OWNER_URL PAPER_AGENT_CONFIG ROBINHOOD_RPC_URL].each do |key|
  errors << "robin-paper-agent: #{key} is missing" unless paper_env.include?(key)
end
unless paper_agent&.fetch("envVars", [])&.any? { |variable| variable["fromGroup"] == "robin-aapl-strategy-policy" }
  errors << "robin-paper-agent: shared AAPL strategy policy is missing"
end
unless paper_agent&.fetch("startCommand", "")&.include?("PAPER_MINIMUM_NET_EDGE_PPM=$AAPL_MINIMUM_NET_EDGE_PPM")
  errors << "robin-paper-agent: shared AAPL threshold mapping is missing"
end
agent_database = paper_agent&.fetch("envVars", [])&.find { |variable| variable["key"] == "AGENT_DATABASE_OWNER_URL" }
unless agent_database&.dig("fromDatabase", "name") == "robin-app" &&
       agent_database&.dig("fromDatabase", "property") == "connectionString"
  errors << "robin-paper-agent: agent fanout migration owner binding is invalid"
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
  "robin-execution-coordinator" => "COORDINATOR_ENABLED",
  "robin-account-publisher" => "ACCOUNT_PUBLISHER_ENABLED",
  "robin-quote-authority" => "ROBIN_QUOTE_AUTHORITY_ENABLED",
  "robin-strategy-runner" => "ROBIN_STRATEGY_RUNNER_ENABLED",
  "robin-lighter-provisioner" => "LIGHTER_PROVISIONER_ENABLED",
  "robin-lighter-signer" => "LIGHTER_SIGNER_ENABLED",
  "robin-robinhood-provisioner" => "ROBINHOOD_PROVISIONER_ENABLED",
  "robin-robinhood-signer" => "ROBINHOOD_SIGNER_ENABLED"
}.each do |name, key|
  service = services.find { |item| item["name"] == name }
  setting = service&.fetch("envVars", [])&.find { |variable| variable["key"] == key }
  errors << "#{name}: canary service must be enabled" unless setting&.fetch("value", nil) == "true"
end

services.select { |service| service["type"] == "pserv" }.each do |service|
  if service.key?("healthCheckPath")
    errors << "#{service.fetch("name")}: private services cannot declare healthCheckPath"
  end
end

scheduler = services.find { |service| service["name"] == "robin-live-control" }
scheduler_enabled = scheduler&.fetch("envVars", [])&.find do |variable|
  variable["key"] == "ROBIN_LIVE_SCHEDULER_ENABLED"
end

evaluation = services.find { |service| service["name"] == "robin-live-control" }
evaluation_env = evaluation&.fetch("envVars", []) || []
{
  "ROBIN_RUNTIME_PROBE_API_HOSTPORT" => "robin-api",
  "ROBIN_RUNTIME_PROBE_COORDINATOR_HOSTPORT" => "robin-execution-coordinator",
  "ROBIN_RUNTIME_PROBE_ACCOUNT_PUBLISHER_HOSTPORT" => "robin-account-publisher",
  "ROBIN_RUNTIME_PROBE_QUOTE_AUTHORITY_HOSTPORT" => "robin-quote-authority",
  "ROBIN_RUNTIME_PROBE_STRATEGY_RUNNER_HOSTPORT" => "robin-strategy-runner",
  "ROBIN_RUNTIME_PROBE_LIGHTER_PROVISIONER_HOSTPORT" => "robin-lighter-provisioner",
  "ROBIN_RUNTIME_PROBE_LIGHTER_SIGNER_HOSTPORT" => "robin-lighter-signer",
  "ROBIN_RUNTIME_PROBE_ROBINHOOD_PROVISIONER_HOSTPORT" => "robin-robinhood-provisioner",
  "ROBIN_RUNTIME_PROBE_ROBINHOOD_SIGNER_HOSTPORT" => "robin-robinhood-signer"
}.each do |key, target|
  binding = evaluation_env.find { |variable| variable["key"] == key }
  unless binding&.dig("fromService", "name") == target &&
         binding&.dig("fromService", "type") == "pserv" &&
         binding&.dig("fromService", "property") == "hostport"
    errors << "robin-live-control: #{key} must use the reviewed #{target} private service binding"
  end
end
evaluation_enabled = evaluation_env.find do |variable|
  variable["key"] == "ROBIN_LIVE_EVALUATION_ENABLED"
end
unless evaluation_enabled&.fetch("value", nil) == "true"
  errors << "robin-live-evaluation: canary worker must be enabled"
end
{
  "ROBIN_LIVE_CONTROL_RESEARCH_DATABASE_OWNER_URL" => "robin-research",
  "ROBIN_LIVE_CONTROL_APP_DATABASE_OWNER_URL" => "robin-app",
  "ROBIN_LIVE_CONTROL_EXECUTION_DATABASE_OWNER_URL" => "robin-execution"
}.each do |key, database|
  setting = evaluation_env.find { |variable| variable["key"] == key }
  unless setting&.dig("fromDatabase", "name") == database &&
         setting&.dig("fromDatabase", "property") == "connectionString"
    errors << "robin-live-evaluation: #{key} must use the direct #{database} migration-owner binding"
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
unless coordinator_env.any? { |variable| variable["key"] == "DATABASE_OWNER_URL" && variable.dig("fromDatabase", "name") == "robin-execution" && variable.dig("fromDatabase", "property") == "connectionString" }
  errors << "robin-execution-coordinator: direct migration-owner binding is missing"
end
unless coordinator&.fetch("preDeployCommand", "").include?(
  'scripts/prepare-database.sh execution "$DATABASE_OWNER_URL" robin_execution_coordinator'
)
  errors << "robin-execution-coordinator: isolated migration and role preparation is missing"
end
coordinator_paths = coordinator&.dig("buildFilter", "paths") || []
%w[
  coordinator/** runtime/live-evaluation/migrations/** runtime/live-scheduler/migrations/**
  runtime/sequencer-publisher/migrations/** scripts/database-runtime-exec.rb
  scripts/lock-execution-after-migration.sql scripts/migrate-execution.sh
  scripts/prepare-database.sh scripts/provision-database-roles.sh scripts/psql-with-url.rb
  scripts/run-ordered-migrations.sh
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
  ACCOUNT_PUBLISHER_COORDINATOR_DATABASE_OWNER_URL
  ACCOUNT_PUBLISHER_CUSTODY_DATABASE_OWNER_URL
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
unless scheduler_env.any? { |variable| variable["key"] == "ROBIN_LIVE_CONTROL_EXECUTION_DATABASE_OWNER_URL" && variable.dig("fromDatabase", "name") == "robin-execution" && variable.dig("fromDatabase", "property") == "connectionString" }
  errors << "robin-live-scheduler: direct migration-owner binding is missing"
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
product_database = product_api&.fetch("envVars", [])&.find { |variable| variable["key"] == "DATABASE_OWNER_URL" }
unless product_database&.dig("fromDatabase", "property") == "connectionString"
  errors << "robin-api: direct migration-owner binding is required"
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

database_contracts = {
  "robin-api" => {
    owners: { "DATABASE_OWNER_URL" => "robin-app" },
    groups: %w[robin-db-app-api],
    roles: %w[robin_app_api],
    flags: { "APP_RUN_MIGRATIONS" => "false" }
  },
  "robin-research-collector" => {
    owners: { "DATABASE_OWNER_URL" => "robin-research" },
    groups: %w[robin-db-research-collector],
    roles: %w[robin_research_collector],
    flags: { "RUNTIME_RUN_MIGRATIONS" => "false" }
  },
  "robin-paper-agent" => {
    owners: {
      "DATABASE_OWNER_URL" => "robin-research",
      "AGENT_DATABASE_OWNER_URL" => "robin-app"
    },
    groups: %w[robin-db-research-paper robin-db-app-paper],
    roles: %w[robin_research_paper robin_app_paper],
    flags: { "RUNTIME_RUN_MIGRATIONS" => "false" }
  },
  "robin-execution-coordinator" => {
    owners: { "DATABASE_OWNER_URL" => "robin-execution" },
    groups: %w[robin-db-execution-coordinator],
    roles: %w[robin_execution_coordinator],
    flags: {}
  },
  "robin-account-publisher" => {
    owners: {
      "ACCOUNT_PUBLISHER_COORDINATOR_DATABASE_OWNER_URL" => "robin-execution",
      "ACCOUNT_PUBLISHER_CUSTODY_DATABASE_OWNER_URL" => "robin-robinhood-custody"
    },
    groups: %w[robin-db-execution-readonly robin-db-custody-readonly],
    roles: %w[robin_execution_readonly robin_custody_readonly],
    flags: {}
  },
  "robin-exit-quote-publisher" => {
    owners: { "ROBIN_EXIT_QUOTE_PUBLISHER_DATABASE_OWNER_URL" => "robin-execution" },
    groups: %w[robin-db-execution-readonly],
    roles: %w[robin_execution_readonly],
    flags: {}
  },
  "robin-live-control" => {
    owners: {
      "ROBIN_LIVE_CONTROL_EXECUTION_DATABASE_OWNER_URL" => "robin-execution",
      "ROBIN_LIVE_CONTROL_RESEARCH_DATABASE_OWNER_URL" => "robin-research",
      "ROBIN_LIVE_CONTROL_APP_DATABASE_OWNER_URL" => "robin-app"
    },
    groups: %w[
      robin-db-execution-live-control robin-db-research-readonly robin-db-app-readonly
    ],
    roles: %w[robin_execution_live_control robin_research_readonly robin_app_readonly],
    flags: {}
  },
  "robin-sequencer-publisher-1" => {
    owners: { "SEQUENCER_DATABASE_OWNER_URL" => "robin-execution" },
    groups: %w[robin-db-execution-sequencer-1],
    roles: %w[robin_execution_sequencer_1],
    flags: { "SEQUENCER_RUN_MIGRATIONS" => "false" }
  },
  "robin-sequencer-publisher-2" => {
    owners: { "SEQUENCER_DATABASE_OWNER_URL" => "robin-execution" },
    groups: %w[robin-db-execution-sequencer-2],
    roles: %w[robin_execution_sequencer_2],
    flags: { "SEQUENCER_RUN_MIGRATIONS" => "false" }
  },
  "robin-sequencer-publisher-3" => {
    owners: { "SEQUENCER_DATABASE_OWNER_URL" => "robin-execution" },
    groups: %w[robin-db-execution-sequencer-3],
    roles: %w[robin_execution_sequencer_3],
    flags: { "SEQUENCER_RUN_MIGRATIONS" => "false" }
  },
  "robin-aapl-relay-1" => {
    owners: { "AAPL_RELAY_DATABASE_OWNER_URL" => "robin-execution" },
    groups: %w[robin-db-execution-aapl-relay-1],
    roles: %w[robin_execution_aapl_relay_1],
    flags: { "AAPL_RELAY_RUN_MIGRATIONS" => "false" }
  },
  "robin-aapl-relay-2" => {
    owners: { "AAPL_RELAY_DATABASE_OWNER_URL" => "robin-execution" },
    groups: %w[robin-db-execution-aapl-relay-2],
    roles: %w[robin_execution_aapl_relay_2],
    flags: { "AAPL_RELAY_RUN_MIGRATIONS" => "false" }
  },
  "robin-aapl-relay-3" => {
    owners: { "AAPL_RELAY_DATABASE_OWNER_URL" => "robin-execution" },
    groups: %w[robin-db-execution-aapl-relay-3],
    roles: %w[robin_execution_aapl_relay_3],
    flags: { "AAPL_RELAY_RUN_MIGRATIONS" => "false" }
  },
  "robin-lighter-provisioner" => {
    owners: { "LIGHTER_PROVISIONER_DATABASE_OWNER_URL" => "robin-lighter-credentials" },
    groups: %w[robin-db-lighter-provisioner robin-db-lighter-readonly],
    roles: %w[robin_lighter_provisioner],
    flags: { "LIGHTER_PROVISIONER_RUN_MIGRATIONS" => "false" }
  },
  "robin-robinhood-provisioner" => {
    owners: { "DATABASE_OWNER_URL" => "robin-robinhood-custody" },
    groups: %w[robin-db-custody-provisioner],
    roles: %w[robin_custody_provisioner],
    flags: { "ROBINHOOD_PROVISIONER_RUN_MIGRATIONS" => "false" }
  },
  "robin-robinhood-signer" => {
    owners: { "DATABASE_OWNER_URL" => "robin-robinhood-custody" },
    groups: %w[robin-db-custody-signer],
    roles: %w[robin_custody_signer],
    flags: {}
  }
}

database_contracts.each do |name, contract|
  service = services.find { |item| item["name"] == name }
  env = service&.fetch("envVars", []) || []
  start = service&.fetch("startCommand", "") || ""
  predeploy = service&.fetch("preDeployCommand", "") || ""
  paths = service&.dig("buildFilter", "paths") || []
  unless start.start_with?("ruby scripts/database-runtime-exec.rb ")
    errors << "#{name}: runtime must start through the database credential scrubber"
  end
  contract.fetch(:owners).each do |key, database_name|
    variable = env.find { |item| item["key"] == key }
    unless variable&.dig("fromDatabase", "name") == database_name &&
           variable&.dig("fromDatabase", "property") == "connectionString"
      errors << "#{name}: #{key} must be the direct #{database_name} migration-owner binding"
    end
  end
  actual_owner_keys = env.each_with_object([]) do |item, values|
    values << item["key"] if item["fromDatabase"]
  end
  unless actual_owner_keys.sort == contract.fetch(:owners).keys.sort
    errors << "#{name}: migration-owner bindings do not match the reviewed database contract"
  end
  actual_database_groups = env.each_with_object([]) do |item, values|
    group = item["fromGroup"]
    values << group if group&.start_with?("robin-db-")
  end
  unless actual_database_groups.sort == contract.fetch(:groups).sort
    errors << "#{name}: database role groups do not match the reviewed database contract"
  end
  contract.fetch(:groups).each do |group|
    errors << "#{name}: database role group #{group} is missing" unless env.any? { |item| item["fromGroup"] == group }
  end
  contract.fetch(:roles).each do |role|
    errors << "#{name}: runtime role #{role} is not scrubbed into the command" unless start.include?(",#{role},")
    errors << "#{name}: pre-deploy role #{role} is not provisioned" unless predeploy.include?(role)
  end
  begin
    words = Shellwords.shellsplit(start)
    boundary = words.index("--")
    raise ArgumentError unless boundary

    launcher = words.take(boundary)
    bindings = []
    removals = []
    index = 2
    while index < launcher.length
      case launcher[index]
      when "--binding"
        fields = launcher.fetch(index + 1).split(",", -1)
        raise ArgumentError unless fields.length == 4
        bindings << fields
      when "--remove-env"
        removals << launcher.fetch(index + 1)
      else
        raise ArgumentError
      end
      index += 2
    end
    attached_passwords = actual_database_groups.flat_map do |group|
      generated_group_keys.fetch(group, [])
    end
    binding_owners = bindings.map(&:first)
    binding_roles = bindings.map { |binding| binding[1] }
    binding_passwords = bindings.map { |binding| binding[2] }
    binding_targets = bindings.map { |binding| binding[3] }
    unless (binding_owners - contract.fetch(:owners).keys).empty? &&
           (contract.fetch(:owners).keys - binding_owners).empty?
      errors << "#{name}: runtime owner scrubbing does not match the reviewed database contract"
    end
    unless (binding_roles - contract.fetch(:roles)).empty? &&
           (contract.fetch(:roles) - binding_roles).empty?
      errors << "#{name}: runtime roles do not match the reviewed database contract"
    end
    unless (binding_passwords - attached_passwords).empty? &&
           (attached_passwords - binding_passwords - removals).empty? &&
           (removals - attached_passwords).empty?
      errors << "#{name}: runtime password scrubbing does not match the reviewed database contract"
    end
    if binding_targets.uniq.length != binding_targets.length
      errors << "#{name}: runtime database targets must be unique"
    end
  rescue ArgumentError, IndexError
    errors << "#{name}: database credential scrubber command is invalid"
  end
  contract.fetch(:flags).each do |key, value|
    setting = env.find { |item| item["key"] == key }
    errors << "#{name}: #{key} must be #{value}" unless setting&.fetch("value", nil) == value
  end
  %w[
    scripts/database-runtime-exec.rb
    scripts/prepare-database.sh
    scripts/provision-database-roles.sh
    scripts/psql-with-url.rb
    scripts/run-ordered-migrations.sh
  ].each do |required_path|
    errors << "#{name}: build filter is missing #{required_path}" unless paths.include?(required_path)
  end
end

lighter_provisioner = services.find { |item| item["name"] == "robin-lighter-provisioner" }
unless lighter_provisioner&.fetch("preDeployCommand", "")&.include?("robin_lighter_readonly") &&
       lighter_provisioner&.fetch("startCommand", "")&.include?(
         "--remove-env ROBIN_LIGHTER_READONLY_DATABASE_PASSWORD"
       )
  errors << "robin-lighter-provisioner: receipt role must be provisioned and removed before runtime"
end

services.each do |service|
  service.fetch("envVars", []).each do |variable|
    next unless variable["fromDatabase"]

    key = variable.fetch("key", "")
    errors << "#{service.fetch("name")}: database owner binding #{key} is not explicitly named" unless key.end_with?("_OWNER_URL")
  end
end

robinhood_signer = services.find { |service| service["name"] == "robin-robinhood-signer" }
unless robinhood_signer&.fetch("preDeployCommand", "").to_s.include?(
  'scripts/prepare-database.sh custody "$DATABASE_OWNER_URL" robin_custody_signer'
)
  errors << "robin-robinhood-signer: isolated custody migration and signer role preparation is missing"
end
signer_database = robinhood_signer&.fetch("envVars", [])&.find { |variable| variable["key"] == "DATABASE_OWNER_URL" }
unless signer_database&.dig("fromDatabase", "name") == "robin-robinhood-custody" &&
       signer_database&.dig("fromDatabase", "property") == "connectionString"
  errors << "robin-robinhood-signer: custody migration requires a direct owner binding"
end
signer_paths = robinhood_signer&.dig("buildFilter", "paths") || []
errors << "robin-robinhood-signer: build filter is missing scripts/migrate-robinhood-signer.sh" unless signer_paths.include?("scripts/migrate-robinhood-signer.sh")

database = databases.find { |item| item["name"] == "robin-research" }
if database.nil?
  errors << "robin-research database is missing"
else
  errors << "robin-research: Pro database required" unless database["plan"].to_s.start_with?("pro-")
  errors << "robin-research: external access must be disabled" unless database["ipAllowList"] == []
  errors << "robin-research: PgBouncer required" unless database["connectionPool"] == "pgbouncer"
  errors << "robin-research: HA required" unless database.dig("highAvailability", "enabled") == true
  errors << "robin-research: storage autoscaling required" unless database["storageAutoscalingEnabled"] == true
end

app_database = databases.find { |item| item["name"] == "robin-app" }
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

execution_database = databases.find { |item| item["name"] == "robin-execution" }
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
