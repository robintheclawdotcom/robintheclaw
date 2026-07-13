use alloy_primitives::U256;
use chrono::{TimeZone, Utc};
use robin_runtime::paper::{
    evaluate, ActivePaperPosition, MarketSnapshot, PaperConfig, PaperStatus, PaperTickerEvent,
};
use uuid::Uuid;

fn config() -> PaperConfig {
    let mut config = PaperConfig::load("config/mainnet-paper.json").unwrap();
    config.set_minimum_net_edge_ppm(1_200).unwrap();
    config
}

fn event(bid: &str, ask: &str) -> PaperTickerEvent {
    let now = Utc.with_ymd_and_hms(2026, 7, 13, 0, 0, 0).unwrap();
    let mut payload: serde_json::Value =
        serde_json::from_str(include_str!("../fixtures/paper/aapl_ticker.json")).unwrap();
    payload["ticker"]["b"]["price"] = bid.into();
    payload["ticker"]["a"]["price"] = ask.into();
    PaperTickerEvent {
        id: Uuid::new_v4(),
        symbol: "AAPL".to_string(),
        received_at: now,
        source_timestamp_ms: Some(now.timestamp_millis()),
        source_session: "fixture".to_string(),
        source_event_id: "ticker:42".to_string(),
        payload,
        superseded_events: 0,
    }
}

fn snapshot() -> MarketSnapshot {
    MarketSnapshot {
        block_number: 8_900_000,
        block_hash: format!("0x{}", "ab".repeat(32)),
        block_timestamp: 1_783_900_800,
        settlement_decimals: 6,
        stock_decimals: 18,
        ui_multiplier: U256::from(10_u8).pow(U256::from(18_u8)),
        new_ui_multiplier: U256::from(10_u8).pow(U256::from(18_u8)),
        effective_at: U256::ZERO,
        oracle_paused: false,
        amount_in_raw: U256::from(25_000_000_u64),
        amount_out_raw: U256::from(100_000_000_000_000_000_u64),
        quoter_gas: U256::from(145_000_u64),
        exit_amount_out_raw: None,
        exit_quoter_gas: None,
        quoter_code_hash: format!("0x{}", "01".repeat(32)),
        pool_manager_code_hash: format!("0x{}", "02".repeat(32)),
        settlement_code_hash: format!("0x{}", "03".repeat(32)),
        stock_code_hash: format!("0x{}", "04".repeat(32)),
    }
}

fn active() -> ActivePaperPosition {
    ActivePaperPosition {
        episode_id: Uuid::new_v4(),
        stock_amount_raw: U256::from(100_000_000_000_000_000_u64),
        perp_quantity_wei: U256::from(100_000_000_000_000_000_u64),
        entry_spot_cost_raw: U256::from(25_000_000_u64),
        entry_spot_price_micros: 250_000_000,
        entry_perp_price_micros: 260_000_000,
        entry_perp_fee_raw: U256::from(12_500_u64),
        gas_cost_per_leg_raw: U256::from(50_000_u64),
    }
}

#[test]
fn candidate_opens_a_matched_long_spot_short_perp_position() {
    let config = config();
    let market = config.market("AAPL").unwrap();
    let event = event("260.000000", "261.000000");
    let evaluation = evaluate(
        &config,
        market,
        &event,
        &snapshot(),
        None,
        event.received_at,
    );

    assert_eq!(evaluation.status, PaperStatus::Candidate);
    let entry = evaluation.entry.unwrap();
    assert_eq!(entry.entry_spot_cost_raw, "25000000");
    assert_eq!(entry.stock_amount_raw, "100000000000000000");
    assert_eq!(entry.perp_quantity_wei, "100000000000000000");
    assert_eq!(entry.entry_spot_price_micros, 250_000_000);
    assert_eq!(entry.entry_perp_price_micros, 260_000_000);
    assert_eq!(
        evaluation.evidence["quoterCodeHash"],
        format!("0x{}", "01".repeat(32))
    );
    assert!(!evaluation.close_position);
}

#[test]
fn active_position_is_marked_with_net_pnl() {
    let config = config();
    let market = config.market("AAPL").unwrap();
    let event = event("260.000000", "255.000000");
    let mut quote = snapshot();
    quote.exit_amount_out_raw = Some(U256::from(25_500_000_u64));
    quote.exit_quoter_gas = Some(U256::from(146_000_u64));
    let evaluation = evaluate(
        &config,
        market,
        &event,
        &quote,
        Some(&active()),
        event.received_at,
    );

    assert_eq!(evaluation.status, PaperStatus::Candidate);
    let mark = evaluation.mark.unwrap();
    assert_eq!(mark.spot_exit_raw, "25500000");
    assert_eq!(mark.perp_ask_micros, 255_000_000);
    assert_eq!(mark.net_pnl_raw, 874_750);
    assert!(!evaluation.close_position);
}

#[test]
fn failed_edge_closes_an_active_episode_at_the_current_mark() {
    let config = config();
    let market = config.market("AAPL").unwrap();
    let event = event("249.000000", "251.000000");
    let mut quote = snapshot();
    quote.exit_amount_out_raw = Some(U256::from(24_900_000_u64));
    quote.exit_quoter_gas = Some(U256::from(146_000_u64));
    let evaluation = evaluate(
        &config,
        market,
        &event,
        &quote,
        Some(&active()),
        event.received_at,
    );

    assert_eq!(evaluation.status, PaperStatus::Declined);
    assert_eq!(
        evaluation.reason.as_deref(),
        Some("net_edge_below_threshold")
    );
    assert!(evaluation.mark.is_some());
    assert!(evaluation.close_position);
}

#[test]
fn multiplier_transition_blocks_entry() {
    let config = config();
    let market = config.market("AAPL").unwrap();
    let event = event("260.000000", "261.000000");
    let mut quote = snapshot();
    quote.new_ui_multiplier = quote.ui_multiplier * U256::from(2_u8);
    let evaluation = evaluate(&config, market, &event, &quote, None, event.received_at);

    assert_eq!(evaluation.status, PaperStatus::Declined);
    assert_eq!(evaluation.reason.as_deref(), Some("multiplier_transition"));
    assert!(evaluation.entry.is_none());
}
