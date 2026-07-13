pub mod api;
pub mod config;
pub mod signer_client;
pub mod store;
pub mod worker;

use config::Config;
use signer_client::SignerClients;
use store::Store;

pub struct AppState {
    pub config: Config,
    pub store: Option<Store>,
    pub client: reqwest::Client,
    pub signers: Option<SignerClients>,
}

impl AppState {
    pub async fn initialize(config: Config) -> Result<Self, sqlx::Error> {
        let store = match config.database_url.as_deref() {
            Some(database_url) if config.enabled => Some(Store::connect(database_url).await?),
            _ => None,
        };
        let client = reqwest::Client::builder()
            .connect_timeout(std::time::Duration::from_secs(2))
            .timeout(std::time::Duration::from_secs(3))
            .build()
            .expect("static HTTP client configuration is valid");
        let signers = if config.enabled {
            Some(SignerClients::new(
                client.clone(),
                config
                    .signer_caller_id
                    .clone()
                    .expect("validated caller ID"),
                config
                    .lighter_signer_url
                    .clone()
                    .expect("validated Lighter signer URL"),
                config
                    .robinhood_signer_url
                    .clone()
                    .expect("validated Robinhood signer URL"),
                config
                    .lighter_api_url
                    .clone()
                    .expect("validated Lighter API URL"),
                config
                    .lighter_signer_hmac_key
                    .expect("validated Lighter HMAC key"),
                config
                    .robinhood_signer_hmac_key
                    .expect("validated Robinhood HMAC key"),
            ))
        } else {
            None
        };
        Ok(Self {
            config,
            store,
            client,
            signers,
        })
    }
}
