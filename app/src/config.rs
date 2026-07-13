use std::env;

fn env_or(key: &str, default: &str) -> String {
    env::var(key).unwrap_or_else(|_| default.to_string())
}

fn env_bool(key: &str, default: bool) -> bool {
    env::var(key)
        .ok()
        .map(|v| {
            let v = v.trim();
            v.eq_ignore_ascii_case("true") || v == "1"
        })
        .unwrap_or(default)
}

fn env_u64(key: &str, default: u64) -> u64 {
    env::var(key)
        .ok()
        .and_then(|v| v.trim().parse().ok())
        .unwrap_or(default)
}

fn env_list(key: &str) -> Vec<String> {
    env::var(key)
        .ok()
        .map(|s| {
            s.split(',')
                .map(|x| x.trim().to_string())
                .filter(|x| !x.is_empty())
                .collect()
        })
        .unwrap_or_default()
}

/// Runtime configuration, entirely environment-driven so the same binary runs against testnet or
/// mainnet without a rebuild. Contract addresses default empty; the indexer stays idle until they
/// are supplied, which keeps a fresh deployment from watching the wrong chain.
#[derive(Clone, Debug)]
pub struct Config {
    pub host: String,
    pub port: u16,
    pub is_development: bool,
    pub cors_origins: Vec<String>,

    pub rpc_url: String,
    pub rpc_fallback_urls: Vec<String>,
    pub chain_id: u64,

    pub vault_address: String,
    pub anchor_address: String,
    pub guard_address: String,
    pub lighter_api: String,

    pub evm_enabled: bool,
    pub indexer_lookback_blocks: u64,
    pub indexer_confirmations: u64,
    pub indexer_max_logs: usize,

    pub geo_blocking_enabled: bool,

    pub database_url: Option<String>,
    pub privy_app_id: Option<String>,
    pub privy_app_secret: Option<String>,
    pub privy_verification_key: Option<String>,
    pub app_rpc_url: String,
    pub app_chain_id: u64,
    pub product_indexer_lookback_blocks: u64,
    pub product_indexer_block_range: u64,
    pub personal_vault_factory: String,
    pub test_asset_address: String,
    pub test_faucet_address: String,
    pub test_asset_symbol: String,
    pub test_asset_decimals: u8,
    pub test_claim_amount: String,
    pub alchemy_wallet_rpc_url: String,
    pub agent_strategy_version: String,
    pub lighter_provisioner_url: String,
    pub lighter_provisioner_caller_id: String,
    pub lighter_provisioner_hmac_key: String,
    pub lighter_api_key_index: u64,
    pub robinhood_provisioner_url: String,
    pub robinhood_provisioner_caller_id: String,
    pub robinhood_provisioner_hmac_key: String,
    pub readiness_caller_id: String,
    pub readiness_hmac_key: String,
    pub coordinator_command_url: String,
    pub coordinator_command_caller_id: String,
    pub coordinator_command_hmac_key: String,
    pub command_worker_id: String,
    pub coordinator_registration_url: String,
    pub coordinator_registration_caller_id: String,
    pub coordinator_registration_hmac_key: String,
    pub registration_worker_id: String,
}

impl Config {
    pub fn from_env() -> Self {
        let cors_origins = {
            let list = env_list("CORS_ORIGINS");
            if list.is_empty() {
                vec!["*".to_string()]
            } else {
                list
            }
        };

        Self {
            host: env_or("HOST", "127.0.0.1"),
            port: env_u64("PORT", 8080) as u16,
            is_development: env_bool("DEVELOPMENT", true),
            cors_origins,
            rpc_url: env_or("RH_MAINNET_RPC", "https://rpc.mainnet.chain.robinhood.com"),
            rpc_fallback_urls: env_list("RH_RPC_FALLBACK"),
            chain_id: env_u64("RH_CHAIN_ID", 4663),
            vault_address: env_or("VAULT_ADDRESS", ""),
            anchor_address: env_or("ANCHOR_ADDRESS", ""),
            guard_address: env_or("GUARD_ADDRESS", ""),
            lighter_api: env_or("LIGHTER_API", "https://mainnet.zklighter.elliot.ai"),
            evm_enabled: env_bool("EVM_ENABLED", true),
            indexer_lookback_blocks: env_u64("INDEXER_LOOKBACK_BLOCKS", 50_000),
            indexer_confirmations: env_u64("INDEXER_CONFIRMATIONS", 5),
            indexer_max_logs: env_u64("INDEXER_MAX_LOGS", 20_000) as usize,
            geo_blocking_enabled: env_bool("GEO_BLOCKING_ENABLED", true),
            database_url: env::var("DATABASE_URL")
                .ok()
                .filter(|v| !v.trim().is_empty()),
            privy_app_id: env::var("PRIVY_APP_ID")
                .ok()
                .filter(|v| !v.trim().is_empty()),
            privy_app_secret: env::var("PRIVY_APP_SECRET")
                .ok()
                .filter(|v| !v.trim().is_empty()),
            privy_verification_key: env::var("PRIVY_VERIFICATION_KEY")
                .ok()
                .filter(|v| !v.trim().is_empty()),
            app_rpc_url: env_or("APP_RPC_URL", ""),
            app_chain_id: env_u64("APP_CHAIN_ID", 46630),
            product_indexer_lookback_blocks: env_u64("PRODUCT_INDEXER_LOOKBACK_BLOCKS", 50_000),
            product_indexer_block_range: env_u64("PRODUCT_INDEXER_BLOCK_RANGE", 10).max(1),
            personal_vault_factory: env_or("PERSONAL_VAULT_FACTORY", ""),
            test_asset_address: env_or("TEST_ASSET_ADDRESS", ""),
            test_faucet_address: env_or("TEST_FAUCET_ADDRESS", ""),
            test_asset_symbol: env_or("TEST_ASSET_SYMBOL", "tUSDG"),
            test_asset_decimals: env_u64("TEST_ASSET_DECIMALS", 6) as u8,
            test_claim_amount: env_or("TEST_CLAIM_AMOUNT", "1000000000"),
            alchemy_wallet_rpc_url: env::var("ALCHEMY_WALLET_RPC_URL")
                .ok()
                .filter(|v| !v.trim().is_empty())
                .or_else(|| {
                    env::var("ALCHEMY_API_KEY")
                        .ok()
                        .filter(|v| !v.trim().is_empty())
                        .map(|key| format!("https://api.g.alchemy.com/v2/{key}"))
                })
                .unwrap_or_default(),
            agent_strategy_version: env_or("AGENT_STRATEGY_VERSION", "basis-paper-v1"),
            lighter_provisioner_url: env_or("LIGHTER_PROVISIONER_URL", ""),
            lighter_provisioner_caller_id: env_or("LIGHTER_PROVISIONER_CALLER_ID", "robin-api"),
            lighter_provisioner_hmac_key: env_or("LIGHTER_PROVISIONER_HMAC_KEY", ""),
            lighter_api_key_index: env_u64("LIGHTER_API_KEY_INDEX", 254),
            robinhood_provisioner_url: env_or("ROBINHOOD_PROVISIONER_URL", ""),
            robinhood_provisioner_caller_id: env_or("ROBINHOOD_PROVISIONER_CALLER_ID", "robin-api"),
            robinhood_provisioner_hmac_key: env_or("ROBINHOOD_PROVISIONER_HMAC_KEY", ""),
            readiness_caller_id: env_or("READINESS_CALLER_ID", ""),
            readiness_hmac_key: env_or("READINESS_HMAC_KEY", ""),
            coordinator_command_url: env_or("COORDINATOR_COMMAND_URL", ""),
            coordinator_command_caller_id: env_or(
                "COORDINATOR_COMMAND_CALLER_ID",
                "product-command-worker",
            ),
            coordinator_command_hmac_key: env_or("COORDINATOR_CONTROL_HMAC_KEY", ""),
            command_worker_id: env_or("COMMAND_WORKER_ID", "product-command-worker-1"),
            coordinator_registration_url: env_or("COORDINATOR_REGISTRATION_URL", ""),
            coordinator_registration_caller_id: env_or(
                "COORDINATOR_REGISTRATION_CALLER_ID",
                "product-account-provisioner",
            ),
            coordinator_registration_hmac_key: env_or("COORDINATOR_REGISTRATION_HMAC_KEY", ""),
            registration_worker_id: env_or(
                "REGISTRATION_WORKER_ID",
                "product-account-provisioner-1",
            ),
        }
    }

    pub fn product_contracts_ready(&self) -> bool {
        !self.app_rpc_url.is_empty()
            && !self.alchemy_wallet_rpc_url.is_empty()
            && !self.personal_vault_factory.is_empty()
            && !self.test_asset_address.is_empty()
            && !self.test_faucet_address.is_empty()
    }

    /// Contract addresses the indexer follows, empties dropped.
    pub fn watched_addresses(&self) -> Vec<String> {
        [
            &self.vault_address,
            &self.anchor_address,
            &self.guard_address,
        ]
        .into_iter()
        .filter(|a| !a.is_empty())
        .cloned()
        .collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn cfg() -> Config {
        let mut c = Config::from_env();
        c.vault_address = "0xaaaa".into();
        c.anchor_address = String::new();
        c.guard_address = "0xbbbb".into();
        c
    }

    #[test]
    fn watched_addresses_drops_empties() {
        assert_eq!(
            cfg().watched_addresses(),
            vec!["0xaaaa".to_string(), "0xbbbb".to_string()]
        );
    }

    #[test]
    fn default_chain_is_robinhood() {
        // No env override in the test environment.
        let c = Config::from_env();
        assert!(c.chain_id >= 1);
        assert!(!c.rpc_url.is_empty());
    }
}
