use crate::account_registration::CoordinatorRegistrationClient;
use crate::auth::AuthService;
use crate::config::Config;
use crate::event_bus::EventBus;
use crate::evm::{EvmIndexer, EvmRpc};
use crate::lighter_provisioner::LighterProvisioner;
use crate::privy::PrivyClient;
use crate::product_store::ProductStore;
use crate::robinhood_provisioner::RobinhoodProvisioner;
use crate::service_auth::ServiceAuth;
use crate::store::Store;
use crate::ws::WsHub;
use std::sync::Arc;

/// Shared application state handed to every request handler and background service.
pub struct AppState {
    pub config: Config,
    pub store: Store,
    pub evm_rpc: EvmRpc,
    pub product_rpc: EvmRpc,
    pub wallet_rpc: EvmRpc,
    pub evm_indexer: EvmIndexer,
    pub event_bus: Arc<EventBus>,
    pub ws_hub: Arc<WsHub>,
    pub product_store: ProductStore,
    pub auth: AuthService,
    pub privy: PrivyClient,
    pub lighter_provisioner: LighterProvisioner,
    pub robinhood_provisioner: RobinhoodProvisioner,
    pub readiness_auth: ServiceAuth,
    pub coordinator_registration: CoordinatorRegistrationClient,
}
