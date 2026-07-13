//! Golden tests for the read-only Lighter public connector. Every case runs against a stored
//! example payload; none touch the network.

use robin_runtime::lighter::{
    parse_frame, parse_market_metadata, subscription_budget, validate_ack, Frame, LighterError,
    OrderBook, OrderBookFrame,
};
use std::collections::HashSet;

const SNAPSHOT: &str = include_str!("../fixtures/lighter/order_book_snapshot.json");
const DELTA: &str = include_str!("../fixtures/lighter/order_book_delta.json");
const GAP: &str = include_str!("../fixtures/lighter/order_book_gap.json");
const MALFORMED: &str = include_str!("../fixtures/lighter/order_book_malformed.json");
const TICKER: &str = include_str!("../fixtures/lighter/ticker.json");
const TRADE: &str = include_str!("../fixtures/lighter/trade.json");
const MARKET_STATS: &str = include_str!("../fixtures/lighter/market_stats.json");
const HEIGHT: &str = include_str!("../fixtures/lighter/height.json");
const ACK: &str = include_str!("../fixtures/lighter/subscribed_ack.json");
const ACK_MALFORMED: &str = include_str!("../fixtures/lighter/subscribed_ack_malformed.json");
const METADATA: &[u8] = include_bytes!("../fixtures/lighter/market_metadata.json");

fn order_book_frame(text: &str) -> OrderBookFrame {
    match parse_frame(text).expect("frame parses") {
        Frame::OrderBook(frame) => *frame,
        other => panic!("expected order book frame, got {other:?}"),
    }
}

#[test]
fn parses_order_book_snapshot() {
    let frame = order_book_frame(SNAPSHOT);
    assert_eq!(frame.order_book.nonce, 9182390020);
    assert_eq!(frame.order_book.asks.len(), 2);
    assert_eq!(frame.order_book.bids.len(), 1);
}

#[test]
fn snapshot_initializes_then_a_valid_delta_advances_the_book() {
    let snapshot = order_book_frame(SNAPSHOT);
    let delta = order_book_frame(DELTA);
    let mut book = OrderBook::default();
    book.apply(&snapshot.channel, &snapshot.order_book)
        .expect("snapshot applies");
    assert_eq!(book.nonce(), Some(9182390020));
    let before = book.level_count();
    book.apply(&delta.channel, &delta.order_book)
        .expect("delta applies");
    assert_eq!(book.nonce(), Some(9182390040));
    // the delta removed one ask (size "0") and added another, so the level count is unchanged.
    assert_eq!(book.level_count(), before);
    assert!(book.is_initialized());
}

#[test]
fn a_nonce_gap_invalidates_the_book_and_returns_a_typed_error() {
    let snapshot = order_book_frame(SNAPSHOT);
    let gap = order_book_frame(GAP);
    let mut book = OrderBook::default();
    book.apply(&snapshot.channel, &snapshot.order_book).unwrap();
    let err = book.apply(&gap.channel, &gap.order_book).unwrap_err();
    assert!(matches!(
        err,
        LighterError::ContinuityGap {
            expected: 9182390020,
            received: 9182390999,
            ..
        }
    ));
    assert!(!book.is_initialized(), "book must invalidate on a gap");
}

#[test]
fn top_level_millisecond_and_microsecond_timestamps_stay_independent() {
    let frame = order_book_frame(SNAPSHOT);
    assert_eq!(frame.timestamp_ms, 1774884082326);
    assert_eq!(frame.last_updated_us, 1774884082309144);
    assert_ne!(frame.timestamp_ms as u64, frame.last_updated_us);
    assert_eq!(frame.order_book.last_updated_us, 1774884082309144);
}

#[test]
fn parses_ticker() {
    match parse_frame(TICKER).expect("ticker parses") {
        Frame::Ticker(frame) => {
            assert_eq!(frame.ticker.symbol, "ETH");
            assert_eq!(frame.ticker.bid.price, "2064.30");
            assert_eq!(frame.ticker.ask.price, "2064.48");
        }
        other => panic!("expected ticker, got {other:?}"),
    }
}

#[test]
fn parses_trade_including_omitempty_fee() {
    match parse_frame(TRADE).expect("trade parses") {
        Frame::Trade(frame) => {
            assert_eq!(frame.trades.len(), 1);
            assert_eq!(frame.trades[0].price, "2064.50");
            assert_eq!(frame.trades[0].taker_fee, Some(196));
        }
        other => panic!("expected trade, got {other:?}"),
    }
}

#[test]
fn parses_market_stats_funding_and_book_fields() {
    match parse_frame(MARKET_STATS).expect("market stats parse") {
        Frame::MarketStats(frame) => {
            let stats = frame.market_stats;
            assert_eq!(stats.current_funding_rate, "0.0012");
            assert_eq!(stats.funding_rate, "0.0006");
            assert_eq!(stats.funding_timestamp, 1781089200001);
            assert_eq!(stats.open_interest, "56951890.578450");
            assert_eq!(stats.mark_price, "1620.50");
            assert_eq!(stats.index_price, "1621.50");
            assert_eq!(stats.best_bid_price, "1620.28");
            assert_eq!(stats.best_ask_price, "1620.45");
        }
        other => panic!("expected market stats, got {other:?}"),
    }
}

#[test]
fn parses_height() {
    match parse_frame(HEIGHT).expect("height parses") {
        Frame::Height(frame) => assert_eq!(frame.height, 12345678),
        other => panic!("expected height, got {other:?}"),
    }
}

#[test]
fn rejects_a_frame_missing_a_required_field() {
    assert!(matches!(
        parse_frame(MALFORMED),
        Err(LighterError::Decode(_))
    ));
}

#[test]
fn accepts_a_well_formed_acknowledgement() {
    assert!(matches!(parse_frame(ACK).unwrap(), Frame::Ack(_)));
    assert_eq!(validate_ack(ACK).unwrap().channel, "order_book:0");
}

#[test]
fn rejects_an_acknowledgement_without_a_channel() {
    assert!(matches!(
        validate_ack(ACK_MALFORMED),
        Err(LighterError::Acknowledgement(_))
    ));
    assert!(matches!(
        parse_frame(ACK_MALFORMED),
        Err(LighterError::Acknowledgement(_))
    ));
}

#[test]
fn enforces_the_subscription_budget() {
    assert_eq!(subscription_budget(24).unwrap(), 97);
    assert!(matches!(
        subscription_budget(25),
        Err(LighterError::SubscriptionBudget {
            requested: 101,
            limit: 100
        })
    ));
}

#[test]
fn parses_market_metadata_without_lossy_defaults() {
    let wanted: HashSet<&str> = ["NVDA"].into_iter().collect();
    let markets = parse_market_metadata(METADATA, &wanted).expect("metadata parses");
    assert_eq!(markets.len(), 1, "spot market must be filtered out");
    let nvda = &markets[0];
    assert_eq!(nvda.symbol, "NVDA");
    assert!(nvda.is_active_perp());
    assert_eq!(nvda.supported_price_decimals, 2);
    assert_eq!(nvda.supported_size_decimals, 4);
    assert_eq!(nvda.min_base_amount, "0.0350");
    assert_eq!(nvda.taker_fee, "0.0000");
    assert_eq!(nvda.maintenance_margin_fraction, 1200);
    assert_eq!(nvda.closeout_margin_fraction, 800);
}
