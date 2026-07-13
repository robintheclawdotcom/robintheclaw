//! Backend for the delta-neutral basis agent. Provides the HTTP API surface, a reorg-safe EVM
//! indexer, a JSON-RPC client with failover, an in-memory store, an event bus, and a live feed.
//! The binary in `main.rs` wires these together; the modules are public so later phases (the
//! basis-scanner service, a Postgres store, the signer and execution path) build on the same API.

pub mod api;
pub mod auth;
pub mod command_dispatcher;
pub mod config;
pub mod event_bus;
pub mod evm;
pub mod lighter_provisioner;
pub mod orchestrator;
pub mod privy;
pub mod product;
pub mod product_indexer;
pub mod product_store;
pub mod service_auth;
pub mod state;
pub mod store;
pub mod ws;

pub use config::Config;
pub use state::AppState;
