use axum::{body::Body, http::Request};
use coordinator::{api, config::Config, AppState};
use std::sync::Arc;
use tower::ServiceExt;

fn disabled_config() -> Config {
    Config {
        enabled: false,
        listen: "127.0.0.1:0".parse().unwrap(),
        database_url: None,
        api_token: None,
        lighter_signer_url: None,
        robinhood_signer_url: None,
        signer_caller_id: None,
        lighter_signer_hmac_key: None,
        robinhood_signer_hmac_key: None,
        lighter_api_url: None,
    }
}

#[tokio::test]
async fn disabled_coordinator_is_live_but_not_ready() {
    let app = api::routes(Arc::new(
        AppState::initialize(disabled_config()).await.unwrap(),
    ));
    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/livez")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert!(response.status().is_success());

    let response = app
        .oneshot(
            Request::builder()
                .uri("/readyz")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), 503);
}

#[tokio::test]
async fn disabled_coordinator_rejects_intents() {
    let app = api::routes(Arc::new(
        AppState::initialize(disabled_config()).await.unwrap(),
    ));
    let response = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/intents")
                .header("content-type", "application/json")
                .body(Body::from("{}"))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_ne!(response.status(), 201);
}
