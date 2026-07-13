use serde::{Deserialize, Serialize};
use thiserror::Error;

pub const USD_SCALE: u64 = 1_000_000;
pub const CANARY_LEG_CAP_MICROS: u64 = 25 * USD_SCALE;
pub const CANARY_GROSS_CAP_MICROS: u64 = 50 * USD_SCALE;
const LEG_NAV_DENOMINATOR: u64 = 400;
const GROSS_NAV_DENOMINATOR: u64 = 200;
const MIN_ORDER_EXPIRY_MS: u64 = 5 * 60 * 1_000;
const MAX_ORDER_EXPIRY_MS: u64 = 30 * 24 * 60 * 60 * 1_000;
const MAX_CLIENT_ORDER_INDEX: u64 = (1 << 48) - 1;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum SpotSide {
    Buy,
    Sell,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum PerpSide {
    Long,
    Short,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FrozenEvidence {
    pub dataset_manifest: String,
    pub strategy_version: String,
    pub market_manifest: String,
    pub quote_block_hash: String,
    pub quote_received_at_ms: u64,
    pub quote_expires_at_ms: u64,
    #[serde(with = "u128_string")]
    pub ui_multiplier_e18: u128,
    pub perp_mark_price: u32,
    pub estimated_total_cost_micros: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PairIntent {
    pub id: String,
    pub spot_unwind_intent_id: String,
    pub symbol: String,
    pub spot_token: String,
    pub lighter_market_index: u32,
    pub spot_side: SpotSide,
    pub perp_side: PerpSide,
    pub spot_notional_micros: u64,
    pub perp_notional_micros: u64,
    pub nav_micros: u64,
    #[serde(with = "u128_string")]
    pub raw_spot_amount: u128,
    #[serde(with = "u128_string")]
    pub settlement_amount_in: u128,
    #[serde(with = "u128_string")]
    pub minimum_spot_amount_out: u128,
    #[serde(with = "u128_string")]
    pub minimum_unwind_settlement_out: u128,
    pub spot_decimals: u8,
    pub spot_config_version: u64,
    pub perp_base_amount: u64,
    pub perp_base_decimals: u8,
    pub perp_price_decimals: u8,
    pub perp_limit_price: u32,
    pub client_order_index: u64,
    pub perp_unwind_price: u32,
    pub unwind_client_order_index: u64,
    pub max_unwind_attempts: u8,
    pub perp_order_expiry_ms: u64,
    pub emergency_deadline_ms: u64,
    pub reconciliation_deadline_ms: u64,
    pub leverage_micros: u64,
    pub created_at_ms: u64,
    pub deadline_ms: u64,
    pub evidence: FrozenEvidence,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct SpotAmounts {
    pub settlement_amount_in: u128,
    pub minimum_spot_amount_out: u128,
    pub target_spot_amount: u128,
}

#[derive(Debug, Error, PartialEq, Eq)]
pub enum PairIntentError {
    #[error("intent identity and market fields are required")]
    MissingIdentity,
    #[error("v1 supports long spot and short perp only")]
    UnsupportedDirection,
    #[error("both legs must be positive")]
    EmptyLeg,
    #[error("canary leg cap exceeded")]
    LegCapExceeded,
    #[error("canary gross cap exceeded")]
    GrossCapExceeded,
    #[error("leverage must be positive and no greater than 1x")]
    InvalidLeverage,
    #[error("intent deadline is invalid")]
    InvalidDeadline,
    #[error("frozen evidence is incomplete or stale")]
    InvalidEvidence,
    #[error("venue execution parameters are invalid")]
    InvalidExecution,
    #[error("share exposure does not match after applying the stock-token multiplier")]
    ExposureMismatch,
}

impl PairIntent {
    pub fn validate(&self) -> Result<(), PairIntentError> {
        if !valid_bytes32(&self.id)
            || !valid_bytes32(&self.spot_unwind_intent_id)
            || self.spot_unwind_intent_id == self.id
            || self.symbol.is_empty()
            || !valid_address(&self.spot_token)
            || !valid_bytes32(&self.evidence.dataset_manifest)
            || self.evidence.strategy_version.is_empty()
            || !valid_bytes32(&self.evidence.market_manifest)
            || !valid_bytes32(&self.evidence.quote_block_hash)
        {
            return Err(PairIntentError::MissingIdentity);
        }
        if self.spot_side != SpotSide::Buy || self.perp_side != PerpSide::Short {
            return Err(PairIntentError::UnsupportedDirection);
        }
        if self.spot_notional_micros == 0
            || self.perp_notional_micros == 0
            || self.raw_spot_amount == 0
            || self.settlement_amount_in == 0
            || self.minimum_spot_amount_out == 0
            || self.minimum_unwind_settlement_out == 0
            || self.perp_base_amount == 0
        {
            return Err(PairIntentError::EmptyLeg);
        }
        if self.derived_perp_notional_micros() != Some(self.perp_notional_micros) {
            return Err(PairIntentError::InvalidExecution);
        }
        if self.spot_notional_micros != self.perp_notional_micros {
            return Err(PairIntentError::InvalidExecution);
        }
        let nav_leg_cap = self.nav_micros / LEG_NAV_DENOMINATOR;
        let leg_cap = CANARY_LEG_CAP_MICROS.min(nav_leg_cap);
        if leg_cap == 0
            || self.spot_notional_micros > leg_cap
            || self.perp_notional_micros > leg_cap
        {
            return Err(PairIntentError::LegCapExceeded);
        }
        let gross = self
            .spot_notional_micros
            .checked_add(self.perp_notional_micros)
            .ok_or(PairIntentError::GrossCapExceeded)?;
        let gross_cap = CANARY_GROSS_CAP_MICROS.min(self.nav_micros / GROSS_NAV_DENOMINATOR);
        if gross_cap == 0 || gross > gross_cap {
            return Err(PairIntentError::GrossCapExceeded);
        }
        if self.leverage_micros == 0 || self.leverage_micros > USD_SCALE {
            return Err(PairIntentError::InvalidLeverage);
        }
        if self.created_at_ms >= self.deadline_ms
            || self.evidence.quote_received_at_ms > self.created_at_ms
            || self.evidence.quote_expires_at_ms < self.deadline_ms
            || self.evidence.quote_received_at_ms >= self.evidence.quote_expires_at_ms
        {
            return Err(PairIntentError::InvalidDeadline);
        }
        if self.evidence.ui_multiplier_e18 == 0 {
            return Err(PairIntentError::InvalidEvidence);
        }
        if self.settlement_amount_in != u128::from(self.spot_notional_micros)
            || self.minimum_spot_amount_out > self.raw_spot_amount
            || self.minimum_unwind_settlement_out > self.settlement_amount_in
            || self.spot_config_version == 0
            || self.lighter_market_index > i16::MAX as u32
            || self.perp_base_amount > i64::MAX as u64
            || self.evidence.perp_mark_price == 0
            || self.perp_limit_price == 0
            || self.perp_unwind_price == 0
            || self.client_order_index > MAX_CLIENT_ORDER_INDEX
            || self.unwind_client_order_index > MAX_CLIENT_ORDER_INDEX
            || self.unwind_client_order_index == self.client_order_index
            || !(1..=8).contains(&self.max_unwind_attempts)
            || self
                .unwind_client_order_index
                .checked_add(u64::from(self.max_unwind_attempts) - 1)
                .is_none_or(|last| {
                    last > MAX_CLIENT_ORDER_INDEX
                        || (self.client_order_index >= self.unwind_client_order_index
                            && self.client_order_index <= last)
                })
            || self.perp_order_expiry_ms < self.created_at_ms.saturating_add(MIN_ORDER_EXPIRY_MS)
            || self.perp_order_expiry_ms > self.created_at_ms.saturating_add(MAX_ORDER_EXPIRY_MS)
            || self.emergency_deadline_ms <= self.perp_order_expiry_ms
            || self.emergency_deadline_ms > self.created_at_ms.saturating_add(MAX_ORDER_EXPIRY_MS)
            || self.reconciliation_deadline_ms <= self.emergency_deadline_ms
            || self.reconciliation_deadline_ms
                > self.created_at_ms.saturating_add(MAX_ORDER_EXPIRY_MS)
            || self.deadline_ms > self.perp_order_expiry_ms
        {
            return Err(PairIntentError::InvalidExecution);
        }

        if self.spot_decimals > 38 || self.perp_base_decimals > 18 || self.perp_price_decimals > 18
        {
            return Err(PairIntentError::ExposureMismatch);
        }
        if !self
            .raw_spot_amount
            .is_multiple_of(u128::from(self.perp_base_amount))
            || self.minimum_spot_amount_out < u128::from(self.perp_base_amount)
        {
            return Err(PairIntentError::ExposureMismatch);
        }
        let adjusted_numerator = self
            .raw_spot_amount
            .checked_mul(self.evidence.ui_multiplier_e18)
            .ok_or(PairIntentError::ExposureMismatch)?;
        let multiplier_scale = 1_000_000_000_000_000_000u128;
        if !adjusted_numerator.is_multiple_of(multiplier_scale) {
            return Err(PairIntentError::ExposureMismatch);
        }
        let adjusted_spot_units = adjusted_numerator / multiplier_scale;
        let share_exposure = scale_units(
            adjusted_spot_units,
            self.spot_decimals,
            self.perp_base_decimals,
        )
        .ok_or(PairIntentError::ExposureMismatch)?;
        if share_exposure != u128::from(self.perp_base_amount) {
            return Err(PairIntentError::ExposureMismatch);
        }
        Ok(())
    }

    pub fn spot_amounts_for_fill(&self, filled_base: u64) -> Option<SpotAmounts> {
        if filled_base == 0 || filled_base > self.perp_base_amount {
            return None;
        }
        let denominator = u128::from(self.perp_base_amount);
        let filled = u128::from(filled_base);
        let target_numerator = self.raw_spot_amount.checked_mul(filled)?;
        if !target_numerator.is_multiple_of(denominator) {
            return None;
        }
        let target_spot_amount = target_numerator / denominator;
        let settlement_numerator = self.settlement_amount_in.checked_mul(filled)?;
        let settlement_amount_in =
            settlement_numerator.checked_add(denominator.checked_sub(1)?)? / denominator;
        let minimum_spot_amount_out =
            self.minimum_spot_amount_out.checked_mul(filled)? / denominator;
        if settlement_amount_in == 0 || minimum_spot_amount_out == 0 || target_spot_amount == 0 {
            return None;
        }
        Some(SpotAmounts {
            settlement_amount_in,
            minimum_spot_amount_out,
            target_spot_amount,
        })
    }

    pub fn minimum_unwind_output(&self, spot_amount: u128) -> Option<u128> {
        if spot_amount == 0 {
            return None;
        }
        let numerator = self
            .minimum_unwind_settlement_out
            .checked_mul(spot_amount)?;
        let minimum = numerator / self.raw_spot_amount;
        (minimum > 0).then_some(minimum)
    }

    pub fn derived_perp_notional_micros(&self) -> Option<u64> {
        self.perp_notional_for_fill(
            self.perp_base_amount,
            self.perp_limit_price.max(self.evidence.perp_mark_price),
        )
    }

    pub fn perp_notional_for_fill(&self, base_amount: u64, price: u32) -> Option<u64> {
        if base_amount == 0 || price == 0 {
            return None;
        }
        let decimals =
            u32::from(self.perp_base_decimals).checked_add(u32::from(self.perp_price_decimals))?;
        let denominator = 10u128.checked_pow(decimals)?;
        let numerator = u128::from(base_amount)
            .checked_mul(u128::from(price))?
            .checked_mul(u128::from(USD_SCALE))?;
        let micros = numerator.checked_add(denominator.checked_sub(1)?)? / denominator;
        u64::try_from(micros).ok()
    }
}

fn valid_bytes32(value: &str) -> bool {
    value.len() == 66
        && value.starts_with("0x")
        && value[2..]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        && value[2..].bytes().any(|byte| byte != b'0')
}

fn valid_address(value: &str) -> bool {
    value.len() == 42
        && value.starts_with("0x")
        && value[2..]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        && value[2..].bytes().any(|byte| byte != b'0')
}

pub(crate) mod u128_string {
    use serde::{de::Error, Deserialize, Deserializer, Serializer};

    pub fn serialize<S>(value: &u128, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_str(&value.to_string())
    }

    pub fn deserialize<'de, D>(deserializer: D) -> Result<u128, D::Error>
    where
        D: Deserializer<'de>,
    {
        let value = String::deserialize(deserializer)?;
        if value.is_empty()
            || value.starts_with('+')
            || value.trim() != value
            || (value.len() > 1 && value.starts_with('0'))
        {
            return Err(D::Error::custom("invalid uint128 string"));
        }
        value
            .parse::<u128>()
            .map_err(|_| D::Error::custom("invalid uint128 string"))
    }
}

fn scale_units(value: u128, from_decimals: u8, to_decimals: u8) -> Option<u128> {
    if from_decimals == to_decimals {
        return Some(value);
    }
    if from_decimals < to_decimals {
        return value.checked_mul(10u128.checked_pow(u32::from(to_decimals - from_decimals))?);
    }
    let divisor = 10u128.checked_pow(u32::from(from_decimals - to_decimals))?;
    if !value.is_multiple_of(divisor) {
        return None;
    }
    Some(value / divisor)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn intent() -> PairIntent {
        PairIntent {
            id: "0x1111111111111111111111111111111111111111111111111111111111111111".into(),
            spot_unwind_intent_id:
                "0x2222222222222222222222222222222222222222222222222222222222222222".into(),
            symbol: "NVDA".into(),
            spot_token: "0x0000000000000000000000000000000000000001".into(),
            lighter_market_index: 101,
            spot_side: SpotSide::Buy,
            perp_side: PerpSide::Short,
            spot_notional_micros: 25_000_000,
            perp_notional_micros: 25_000_000,
            nav_micros: 10_000_000_000,
            raw_spot_amount: 2_000_000,
            settlement_amount_in: 25_000_000,
            minimum_spot_amount_out: 1_990_000,
            minimum_unwind_settlement_out: 24_000_000,
            spot_decimals: 6,
            spot_config_version: 1,
            perp_base_amount: 1_000_000,
            perp_base_decimals: 6,
            perp_price_decimals: 3,
            perp_limit_price: 25_000,
            client_order_index: 1,
            perp_unwind_price: 30_000,
            unwind_client_order_index: 2,
            max_unwind_attempts: 3,
            perp_order_expiry_ms: 301_000,
            emergency_deadline_ms: 601_000,
            reconciliation_deadline_ms: 86_401_000,
            leverage_micros: 1_000_000,
            created_at_ms: 1_000,
            deadline_ms: 1_500,
            evidence: FrozenEvidence {
                dataset_manifest:
                    "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd".into(),
                strategy_version: "strategy".into(),
                market_manifest:
                    "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa".into(),
                quote_block_hash:
                    "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb".into(),
                quote_received_at_ms: 900,
                quote_expires_at_ms: 1_500,
                ui_multiplier_e18: 500_000_000_000_000_000,
                perp_mark_price: 25_000,
                estimated_total_cost_micros: 10_000,
            },
        }
    }

    #[test]
    fn valid_canary_intent_passes() {
        assert_eq!(intent().validate(), Ok(()));
    }

    #[test]
    fn short_spot_is_rejected() {
        let mut value = intent();
        value.spot_side = SpotSide::Sell;
        assert_eq!(value.validate(), Err(PairIntentError::UnsupportedDirection));
    }

    #[test]
    fn multiplier_mismatch_is_rejected() {
        let mut value = intent();
        value.evidence.ui_multiplier_e18 = 400_000_000_000_000_000;
        assert_eq!(value.validate(), Err(PairIntentError::ExposureMismatch));
    }

    #[test]
    fn venue_decimal_scaling_is_exact() {
        let mut value = intent();
        value.perp_base_amount = 1_000;
        value.perp_base_decimals = 3;
        assert_eq!(value.validate(), Ok(()));

        value.raw_spot_amount = 2_000_001;
        assert_eq!(value.validate(), Err(PairIntentError::ExposureMismatch));
    }

    #[test]
    fn execution_bounds_are_enforced() {
        let mut value = intent();
        value.minimum_spot_amount_out = value.raw_spot_amount + 1;
        assert_eq!(value.validate(), Err(PairIntentError::InvalidExecution));

        let mut value = intent();
        value.perp_order_expiry_ms = value.created_at_ms + MIN_ORDER_EXPIRY_MS - 1;
        assert_eq!(value.validate(), Err(PairIntentError::InvalidExecution));

        let mut value = intent();
        value.client_order_index = MAX_CLIENT_ORDER_INDEX + 1;
        assert_eq!(value.validate(), Err(PairIntentError::InvalidExecution));
    }

    #[test]
    fn partial_fill_scales_spot_leg_exactly() {
        let value = intent();
        assert_eq!(
            value.spot_amounts_for_fill(500_000),
            Some(SpotAmounts {
                settlement_amount_in: 12_500_000,
                minimum_spot_amount_out: 995_000,
                target_spot_amount: 1_000_000,
            })
        );
        assert_eq!(value.spot_amounts_for_fill(0), None);
        assert_eq!(value.spot_amounts_for_fill(1_000_001), None);
        assert_eq!(value.minimum_unwind_output(1_000_000), Some(12_000_000));
    }

    #[test]
    fn uint128_values_use_decimal_strings() {
        let encoded = serde_json::to_value(intent()).unwrap();
        assert_eq!(encoded["raw_spot_amount"], "2000000");
        let mut invalid = encoded;
        invalid["raw_spot_amount"] = serde_json::json!(2_000_000);
        assert!(serde_json::from_value::<PairIntent>(invalid).is_err());
    }

    #[test]
    fn leg_and_leverage_caps_are_enforced() {
        let mut value = intent();
        value.perp_limit_price = 25_001;
        value.spot_notional_micros = 25_001_000;
        value.perp_notional_micros = 25_001_000;
        value.settlement_amount_in = 25_001_000;
        assert_eq!(value.validate(), Err(PairIntentError::LegCapExceeded));

        let mut value = intent();
        value.leverage_micros += 1;
        assert_eq!(value.validate(), Err(PairIntentError::InvalidLeverage));
    }

    #[test]
    fn perp_notional_is_derived_from_executable_quantity_and_price() {
        let mut value = intent();
        value.perp_notional_micros = 1;
        assert_eq!(value.validate(), Err(PairIntentError::InvalidExecution));

        let value = intent();
        assert_eq!(value.derived_perp_notional_micros(), Some(25_000_000));
    }

    #[test]
    fn spot_notional_and_fill_granularity_are_enforced() {
        let mut value = intent();
        value.spot_notional_micros = 1;
        value.settlement_amount_in = 1;
        assert_eq!(value.validate(), Err(PairIntentError::InvalidExecution));

        let mut value = intent();
        value.raw_spot_amount += 1;
        assert_eq!(value.validate(), Err(PairIntentError::ExposureMismatch));
    }

    #[test]
    fn execution_identifiers_are_canonical_lowercase_hex() {
        let mut value = intent();
        value.id.replace_range(65..66, "A");
        assert_eq!(value.validate(), Err(PairIntentError::MissingIdentity));
    }

    #[test]
    fn nav_relative_caps_are_enforced() {
        let mut value = intent();
        value.nav_micros = 4_000_000_000;
        assert_eq!(value.validate(), Err(PairIntentError::LegCapExceeded));

        value.spot_notional_micros = 10_000_000;
        value.perp_notional_micros = 10_000_000;
        value.settlement_amount_in = 10_000_000;
        value.minimum_unwind_settlement_out = 9_600_000;
        value.raw_spot_amount = 800_000;
        value.minimum_spot_amount_out = 796_000;
        value.perp_base_amount = 400_000;
        assert_eq!(value.validate(), Ok(()));
    }
}
