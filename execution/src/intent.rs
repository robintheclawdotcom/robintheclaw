use serde::{Deserialize, Serialize};
use thiserror::Error;

pub const USD_SCALE: u64 = 1_000_000;
pub const CANARY_LEG_CAP_MICROS: u64 = 25 * USD_SCALE;
pub const CANARY_GROSS_CAP_MICROS: u64 = 50 * USD_SCALE;
const LEG_NAV_DENOMINATOR: u64 = 400;
const GROSS_NAV_DENOMINATOR: u64 = 200;

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
pub struct FrozenEvidence {
    pub dataset_manifest: String,
    pub strategy_version: String,
    pub market_manifest: String,
    pub quote_block_hash: String,
    pub quote_received_at_ms: u64,
    pub quote_expires_at_ms: u64,
    pub ui_multiplier_e18: u128,
    pub estimated_total_cost_micros: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct PairIntent {
    pub id: String,
    pub symbol: String,
    pub spot_token: String,
    pub lighter_market_index: u32,
    pub spot_side: SpotSide,
    pub perp_side: PerpSide,
    pub spot_notional_micros: u64,
    pub perp_notional_micros: u64,
    pub nav_micros: u64,
    pub raw_spot_amount: u128,
    pub spot_decimals: u8,
    pub perp_base_amount: u64,
    pub perp_base_decimals: u8,
    pub leverage_micros: u64,
    pub created_at_ms: u64,
    pub deadline_ms: u64,
    pub evidence: FrozenEvidence,
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
    #[error("share exposure does not match after applying the stock-token multiplier")]
    ExposureMismatch,
}

impl PairIntent {
    pub fn validate(&self) -> Result<(), PairIntentError> {
        if self.id.is_empty()
            || self.symbol.is_empty()
            || self.spot_token.is_empty()
            || self.evidence.dataset_manifest.is_empty()
            || self.evidence.strategy_version.is_empty()
            || self.evidence.market_manifest.is_empty()
            || self.evidence.quote_block_hash.is_empty()
        {
            return Err(PairIntentError::MissingIdentity);
        }
        if self.spot_side != SpotSide::Buy || self.perp_side != PerpSide::Short {
            return Err(PairIntentError::UnsupportedDirection);
        }
        if self.spot_notional_micros == 0
            || self.perp_notional_micros == 0
            || self.raw_spot_amount == 0
            || self.perp_base_amount == 0
        {
            return Err(PairIntentError::EmptyLeg);
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

        if self.spot_decimals > 38 || self.perp_base_decimals > 18 {
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
            id: "intent-1".into(),
            symbol: "NVDA".into(),
            spot_token: "0x0000000000000000000000000000000000000001".into(),
            lighter_market_index: 101,
            spot_side: SpotSide::Buy,
            perp_side: PerpSide::Short,
            spot_notional_micros: 25_000_000,
            perp_notional_micros: 25_000_000,
            nav_micros: 10_000_000_000,
            raw_spot_amount: 2_000_000,
            spot_decimals: 6,
            perp_base_amount: 1_000_000,
            perp_base_decimals: 6,
            leverage_micros: 1_000_000,
            created_at_ms: 1_000,
            deadline_ms: 1_500,
            evidence: FrozenEvidence {
                dataset_manifest: "dataset".into(),
                strategy_version: "strategy".into(),
                market_manifest: "market".into(),
                quote_block_hash: "0x01".into(),
                quote_received_at_ms: 900,
                quote_expires_at_ms: 1_500,
                ui_multiplier_e18: 500_000_000_000_000_000,
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
    fn leg_and_leverage_caps_are_enforced() {
        let mut value = intent();
        value.spot_notional_micros += 1;
        assert_eq!(value.validate(), Err(PairIntentError::LegCapExceeded));

        let mut value = intent();
        value.leverage_micros += 1;
        assert_eq!(value.validate(), Err(PairIntentError::InvalidLeverage));
    }

    #[test]
    fn nav_relative_caps_are_enforced() {
        let mut value = intent();
        value.nav_micros = 4_000_000_000;
        assert_eq!(value.validate(), Err(PairIntentError::LegCapExceeded));

        value.spot_notional_micros = 10_000_000;
        value.perp_notional_micros = 10_000_000;
        assert_eq!(value.validate(), Ok(()));
    }
}
