//! Live event feed. The hub is a broadcast channel of JSON strings; each WebSocket client
//! subscribes and receives every platform event the orchestrator bridges in. Transport is
//! swappable behind the hub; only the socket session below is WebSocket-specific.

use crate::api::error::ApiError;
use crate::auth::require_user;
use crate::state::AppState;
use actix::{Actor, ActorContext, AsyncContext, StreamHandler};
use actix_web::{web, HttpRequest, HttpResponse};
use actix_web_actors::ws;
use tokio::sync::broadcast;
use tokio_stream::wrappers::{errors::BroadcastStreamRecvError, BroadcastStream};

#[derive(Clone)]
pub struct WsHub {
    tx: broadcast::Sender<String>,
}

impl WsHub {
    pub fn new() -> Self {
        let (tx, _) = broadcast::channel(1024);
        Self { tx }
    }

    pub fn broadcast(&self, msg: String) {
        let _ = self.tx.send(msg);
    }

    pub fn subscribe(&self) -> broadcast::Receiver<String> {
        self.tx.subscribe()
    }
}

impl Default for WsHub {
    fn default() -> Self {
        Self::new()
    }
}

struct WsSession {
    rx: Option<broadcast::Receiver<String>>,
}

impl Actor for WsSession {
    type Context = ws::WebsocketContext<Self>;

    fn started(&mut self, ctx: &mut Self::Context) {
        if let Some(rx) = self.rx.take() {
            ctx.add_stream(BroadcastStream::new(rx));
        }
    }
}

// Feed pushed from the hub to this client.
impl StreamHandler<Result<String, BroadcastStreamRecvError>> for WsSession {
    fn handle(&mut self, item: Result<String, BroadcastStreamRecvError>, ctx: &mut Self::Context) {
        if let Ok(msg) = item {
            ctx.text(msg);
        }
    }
}

// Client control frames.
impl StreamHandler<Result<ws::Message, ws::ProtocolError>> for WsSession {
    fn handle(&mut self, item: Result<ws::Message, ws::ProtocolError>, ctx: &mut Self::Context) {
        match item {
            Ok(ws::Message::Ping(m)) => ctx.pong(&m),
            Ok(ws::Message::Close(reason)) => {
                ctx.close(reason);
                ctx.stop();
            }
            _ => {}
        }
    }
}

pub async fn ws_index(
    req: HttpRequest,
    stream: web::Payload,
    state: web::Data<AppState>,
) -> Result<HttpResponse, actix_web::Error> {
    let rx = state.ws_hub.subscribe();
    ws::start(WsSession { rx: Some(rx) }, &req, stream)
}

pub async fn product_ws_index(
    req: HttpRequest,
    stream: web::Payload,
    state: web::Data<AppState>,
) -> Result<HttpResponse, ApiError> {
    require_user(&req, &state)?;
    let rx = state.ws_hub.subscribe();
    ws::start(WsSession { rx: Some(rx) }, &req, stream).map_err(ApiError::internal)
}
