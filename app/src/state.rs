use crate::config::Config;
use crate::event_bus::EventBus;
use crate::evm::{EvmIndexer, EvmRpc};
use crate::store::Store;
use crate::ws::WsHub;
use std::sync::Arc;

/// Shared application state handed to every request handler and background service.
pub struct AppState {
    pub config: Config,
    pub store: Store,
    pub evm_rpc: EvmRpc,
    pub evm_indexer: EvmIndexer,
    pub event_bus: Arc<EventBus>,
    pub ws_hub: Arc<WsHub>,
}
