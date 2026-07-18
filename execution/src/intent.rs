use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use thiserror::Error;

pub const USD_SCALE: u64 = 1_000_000;
pub const CANARY_LEG_CAP_MICROS: u64 = 25 * USD_SCALE;
pub const CANARY_GROSS_CAP_MICROS: u64 = 50 * USD_SCALE;
pub const CANARY_DAILY_TURNOVER_CAP_MICROS: u64 = 50 * USD_SCALE;
pub const PAIR_INTENT_VERSION: u8 = 2;
pub const CANARY_RISK_VERSION: &str = "basis-aapl-v1";
pub const BASIS_AAPL_V1_MANIFEST_SHA256: &str =
    "27df8d5a56b45f6966f8a60d866a55cfddfc65835216def5def023126c96c937";
pub const BASIS_AAPL_V1_PREVIOUS_MANIFEST_SHA256: &str =
    "da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f";
pub const BASIS_AAPL_V1_LEGACY_MANIFEST_SHA256: &str =
    "4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a";
const PAIR_INTENT_DOMAIN: &[u8] = b"robin.execution.pair-intent.v2\0";
const SPOT_UNWIND_DOMAIN: &[u8] = b"robin.execution.spot-unwind.v2\0";
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
    pub version: u8,
    pub id: String,
    pub spot_unwind_intent_id: String,
    pub execution_account_id: String,
    pub agent_id: String,
    pub source_evaluation_id: String,
    pub risk_version: String,
    pub strategy_manifest_sha256: String,
    pub lighter_account_index: u64,
    pub lighter_api_key_index: u8,
    pub robinhood_vault: String,
    pub robinhood_signer: String,
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
    #[serde(with = "u128_string")]
    pub expected_ui_multiplier: u128,
    #[serde(with = "u128_string")]
    pub min_oracle_round_id: u128,
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
    #[error("only PairIntent v2 is accepted")]
    InvalidVersion,
    #[error("intent identity and market fields are required")]
    MissingIdentity,
    #[error("intent identifiers are not the canonical v2 derivation")]
    IdentityMismatch,
    #[error("execution account binding is invalid")]
    InvalidAccountBinding,
    #[error("risk policy is not the approved AAPL v1 policy")]
    InvalidRiskPolicy,
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
    pub fn derive_identifiers(&mut self) -> Result<(), PairIntentError> {
        self.id = self.calculate_id()?;
        self.spot_unwind_intent_id = self.calculate_spot_unwind_id();
        Ok(())
    }

    pub fn calculate_id(&self) -> Result<String, PairIntentError> {
        let mut material = self.clone();
        material.id.clear();
        material.spot_unwind_intent_id.clear();
        let encoded =
            serde_json::to_vec(&material).map_err(|_| PairIntentError::MissingIdentity)?;
        Ok(domain_hash(PAIR_INTENT_DOMAIN, &encoded))
    }

    pub fn calculate_spot_unwind_id(&self) -> String {
        domain_hash(SPOT_UNWIND_DOMAIN, self.id.as_bytes())
    }

    pub fn validate(&self) -> Result<(), PairIntentError> {
        self.validate_with_manifest_policy(false)
    }

    pub fn validate_for_unwind(&self) -> Result<(), PairIntentError> {
        self.validate_with_manifest_policy(true)
    }

    fn validate_with_manifest_policy(
        &self,
        allow_predecessor_manifest: bool,
    ) -> Result<(), PairIntentError> {
        if self.version != PAIR_INTENT_VERSION {
            return Err(PairIntentError::InvalidVersion);
        }
        if !valid_bytes32(&self.id)
            || !valid_bytes32(&self.spot_unwind_intent_id)
            || self.spot_unwind_intent_id == self.id
            || !valid_execution_id(&self.execution_account_id)
            || !valid_execution_id(&self.agent_id)
            || !valid_bytes32(&self.source_evaluation_id)
            || self.symbol.is_empty()
            || !valid_address(&self.spot_token)
            || !valid_bytes32(&self.evidence.dataset_manifest)
            || self.evidence.strategy_version.is_empty()
            || !valid_bytes32(&self.evidence.market_manifest)
            || !valid_bytes32(&self.evidence.quote_block_hash)
        {
            return Err(PairIntentError::MissingIdentity);
        }
        if self.calculate_id().as_deref() != Ok(self.id.as_str())
            || self.calculate_spot_unwind_id() != self.spot_unwind_intent_id
        {
            return Err(PairIntentError::IdentityMismatch);
        }
        if self.lighter_account_index == 0
            || !(4..=254).contains(&self.lighter_api_key_index)
            || !valid_address(&self.robinhood_vault)
            || !valid_address(&self.robinhood_signer)
            || self.robinhood_vault == self.robinhood_signer
        {
            return Err(PairIntentError::InvalidAccountBinding);
        }
        if self.risk_version != CANARY_RISK_VERSION
            || self.evidence.strategy_version != CANARY_RISK_VERSION
            || !valid_strategy_manifest(&self.strategy_manifest_sha256, allow_predecessor_manifest)
            || self.symbol != "AAPL"
        {
            return Err(PairIntentError::InvalidRiskPolicy);
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
            || self.expected_ui_multiplier == 0
            || self.min_oracle_round_id == 0
            || self.perp_base_amount == 0
        {
            return Err(PairIntentError::EmptyLeg);
        }
        if self.derived_perp_notional_micros() != Some(self.perp_notional_micros) {
            return Err(PairIntentError::InvalidExecution);
        }
        if self.spot_notional_micros > CANARY_LEG_CAP_MICROS
            || self.perp_notional_micros > CANARY_LEG_CAP_MICROS
        {
            return Err(PairIntentError::LegCapExceeded);
        }
        let gross = self
            .spot_notional_micros
            .checked_add(self.perp_notional_micros)
            .ok_or(PairIntentError::GrossCapExceeded)?;
        if gross > CANARY_GROSS_CAP_MICROS {
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
            || self.expected_ui_multiplier != self.evidence.ui_multiplier_e18
            || self.min_oracle_round_id >= (1u128 << 80)
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
        if !self.spot_matches_perp_base(self.raw_spot_amount, self.perp_base_amount) {
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

    pub fn spot_matches_perp_base(&self, spot_amount: u128, perp_base: u64) -> bool {
        if spot_amount == 0 || perp_base == 0 {
            return false;
        }
        let Some(adjusted) = spot_amount
            .checked_mul(self.evidence.ui_multiplier_e18)
            .map(|value| value / 1_000_000_000_000_000_000u128)
        else {
            return false;
        };
        scale_units_floor(adjusted, self.spot_decimals, self.perp_base_decimals)
            == Some(u128::from(perp_base))
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

fn valid_strategy_manifest(value: &str, allow_predecessor: bool) -> bool {
    value == BASIS_AAPL_V1_MANIFEST_SHA256
        || allow_predecessor
            && matches!(
                value,
                BASIS_AAPL_V1_PREVIOUS_MANIFEST_SHA256 | BASIS_AAPL_V1_LEGACY_MANIFEST_SHA256
            )
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

fn valid_execution_id(value: &str) -> bool {
    (8..=64).contains(&value.len())
        && value
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
}

fn domain_hash(domain: &[u8], payload: &[u8]) -> String {
    let mut digest = Sha256::new();
    digest.update(domain);
    digest.update(payload);
    format!("0x{}", hex::encode(digest.finalize()))
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

fn scale_units_floor(value: u128, from_decimals: u8, to_decimals: u8) -> Option<u128> {
    if from_decimals == to_decimals {
        return Some(value);
    }
    if from_decimals < to_decimals {
        return value.checked_mul(10u128.checked_pow(u32::from(to_decimals - from_decimals))?);
    }
    let divisor = 10u128.checked_pow(u32::from(from_decimals - to_decimals))?;
    Some(value / divisor)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn intent() -> PairIntent {
        let mut intent = PairIntent {
            version: PAIR_INTENT_VERSION,
            id: String::new(),
            spot_unwind_intent_id: String::new(),
            execution_account_id: "account-canary-1".into(),
            agent_id: "agent-canary-1".into(),
            source_evaluation_id:
                "0x3333333333333333333333333333333333333333333333333333333333333333".into(),
            risk_version: CANARY_RISK_VERSION.into(),
            strategy_manifest_sha256: BASIS_AAPL_V1_MANIFEST_SHA256.into(),
            lighter_account_index: 7,
            lighter_api_key_index: 4,
            robinhood_vault: "0x0000000000000000000000000000000000000002".into(),
            robinhood_signer: "0x0000000000000000000000000000000000000003".into(),
            symbol: "AAPL".into(),
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
            expected_ui_multiplier: 500_000_000_000_000_000,
            min_oracle_round_id: 1,
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
                strategy_version: CANARY_RISK_VERSION.into(),
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
        };
        intent.derive_identifiers().unwrap();
        intent
    }

    #[test]
    fn valid_canary_intent_passes() {
        assert_eq!(intent().validate(), Ok(()));
    }

    #[test]
    fn reserved_lighter_api_key_is_rejected() {
        let mut value = intent();
        value.lighter_api_key_index = 3;
        value.derive_identifiers().unwrap();
        assert_eq!(
            value.validate(),
            Err(PairIntentError::InvalidAccountBinding)
        );
    }

    #[test]
    fn short_spot_is_rejected() {
        let mut value = intent();
        value.spot_side = SpotSide::Sell;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Err(PairIntentError::UnsupportedDirection));
    }

    #[test]
    fn unapproved_strategy_manifest_is_rejected() {
        let mut value = intent();
        value.strategy_manifest_sha256 = "0".repeat(64);
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Err(PairIntentError::InvalidRiskPolicy));
        assert_eq!(
            value.validate_for_unwind(),
            Err(PairIntentError::InvalidRiskPolicy)
        );
    }

    #[test]
    fn unwind_accepts_only_current_and_explicit_predecessor_manifests() {
        for manifest in [
            BASIS_AAPL_V1_MANIFEST_SHA256,
            BASIS_AAPL_V1_PREVIOUS_MANIFEST_SHA256,
            BASIS_AAPL_V1_LEGACY_MANIFEST_SHA256,
        ] {
            let mut value = intent();
            value.strategy_manifest_sha256 = manifest.into();
            value.derive_identifiers().unwrap();
            assert_eq!(value.validate_for_unwind(), Ok(()));
        }
    }

    #[test]
    fn entry_rejects_predecessor_manifests() {
        for manifest in [
            BASIS_AAPL_V1_PREVIOUS_MANIFEST_SHA256,
            BASIS_AAPL_V1_LEGACY_MANIFEST_SHA256,
        ] {
            let mut value = intent();
            value.strategy_manifest_sha256 = manifest.into();
            value.derive_identifiers().unwrap();
            assert_eq!(value.validate(), Err(PairIntentError::InvalidRiskPolicy));
        }
    }

    #[test]
    fn multiplier_mismatch_is_rejected() {
        let mut value = intent();
        value.evidence.ui_multiplier_e18 = 400_000_000_000_000_000;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Err(PairIntentError::InvalidExecution));
    }

    #[test]
    fn venue_decimal_scaling_allows_only_sub_lot_spot_dust() {
        let mut value = intent();
        value.perp_base_amount = 1_000;
        value.perp_base_decimals = 3;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Ok(()));

        value.raw_spot_amount = 2_000_001;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Ok(()));

        value.raw_spot_amount = 2_002_000;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Err(PairIntentError::ExposureMismatch));
    }

    #[test]
    fn execution_bounds_are_enforced() {
        let mut value = intent();
        value.minimum_spot_amount_out = value.raw_spot_amount + 1;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Err(PairIntentError::InvalidExecution));

        let mut value = intent();
        value.perp_order_expiry_ms = value.created_at_ms + MIN_ORDER_EXPIRY_MS - 1;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Err(PairIntentError::InvalidExecution));

        let mut value = intent();
        value.client_order_index = MAX_CLIENT_ORDER_INDEX + 1;
        value.derive_identifiers().unwrap();
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
        assert_eq!(encoded["expected_ui_multiplier"], "500000000000000000");
        assert_eq!(encoded["min_oracle_round_id"], "1");
        let mut invalid = encoded;
        invalid["raw_spot_amount"] = serde_json::json!(2_000_000);
        assert!(serde_json::from_value::<PairIntent>(invalid).is_err());
    }

    #[test]
    fn spot_oracle_bounds_are_enforced() {
        let mut value = intent();
        value.expected_ui_multiplier -= 1;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Err(PairIntentError::InvalidExecution));

        let mut value = intent();
        value.min_oracle_round_id = 1u128 << 80;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Err(PairIntentError::InvalidExecution));
    }

    #[test]
    fn leg_and_leverage_caps_are_enforced() {
        let mut value = intent();
        value.perp_limit_price = 25_001;
        value.spot_notional_micros = 25_001_000;
        value.perp_notional_micros = 25_001_000;
        value.settlement_amount_in = 25_001_000;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Err(PairIntentError::LegCapExceeded));

        let mut value = intent();
        value.leverage_micros += 1;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Err(PairIntentError::InvalidLeverage));
    }

    #[test]
    fn perp_notional_is_derived_from_executable_quantity_and_price() {
        let mut value = intent();
        value.perp_notional_micros = 1;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Err(PairIntentError::InvalidExecution));

        let value = intent();
        assert_eq!(value.derived_perp_notional_micros(), Some(25_000_000));

        let mut value = intent();
        value.perp_limit_price = 24_999;
        value.evidence.perp_mark_price = 24_999;
        value.perp_notional_micros = 24_999_000;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Ok(()));
    }

    #[test]
    fn spot_notional_and_exposure_are_enforced() {
        let mut value = intent();
        value.spot_notional_micros = 1;
        value.settlement_amount_in = 1;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Err(PairIntentError::InvalidExecution));

        let mut value = intent();
        value.raw_spot_amount += 1;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Ok(()));

        value.raw_spot_amount += 2_000_000;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Err(PairIntentError::ExposureMismatch));
    }

    #[test]
    fn execution_identifiers_are_canonical_lowercase_hex() {
        let mut value = intent();
        value.id.replace_range(65..66, "A");
        assert_eq!(value.validate(), Err(PairIntentError::MissingIdentity));
    }

    #[test]
    fn account_nav_does_not_reduce_fixed_canary_caps() {
        let mut value = intent();
        value.nav_micros = 1;
        value.derive_identifiers().unwrap();
        assert_eq!(value.validate(), Ok(()));
    }

    #[test]
    fn identifiers_are_domain_separated_and_cover_account_binding() {
        let value = intent();
        assert_ne!(value.id, value.spot_unwind_intent_id);
        let mut other = value.clone();
        other.execution_account_id = "account-canary-2".into();
        other.derive_identifiers().unwrap();
        assert_ne!(value.id, other.id);

        other.robinhood_vault = "0x0000000000000000000000000000000000000004".into();
        assert_eq!(other.validate(), Err(PairIntentError::IdentityMismatch));
    }
}
