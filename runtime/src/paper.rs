use alloy_primitives::{keccak256, Address, Bytes, U256};
use alloy_sol_types::{sol, SolCall};
use anyhow::Context;
use chrono::{DateTime, Utc};
use reqwest::Client;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::{collections::HashSet, fs, path::Path, str::FromStr, time::Duration};
use tokio::sync::Mutex;
use uuid::Uuid;

const PRICE_DECIMALS: u8 = 6;
const SHARES_DECIMALS: u8 = 18;
const PPM: u128 = 1_000_000;
const ZERO_ADDRESS: Address = Address::ZERO;

sol! {
    struct PoolKey {
        address currency0;
        address currency1;
        uint24 fee;
        int24 tickSpacing;
        address hooks;
    }

    struct QuoteExactSingleParams {
        PoolKey poolKey;
        bool zeroForOne;
        uint128 exactAmount;
        bytes hookData;
    }

    function quoteExactInputSingle(QuoteExactSingleParams params)
        external returns (uint256 amountOut, uint256 gasEstimate);
    function decimals() external view returns (uint8 value);
    function uiMultiplier() external view returns (uint256 value);
    function newUIMultiplier() external view returns (uint256 value);
    function effectiveAt() external view returns (uint256 value);
    function oraclePaused() external view returns (bool value);
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct PaperConfig {
    pub schema_version: u32,
    pub strategy_version: String,
    pub chain_id: u64,
    pub quoter: String,
    pub quoter_code_hash: String,
    pub pool_manager: String,
    pub pool_manager_code_hash: String,
    pub settlement_asset: String,
    pub settlement_asset_code_hash: String,
    pub settlement_decimals: u8,
    pub max_ticker_age_ms: i64,
    pub markets: Vec<PaperMarket>,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct PaperMarket {
    pub symbol: String,
    pub stock_token: String,
    pub stock_token_code_hash: String,
    pub stock_decimals: u8,
    pub currency0: String,
    pub currency1: String,
    pub fee: u32,
    pub tick_spacing: i32,
    pub hooks: String,
    pub amount_in_raw: String,
    pub perp_taker_fee_ppm: u64,
    pub gas_cost_settlement_raw: String,
    #[serde(skip)]
    pub minimum_net_edge_ppm: u64,
}

impl PaperConfig {
    pub fn load(path: impl AsRef<Path>) -> anyhow::Result<Self> {
        let path = path.as_ref();
        let config: Self = serde_json::from_slice(
            &fs::read(path).with_context(|| format!("read {}", path.display()))?,
        )
        .with_context(|| format!("parse {}", path.display()))?;
        config.validate()?;
        Ok(config)
    }

    pub fn validate(&self) -> anyhow::Result<()> {
        anyhow::ensure!(self.schema_version == 1, "unsupported paper config schema");
        anyhow::ensure!(
            !self.strategy_version.trim().is_empty(),
            "strategy version is empty"
        );
        anyhow::ensure!(
            self.chain_id == 4663,
            "paper agent requires Robinhood Chain mainnet"
        );
        anyhow::ensure!(
            self.max_ticker_age_ms > 0,
            "ticker age limit must be positive"
        );
        anyhow::ensure!(!self.markets.is_empty(), "paper market set is empty");
        let quoter = address(&self.quoter, "quoter")?;
        let pool_manager = address(&self.pool_manager, "pool manager")?;
        let settlement = address(&self.settlement_asset, "settlement asset")?;
        anyhow::ensure!(
            quoter != ZERO_ADDRESS && pool_manager != ZERO_ADDRESS && settlement != ZERO_ADDRESS,
            "zero core address"
        );
        code_hash(&self.quoter_code_hash, "quoter")?;
        code_hash(&self.pool_manager_code_hash, "pool manager")?;
        code_hash(&self.settlement_asset_code_hash, "settlement asset")?;
        anyhow::ensure!(
            self.settlement_decimals <= 18,
            "unsupported settlement decimals"
        );

        let mut symbols = HashSet::new();
        for market in &self.markets {
            anyhow::ensure!(
                symbols.insert(&market.symbol),
                "duplicate paper market {}",
                market.symbol
            );
            anyhow::ensure!(
                !market.symbol.trim().is_empty(),
                "paper market symbol is empty"
            );
            let stock = address(&market.stock_token, "stock token")?;
            code_hash(&market.stock_token_code_hash, &market.symbol)?;
            let currency0 = address(&market.currency0, "currency0")?;
            let currency1 = address(&market.currency1, "currency1")?;
            let hooks = address(&market.hooks, "hooks")?;
            anyhow::ensure!(
                stock != ZERO_ADDRESS,
                "{} has a zero stock token",
                market.symbol
            );
            anyhow::ensure!(
                hooks == ZERO_ADDRESS,
                "{} must use a zero-hook pool",
                market.symbol
            );
            anyhow::ensure!(
                currency0 < currency1,
                "{} pool currencies are not sorted",
                market.symbol
            );
            anyhow::ensure!(
                (currency0 == settlement && currency1 == stock)
                    || (currency1 == settlement && currency0 == stock),
                "{} pool does not match settlement and stock tokens",
                market.symbol
            );
            anyhow::ensure!(market.fee <= 1_000_000, "{} fee is invalid", market.symbol);
            anyhow::ensure!(
                market.tick_spacing != 0,
                "{} tick spacing is zero",
                market.symbol
            );
            anyhow::ensure!(
                market.stock_decimals <= 18,
                "{} token decimals are unsupported",
                market.symbol
            );
            anyhow::ensure!(
                decimal_integer(&market.amount_in_raw)? > U256::ZERO,
                "{} amount is zero",
                market.symbol
            );
            decimal_integer(&market.gas_cost_settlement_raw)?;
            anyhow::ensure!(
                market.perp_taker_fee_ppm <= 100_000,
                "{} perp fee is invalid",
                market.symbol
            );
            anyhow::ensure!(
                market.minimum_net_edge_ppm <= 1_000_000,
                "{} edge threshold is invalid",
                market.symbol
            );
        }
        Ok(())
    }

    pub fn market(&self, symbol: &str) -> Option<&PaperMarket> {
        self.markets.iter().find(|market| market.symbol == symbol)
    }

    pub fn symbols(&self) -> Vec<String> {
        self.markets
            .iter()
            .map(|market| market.symbol.clone())
            .collect()
    }

    pub fn set_minimum_net_edge_ppm(&mut self, value: u64) -> anyhow::Result<()> {
        anyhow::ensure!(
            value > 0 && value <= 1_000_000,
            "paper edge threshold is invalid"
        );
        for market in &mut self.markets {
            market.minimum_net_edge_ppm = value;
        }
        Ok(())
    }
}

#[derive(Debug, Clone)]
pub struct PaperTickerEvent {
    pub id: Uuid,
    pub symbol: String,
    pub received_at: DateTime<Utc>,
    pub source_timestamp_ms: Option<i64>,
    pub source_session: String,
    pub source_event_id: String,
    pub payload: Value,
    pub superseded_events: u64,
}

#[derive(Debug, Clone)]
pub struct MarketSnapshot {
    pub block_number: u64,
    pub block_hash: String,
    pub block_timestamp: u64,
    pub settlement_decimals: u8,
    pub stock_decimals: u8,
    pub ui_multiplier: U256,
    pub new_ui_multiplier: U256,
    pub effective_at: U256,
    pub oracle_paused: bool,
    pub amount_in_raw: U256,
    pub amount_out_raw: U256,
    pub quoter_gas: U256,
    pub exit_amount_out_raw: Option<U256>,
    pub exit_quoter_gas: Option<U256>,
    pub quoter_code_hash: String,
    pub pool_manager_code_hash: String,
    pub settlement_code_hash: String,
    pub stock_code_hash: String,
}

#[derive(Debug, Clone)]
pub struct ActivePaperPosition {
    pub episode_id: Uuid,
    pub stock_amount_raw: U256,
    pub perp_quantity_wei: U256,
    pub entry_spot_cost_raw: U256,
    pub entry_spot_price_micros: u64,
    pub entry_perp_price_micros: u64,
    pub entry_perp_fee_raw: U256,
    pub gas_cost_per_leg_raw: U256,
}

#[derive(Debug, Clone)]
pub struct PaperEntry {
    pub stock_amount_raw: String,
    pub perp_quantity_wei: String,
    pub entry_spot_cost_raw: String,
    pub entry_spot_price_micros: i64,
    pub entry_perp_price_micros: i64,
    pub entry_perp_fee_raw: String,
    pub gas_cost_per_leg_raw: String,
}

#[derive(Debug, Clone)]
pub struct PaperMark {
    pub spot_exit_raw: String,
    pub perp_ask_micros: i64,
    pub net_pnl_raw: i64,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PaperStatus {
    Candidate,
    Declined,
}

impl PaperStatus {
    pub fn as_db(self) -> &'static str {
        match self {
            Self::Candidate => "candidate",
            Self::Declined => "declined",
        }
    }
}

#[derive(Debug, Clone)]
pub struct PaperEvaluation {
    pub id: Uuid,
    pub status: PaperStatus,
    pub reason: Option<String>,
    pub block_number: Option<i64>,
    pub block_hash: Option<String>,
    pub gross_edge_ppm: Option<i64>,
    pub net_edge_ppm: Option<i64>,
    pub evidence: Value,
    pub evaluated_at: DateTime<Utc>,
    pub entry: Option<PaperEntry>,
    pub mark: Option<PaperMark>,
    pub close_position: bool,
}

impl PaperEvaluation {
    pub fn dependency_decline(reason: impl Into<String>, evaluated_at: DateTime<Utc>) -> Self {
        Self {
            id: Uuid::new_v4(),
            status: PaperStatus::Declined,
            reason: Some(reason.into()),
            block_number: None,
            block_hash: None,
            gross_edge_ppm: None,
            net_edge_ppm: None,
            evidence: json!({ "direction": "long_spot_short_perp" }),
            evaluated_at,
            entry: None,
            mark: None,
            close_position: false,
        }
    }

    pub fn is_candidate(&self) -> bool {
        self.status == PaperStatus::Candidate
    }
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct Evidence {
    direction: &'static str,
    ticker_source_session: String,
    ticker_source_event_id: String,
    ticker_timestamp_ms: Option<i64>,
    ticker_received_at: DateTime<Utc>,
    perp_bid_price: String,
    perp_bid_size: String,
    perp_price_micros: String,
    perp_bid_size_shares_wei: String,
    settlement_amount_in_raw: String,
    stock_amount_out_raw: String,
    underlying_shares_wei: String,
    settlement_decimals: u8,
    stock_decimals: u8,
    ui_multiplier_raw: String,
    new_ui_multiplier_raw: String,
    effective_at: String,
    oracle_paused: bool,
    spot_price_micros: String,
    quoter_gas: String,
    exit_amount_out_raw: String,
    exit_quoter_gas: String,
    block_timestamp: u64,
    quoter_code_hash: String,
    pool_manager_code_hash: String,
    settlement_code_hash: String,
    stock_code_hash: String,
}

pub fn evaluate(
    config: &PaperConfig,
    market: &PaperMarket,
    event: &PaperTickerEvent,
    snapshot: &MarketSnapshot,
    active: Option<&ActivePaperPosition>,
    now: DateTime<Utc>,
) -> PaperEvaluation {
    let mut reason = None;
    let age = event
        .source_timestamp_ms
        .map(|timestamp| now.timestamp_millis().saturating_sub(timestamp).max(0));
    if age.is_none_or(|age| age > config.max_ticker_age_ms) {
        reason = Some("ticker_stale".to_string());
    }

    let bid_price = event
        .payload
        .pointer("/ticker/b/price")
        .and_then(Value::as_str);
    let bid_size = event
        .payload
        .pointer("/ticker/b/size")
        .and_then(Value::as_str);
    let ask_price = event
        .payload
        .pointer("/ticker/a/price")
        .and_then(Value::as_str);
    let ask_size = event
        .payload
        .pointer("/ticker/a/size")
        .and_then(Value::as_str);
    let perp_price_micros =
        bid_price.and_then(|value| decimal_to_scale(value, PRICE_DECIMALS).ok());
    let perp_bid_size_shares =
        bid_size.and_then(|value| decimal_to_scale(value, SHARES_DECIMALS).ok());
    if perp_price_micros.is_none() || perp_bid_size_shares.is_none() {
        reason.get_or_insert_with(|| "ticker_invalid".to_string());
    }
    if snapshot.settlement_decimals != config.settlement_decimals
        || snapshot.stock_decimals != market.stock_decimals
    {
        reason.get_or_insert_with(|| "token_decimals_changed".to_string());
    }
    if snapshot.oracle_paused {
        reason.get_or_insert_with(|| "oracle_paused".to_string());
    }
    if snapshot.ui_multiplier == U256::ZERO || snapshot.new_ui_multiplier != snapshot.ui_multiplier
    {
        reason.get_or_insert_with(|| "multiplier_transition".to_string());
    }

    let stock_scale = pow10(snapshot.stock_decimals).unwrap_or(U256::ZERO);
    let underlying_shares = snapshot
        .amount_out_raw
        .checked_mul(snapshot.ui_multiplier)
        .and_then(|value| value.checked_div(stock_scale))
        .unwrap_or(U256::ZERO);
    if underlying_shares == U256::ZERO {
        reason.get_or_insert_with(|| "zero_spot_output".to_string());
    }
    if perp_bid_size_shares.is_some_and(|size| size < underlying_shares) {
        reason.get_or_insert_with(|| "perp_depth_insufficient".to_string());
    }

    let settlement_micros = scale_integer(
        snapshot.amount_in_raw,
        snapshot.settlement_decimals,
        PRICE_DECIMALS,
    )
    .unwrap_or(U256::ZERO);
    let spot_price_micros = div_ceil(
        settlement_micros
            .checked_mul(pow10(SHARES_DECIMALS).unwrap_or(U256::ZERO))
            .unwrap_or(U256::ZERO),
        underlying_shares,
    )
    .unwrap_or(U256::ZERO);

    let mut gross_edge_ppm = None;
    let mut net_edge_ppm = None;
    if let (Ok(spot), Some(perp)) = (u128::try_from(spot_price_micros), perp_price_micros) {
        if spot > 0 {
            let perp = u128::try_from(perp).unwrap_or(u128::MAX);
            let gross = signed_ratio_ppm(perp, spot);
            let gas_raw = decimal_integer(&market.gas_cost_settlement_raw).unwrap_or(U256::ZERO);
            let gas_micros = scale_integer(gas_raw, config.settlement_decimals, PRICE_DECIMALS)
                .unwrap_or(U256::ZERO);
            let gas_ppm = ratio_ppm_ceil(gas_micros, settlement_micros).unwrap_or(u64::MAX);
            let net = gross
                .saturating_sub(i128::from(market.perp_taker_fee_ppm))
                .saturating_sub(i128::from(gas_ppm));
            gross_edge_ppm = i64::try_from(gross).ok();
            net_edge_ppm = i64::try_from(net).ok();
            if net < i128::from(market.minimum_net_edge_ppm) {
                reason.get_or_insert_with(|| "net_edge_below_threshold".to_string());
            }
        } else {
            reason.get_or_insert_with(|| "spot_price_invalid".to_string());
        }
    } else {
        reason.get_or_insert_with(|| "price_overflow".to_string());
    }

    let spot_price_u64 = u64::try_from(spot_price_micros).ok();
    let perp_price_u64 = perp_price_micros.and_then(|value| u64::try_from(value).ok());
    let gas_cost_raw = decimal_integer(&market.gas_cost_settlement_raw).unwrap_or(U256::ZERO);
    let entry = if reason.is_none() && active.is_none() {
        match (spot_price_u64, perp_price_u64) {
            (Some(spot), Some(perp)) => {
                let perp_fee =
                    fee_raw(snapshot.amount_in_raw, market.perp_taker_fee_ppm).unwrap_or(U256::MAX);
                Some(PaperEntry {
                    stock_amount_raw: snapshot.amount_out_raw.to_string(),
                    perp_quantity_wei: underlying_shares.to_string(),
                    entry_spot_cost_raw: snapshot.amount_in_raw.to_string(),
                    entry_spot_price_micros: i64::try_from(spot).unwrap_or(i64::MAX),
                    entry_perp_price_micros: i64::try_from(perp).unwrap_or(i64::MAX),
                    entry_perp_fee_raw: perp_fee.to_string(),
                    gas_cost_per_leg_raw: gas_cost_raw.to_string(),
                })
            }
            _ => None,
        }
    } else {
        None
    };

    let mark = active.and_then(|position| {
        let exit_raw = snapshot.exit_amount_out_raw?;
        let perp_ask = decimal_to_scale(ask_price?, PRICE_DECIMALS).ok()?;
        let perp_ask_size = decimal_to_scale(ask_size?, SHARES_DECIMALS).ok()?;
        if perp_ask_size < position.perp_quantity_wei {
            return None;
        }
        let perp_ask_u64 = u64::try_from(perp_ask).ok()?;
        let perp_ask_micros = i64::try_from(perp_ask_u64).ok()?;
        paper_pnl(position, exit_raw, perp_ask_u64, market.perp_taker_fee_ppm).map(|pnl| {
            PaperMark {
                spot_exit_raw: exit_raw.to_string(),
                perp_ask_micros,
                net_pnl_raw: pnl,
            }
        })
    });
    if active.is_some() && mark.is_none() {
        reason.get_or_insert_with(|| "exit_quote_or_depth_unavailable".to_string());
    }

    let evidence = Evidence {
        direction: "long_spot_short_perp",
        ticker_source_session: event.source_session.clone(),
        ticker_source_event_id: event.source_event_id.clone(),
        ticker_timestamp_ms: event.source_timestamp_ms,
        ticker_received_at: event.received_at,
        perp_bid_price: bid_price.unwrap_or_default().to_string(),
        perp_bid_size: bid_size.unwrap_or_default().to_string(),
        perp_price_micros: perp_price_micros.unwrap_or(U256::ZERO).to_string(),
        perp_bid_size_shares_wei: perp_bid_size_shares.unwrap_or(U256::ZERO).to_string(),
        settlement_amount_in_raw: snapshot.amount_in_raw.to_string(),
        stock_amount_out_raw: snapshot.amount_out_raw.to_string(),
        underlying_shares_wei: underlying_shares.to_string(),
        settlement_decimals: snapshot.settlement_decimals,
        stock_decimals: snapshot.stock_decimals,
        ui_multiplier_raw: snapshot.ui_multiplier.to_string(),
        new_ui_multiplier_raw: snapshot.new_ui_multiplier.to_string(),
        effective_at: snapshot.effective_at.to_string(),
        oracle_paused: snapshot.oracle_paused,
        spot_price_micros: spot_price_micros.to_string(),
        quoter_gas: snapshot.quoter_gas.to_string(),
        exit_amount_out_raw: snapshot
            .exit_amount_out_raw
            .unwrap_or(U256::ZERO)
            .to_string(),
        exit_quoter_gas: snapshot.exit_quoter_gas.unwrap_or(U256::ZERO).to_string(),
        block_timestamp: snapshot.block_timestamp,
        quoter_code_hash: snapshot.quoter_code_hash.clone(),
        pool_manager_code_hash: snapshot.pool_manager_code_hash.clone(),
        settlement_code_hash: snapshot.settlement_code_hash.clone(),
        stock_code_hash: snapshot.stock_code_hash.clone(),
    };
    let candidate = reason.is_none();
    let close_position = reason.is_some() && active.is_some() && mark.is_some();
    PaperEvaluation {
        id: Uuid::new_v4(),
        status: if candidate {
            PaperStatus::Candidate
        } else {
            PaperStatus::Declined
        },
        reason,
        block_number: i64::try_from(snapshot.block_number).ok(),
        block_hash: Some(snapshot.block_hash.clone()),
        gross_edge_ppm,
        net_edge_ppm,
        evidence: serde_json::to_value(evidence).expect("paper evidence is serializable"),
        evaluated_at: now,
        entry,
        mark,
        close_position,
    }
}

#[derive(Clone)]
pub struct RobinhoodReader {
    client: Client,
    rpc_url: String,
    cache: std::sync::Arc<Mutex<Option<CachedSnapshot>>>,
}

#[derive(Clone)]
struct CachedSnapshot {
    block_number: u64,
    symbol: String,
    active_stock_amount: Option<U256>,
    snapshot: MarketSnapshot,
}

impl RobinhoodReader {
    pub fn new(rpc_url: String) -> anyhow::Result<Self> {
        anyhow::ensure!(!rpc_url.trim().is_empty(), "ROBINHOOD_RPC_URL is empty");
        Ok(Self {
            client: Client::builder()
                .connect_timeout(Duration::from_secs(5))
                .timeout(Duration::from_secs(15))
                .build()
                .context("build Robinhood RPC client")?,
            rpc_url,
            cache: std::sync::Arc::new(Mutex::new(None)),
        })
    }

    pub async fn verify_chain(&self, expected_chain_id: u64) -> anyhow::Result<()> {
        let result = self.rpc("eth_chainId", json!([])).await?;
        let actual = hex_u64(
            result
                .as_str()
                .context("eth_chainId result is not a string")?,
        )?;
        anyhow::ensure!(
            actual == expected_chain_id,
            "Robinhood RPC chain ID {actual} != {expected_chain_id}"
        );
        Ok(())
    }

    pub async fn snapshot(
        &self,
        config: &PaperConfig,
        market: &PaperMarket,
        active: Option<&ActivePaperPosition>,
    ) -> anyhow::Result<MarketSnapshot> {
        let block = self
            .rpc("eth_getBlockByNumber", json!(["latest", false]))
            .await?;
        let block_number = hex_u64(
            block["number"]
                .as_str()
                .context("latest block has no number")?,
        )?;
        let block_hash = block["hash"]
            .as_str()
            .context("latest block has no hash")?
            .to_string();
        let block_timestamp = hex_u64(
            block["timestamp"]
                .as_str()
                .context("latest block has no timestamp")?,
        )?;
        let active_stock_amount = active.map(|position| position.stock_amount_raw);
        if let Some(cached) = self.cache.lock().await.as_ref() {
            if cached.block_number == block_number
                && cached.symbol == market.symbol
                && cached.active_stock_amount == active_stock_amount
            {
                return Ok(cached.snapshot.clone());
            }
        }
        let tag = format!("0x{block_number:x}");
        let settlement = address(&config.settlement_asset, "settlement asset")?;
        let stock = address(&market.stock_token, "stock token")?;
        let quoter = address(&config.quoter, "quoter")?;
        let pool_manager = address(&config.pool_manager, "pool manager")?;

        let quoter_code_hash = self
            .verify_code(quoter, &config.quoter_code_hash, &tag)
            .await?;
        let pool_manager_code_hash = self
            .verify_code(pool_manager, &config.pool_manager_code_hash, &tag)
            .await?;
        let settlement_code_hash = self
            .verify_code(settlement, &config.settlement_asset_code_hash, &tag)
            .await?;
        let stock_code_hash = self
            .verify_code(stock, &market.stock_token_code_hash, &tag)
            .await?;

        let settlement_decimals = self.call_decimals(settlement, &tag).await?;
        let stock_decimals = self.call_decimals(stock, &tag).await?;
        let ui_multiplier = self.call_ui_multiplier(stock, &tag).await?;
        let new_ui_multiplier = self.call_new_ui_multiplier(stock, &tag).await?;
        let effective_at = self.call_effective_at(stock, &tag).await?;
        let oracle_paused = self.call_oracle_paused(stock, &tag).await?;
        let amount_in_raw = decimal_integer(&market.amount_in_raw)?;
        let exact_amount =
            u128::try_from(amount_in_raw).context("paper exact input exceeds uint128")?;
        let call = quoteExactInputSingleCall {
            params: QuoteExactSingleParams {
                poolKey: PoolKey {
                    currency0: address(&market.currency0, "currency0")?,
                    currency1: address(&market.currency1, "currency1")?,
                    fee: market.fee.try_into().context("pool fee exceeds uint24")?,
                    tickSpacing: market
                        .tick_spacing
                        .try_into()
                        .context("tick spacing exceeds int24")?,
                    hooks: address(&market.hooks, "hooks")?,
                },
                zeroForOne: address(&market.currency0, "currency0")? == settlement,
                exactAmount: exact_amount,
                hookData: Bytes::new(),
            },
        };
        let raw = self.eth_call(quoter, call.abi_encode(), &tag).await?;
        let quote = quoteExactInputSingleCall::abi_decode_returns(&raw)
            .context("decode Uniswap v4 exact-input quote")?;
        let (exit_amount_out_raw, exit_quoter_gas) = match active {
            Some(position) => {
                let exact_amount = u128::try_from(position.stock_amount_raw)
                    .context("paper stock position exceeds uint128")?;
                let exit_call = quoteExactInputSingleCall {
                    params: QuoteExactSingleParams {
                        poolKey: PoolKey {
                            currency0: address(&market.currency0, "currency0")?,
                            currency1: address(&market.currency1, "currency1")?,
                            fee: market.fee.try_into().context("pool fee exceeds uint24")?,
                            tickSpacing: market
                                .tick_spacing
                                .try_into()
                                .context("tick spacing exceeds int24")?,
                            hooks: address(&market.hooks, "hooks")?,
                        },
                        zeroForOne: address(&market.currency0, "currency0")? == stock,
                        exactAmount: exact_amount,
                        hookData: Bytes::new(),
                    },
                };
                let raw = self.eth_call(quoter, exit_call.abi_encode(), &tag).await?;
                let exit = quoteExactInputSingleCall::abi_decode_returns(&raw)
                    .context("decode Uniswap v4 exit quote")?;
                (Some(exit.amountOut), Some(exit.gasEstimate))
            }
            None => (None, None),
        };

        let snapshot = MarketSnapshot {
            block_number,
            block_hash,
            block_timestamp,
            settlement_decimals,
            stock_decimals,
            ui_multiplier,
            new_ui_multiplier,
            effective_at,
            oracle_paused,
            amount_in_raw,
            amount_out_raw: quote.amountOut,
            quoter_gas: quote.gasEstimate,
            exit_amount_out_raw,
            exit_quoter_gas,
            quoter_code_hash,
            pool_manager_code_hash,
            settlement_code_hash,
            stock_code_hash,
        };
        *self.cache.lock().await = Some(CachedSnapshot {
            block_number,
            symbol: market.symbol.clone(),
            active_stock_amount,
            snapshot: snapshot.clone(),
        });
        Ok(snapshot)
    }

    async fn verify_code(
        &self,
        target: Address,
        expected: &str,
        block_tag: &str,
    ) -> anyhow::Result<String> {
        let result = self
            .rpc("eth_getCode", json!([target.to_string(), block_tag]))
            .await?;
        let encoded = result
            .as_str()
            .context("eth_getCode result is not a string")?;
        let code = hex::decode(encoded.strip_prefix("0x").unwrap_or(encoded))
            .context("decode contract runtime code")?;
        anyhow::ensure!(!code.is_empty(), "contract runtime code is empty");
        let actual = format!("{:#x}", keccak256(&code));
        anyhow::ensure!(
            actual.eq_ignore_ascii_case(expected),
            "contract runtime code hash changed for {target}: expected {expected}, got {actual}"
        );
        Ok(actual)
    }

    async fn call_decimals(&self, target: Address, tag: &str) -> anyhow::Result<u8> {
        let raw = self
            .eth_call(target, decimalsCall {}.abi_encode(), tag)
            .await?;
        Ok(decimalsCall::abi_decode_returns(&raw)?)
    }

    async fn call_ui_multiplier(&self, target: Address, tag: &str) -> anyhow::Result<U256> {
        let raw = self
            .eth_call(target, uiMultiplierCall {}.abi_encode(), tag)
            .await?;
        Ok(uiMultiplierCall::abi_decode_returns(&raw)?)
    }

    async fn call_new_ui_multiplier(&self, target: Address, tag: &str) -> anyhow::Result<U256> {
        let raw = self
            .eth_call(target, newUIMultiplierCall {}.abi_encode(), tag)
            .await?;
        Ok(newUIMultiplierCall::abi_decode_returns(&raw)?)
    }

    async fn call_effective_at(&self, target: Address, tag: &str) -> anyhow::Result<U256> {
        let raw = self
            .eth_call(target, effectiveAtCall {}.abi_encode(), tag)
            .await?;
        Ok(effectiveAtCall::abi_decode_returns(&raw)?)
    }

    async fn call_oracle_paused(&self, target: Address, tag: &str) -> anyhow::Result<bool> {
        let raw = self
            .eth_call(target, oraclePausedCall {}.abi_encode(), tag)
            .await?;
        Ok(oraclePausedCall::abi_decode_returns(&raw)?)
    }

    async fn eth_call(
        &self,
        target: Address,
        data: Vec<u8>,
        block_tag: &str,
    ) -> anyhow::Result<Vec<u8>> {
        let result = self
            .rpc(
                "eth_call",
                json!([{ "to": target.to_string(), "data": format!("0x{}", hex::encode(data)) }, block_tag]),
            )
            .await?;
        let encoded = result.as_str().context("eth_call result is not a string")?;
        hex::decode(encoded.strip_prefix("0x").unwrap_or(encoded)).context("decode eth_call result")
    }

    async fn rpc(&self, method: &str, params: Value) -> anyhow::Result<Value> {
        let response: Value = self
            .client
            .post(&self.rpc_url)
            .json(&json!({ "jsonrpc": "2.0", "id": 1, "method": method, "params": params }))
            .send()
            .await
            .context("request Robinhood RPC")?
            .error_for_status()
            .context("Robinhood RPC response")?
            .json()
            .await
            .context("decode Robinhood RPC response")?;
        if let Some(error) = response.get("error") {
            anyhow::bail!("Robinhood RPC {method} failed: {error}");
        }
        response
            .get("result")
            .cloned()
            .context("Robinhood RPC response has no result")
    }
}

fn address(value: &str, name: &str) -> anyhow::Result<Address> {
    Address::from_str(value).with_context(|| format!("invalid {name} address"))
}

fn code_hash(value: &str, name: &str) -> anyhow::Result<()> {
    let encoded = value.strip_prefix("0x").unwrap_or(value);
    anyhow::ensure!(encoded.len() == 64, "invalid {name} code hash length");
    hex::decode(encoded).with_context(|| format!("invalid {name} code hash"))?;
    Ok(())
}

fn decimal_integer(value: &str) -> anyhow::Result<U256> {
    anyhow::ensure!(
        !value.is_empty() && value.bytes().all(|byte| byte.is_ascii_digit()),
        "invalid integer amount"
    );
    U256::from_str(value).context("integer amount exceeds uint256")
}

fn decimal_to_scale(value: &str, scale: u8) -> anyhow::Result<U256> {
    anyhow::ensure!(
        !value.is_empty() && !value.starts_with('-'),
        "negative or empty decimal"
    );
    let (whole, fraction) = value.split_once('.').unwrap_or((value, ""));
    anyhow::ensure!(
        !whole.is_empty() && whole.bytes().all(|byte| byte.is_ascii_digit()),
        "invalid decimal whole part"
    );
    anyhow::ensure!(
        fraction.bytes().all(|byte| byte.is_ascii_digit()),
        "invalid decimal fraction"
    );
    anyhow::ensure!(
        fraction.len() <= usize::from(scale),
        "decimal precision exceeds configured scale"
    );
    let whole = decimal_integer(whole)?;
    let factor = pow10(scale).context("decimal scale overflow")?;
    let mut scaled = whole
        .checked_mul(factor)
        .context("decimal amount overflow")?;
    if !fraction.is_empty() {
        let fraction_value = decimal_integer(fraction)?;
        let padding = pow10(scale - fraction.len() as u8).context("decimal padding overflow")?;
        scaled = scaled
            .checked_add(
                fraction_value
                    .checked_mul(padding)
                    .context("decimal fraction overflow")?,
            )
            .context("decimal amount overflow")?;
    }
    Ok(scaled)
}

fn pow10(decimals: u8) -> Option<U256> {
    let mut value = U256::from(1_u8);
    for _ in 0..decimals {
        value = value.checked_mul(U256::from(10_u8))?;
    }
    Some(value)
}

fn scale_integer(value: U256, from: u8, to: u8) -> Option<U256> {
    if from == to {
        Some(value)
    } else if from < to {
        value.checked_mul(pow10(to - from)?)
    } else {
        value.checked_div(pow10(from - to)?)
    }
}

fn div_ceil(numerator: U256, denominator: U256) -> Option<U256> {
    if denominator == U256::ZERO {
        return None;
    }
    let quotient = numerator.checked_div(denominator)?;
    let remainder = numerator.checked_rem(denominator)?;
    if remainder == U256::ZERO {
        Some(quotient)
    } else {
        quotient.checked_add(U256::from(1_u8))
    }
}

fn ratio_ppm_ceil(numerator: U256, denominator: U256) -> Option<u64> {
    let scaled = numerator.checked_mul(U256::from(PPM))?;
    u64::try_from(div_ceil(scaled, denominator)?).ok()
}

fn fee_raw(notional: U256, fee_ppm: u64) -> Option<U256> {
    div_ceil(notional.checked_mul(U256::from(fee_ppm))?, U256::from(PPM))
}

fn paper_pnl(
    position: &ActivePaperPosition,
    spot_exit_raw: U256,
    perp_ask_micros: u64,
    perp_fee_ppm: u64,
) -> Option<i64> {
    let spot_exit = i128::try_from(spot_exit_raw).ok()?;
    let spot_entry = i128::try_from(position.entry_spot_cost_raw).ok()?;
    let price_delta =
        i128::from(position.entry_perp_price_micros).checked_sub(i128::from(perp_ask_micros))?;
    let quantity = i128::try_from(position.perp_quantity_wei).ok()?;
    let perp_pnl = price_delta
        .checked_mul(quantity)?
        .checked_div(i128::try_from(pow10(SHARES_DECIMALS)?).ok()?)?;
    let current_perp_notional = U256::from(perp_ask_micros)
        .checked_mul(position.perp_quantity_wei)?
        .checked_div(pow10(SHARES_DECIMALS)?)?;
    let exit_fee = i128::try_from(fee_raw(current_perp_notional, perp_fee_ppm)?).ok()?;
    let entry_fee = i128::try_from(position.entry_perp_fee_raw).ok()?;
    let gas = i128::try_from(position.gas_cost_per_leg_raw)
        .ok()?
        .checked_mul(2)?;
    let pnl = spot_exit
        .checked_sub(spot_entry)?
        .checked_add(perp_pnl)?
        .checked_sub(entry_fee)?
        .checked_sub(exit_fee)?
        .checked_sub(gas)?;
    i64::try_from(pnl).ok()
}

fn signed_ratio_ppm(value: u128, baseline: u128) -> i128 {
    if value >= baseline {
        let difference = value - baseline;
        i128::try_from(difference.saturating_mul(PPM) / baseline).unwrap_or(i128::MAX)
    } else {
        let difference = baseline - value;
        -i128::try_from(difference.saturating_mul(PPM) / baseline).unwrap_or(i128::MAX)
    }
}

fn hex_u64(value: &str) -> anyhow::Result<u64> {
    u64::from_str_radix(value.strip_prefix("0x").unwrap_or(value), 16).context("parse hex quantity")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn decimal_parser_is_exact() {
        assert_eq!(
            decimal_to_scale("316.25", 6).unwrap(),
            U256::from(316_250_000_u64)
        );
        assert_eq!(decimal_to_scale("0.000001", 6).unwrap(), U256::from(1_u8));
        assert!(decimal_to_scale("1.0000001", 6).is_err());
        assert!(decimal_to_scale("NaN", 6).is_err());
    }

    #[test]
    fn reviewed_config_is_valid() {
        PaperConfig::load("config/mainnet-paper.json").unwrap();
    }
}
