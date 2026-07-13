pub mod api;
pub mod config;
pub mod store;

use config::Config;
use store::Store;

pub struct AppState {
    pub config: Config,
    pub store: Option<Store>,
    pub client: reqwest::Client,
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
        Ok(Self {
            config,
            store,
            client,
        })
    }
}
