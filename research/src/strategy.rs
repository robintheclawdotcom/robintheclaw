use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use thiserror::Error;

pub const BASIS_AAPL_V1: &str = "basis-aapl-v1";
pub const ROBINHOOD_MAINNET_CHAIN_ID: u64 = 4663;
pub const MAX_LEG_NOTIONAL_MICROS: u64 = 25_000_000;
pub const MAX_GROSS_NOTIONAL_MICROS: u64 = 50_000_000;
pub const MAX_DAILY_TURNOVER_MICROS: u64 = 50_000_000;
pub const ONE_X_LEVERAGE_PPM: u32 = 1_000_000;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum StrategyPromotionState {
    Paper,
    Shadow,
    Canary,
    Cohort,
    Public,
    Retired,
}

impl StrategyPromotionState {
    pub fn can_transition_to(self, next: Self) -> bool {
        matches!(
            (self, next),
            (Self::Paper, Self::Shadow)
                | (Self::Shadow, Self::Canary)
                | (Self::Canary, Self::Cohort)
                | (Self::Cohort, Self::Public)
                | (
                    Self::Paper | Self::Shadow | Self::Canary | Self::Cohort | Self::Public,
                    Self::Retired
                )
        )
    }

    pub fn admits_live_capital(self) -> bool {
        matches!(self, Self::Canary | Self::Cohort | Self::Public)
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StrategyManifest {
    pub schema_version: u32,
    pub strategy_version: String,
    pub chain_id: u64,
    pub symbol: String,
    pub direction: String,
    pub source_config_sha256: String,
    pub route_sha256: String,
    pub oracle_policy_sha256: String,
    pub risk_policy_sha256: String,
    pub code_commit: String,
    pub max_leg_notional_micros: u64,
    pub max_gross_notional_micros: u64,
    pub max_daily_turnover_micros: u64,
    pub max_leverage_ppm: u32,
    pub max_active_episodes: u8,
    pub sha256: String,
}

#[derive(Debug, Error, PartialEq, Eq)]
pub enum StrategyManifestError {
    #[error("strategy identity is invalid")]
    InvalidIdentity,
    #[error("strategy digest is malformed")]
    InvalidDigest,
    #[error("strategy policy differs from basis-aapl-v1")]
    InvalidPolicy,
    #[error("strategy checksum does not match")]
    ChecksumMismatch,
}

impl StrategyManifest {
    pub fn calculate_hash(&self) -> String {
        let mut hasher = Sha256::new();
        hasher.update(self.schema_version.to_be_bytes());
        for value in [
            &self.strategy_version,
            &self.symbol,
            &self.direction,
            &self.source_config_sha256,
            &self.route_sha256,
            &self.oracle_policy_sha256,
            &self.risk_policy_sha256,
            &self.code_commit,
        ] {
            write_field(&mut hasher, value.as_bytes());
        }
        hasher.update(self.chain_id.to_be_bytes());
        hasher.update(self.max_leg_notional_micros.to_be_bytes());
        hasher.update(self.max_gross_notional_micros.to_be_bytes());
        hasher.update(self.max_daily_turnover_micros.to_be_bytes());
        hasher.update(self.max_leverage_ppm.to_be_bytes());
        hasher.update([self.max_active_episodes]);
        hex::encode(hasher.finalize())
    }

    pub fn validate(&self) -> Result<(), StrategyManifestError> {
        if self.schema_version != 1
            || self.strategy_version != BASIS_AAPL_V1
            || self.chain_id != ROBINHOOD_MAINNET_CHAIN_ID
            || self.symbol != "AAPL"
            || self.direction != "long_spot_short_perp"
            || self.code_commit.is_empty()
        {
            return Err(StrategyManifestError::InvalidIdentity);
        }
        if [
            &self.source_config_sha256,
            &self.route_sha256,
            &self.oracle_policy_sha256,
            &self.risk_policy_sha256,
            &self.sha256,
        ]
        .into_iter()
        .any(|value| !is_sha256(value))
        {
            return Err(StrategyManifestError::InvalidDigest);
        }
        if self.max_leg_notional_micros != MAX_LEG_NOTIONAL_MICROS
            || self.max_gross_notional_micros != MAX_GROSS_NOTIONAL_MICROS
            || self.max_daily_turnover_micros != MAX_DAILY_TURNOVER_MICROS
            || self.max_leverage_ppm != ONE_X_LEVERAGE_PPM
            || self.max_active_episodes != 1
        {
            return Err(StrategyManifestError::InvalidPolicy);
        }
        if self.sha256 != self.calculate_hash() {
            return Err(StrategyManifestError::ChecksumMismatch);
        }
        Ok(())
    }
}

fn write_field(hasher: &mut Sha256, value: &[u8]) {
    hasher.update((value.len() as u64).to_be_bytes());
    hasher.update(value);
}

fn is_sha256(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn manifest() -> StrategyManifest {
        let mut manifest = StrategyManifest {
            schema_version: 1,
            strategy_version: BASIS_AAPL_V1.into(),
            chain_id: ROBINHOOD_MAINNET_CHAIN_ID,
            symbol: "AAPL".into(),
            direction: "long_spot_short_perp".into(),
            source_config_sha256: "a".repeat(64),
            route_sha256: "b".repeat(64),
            oracle_policy_sha256: "c".repeat(64),
            risk_policy_sha256: "d".repeat(64),
            code_commit: "0123456789abcdef".into(),
            max_leg_notional_micros: MAX_LEG_NOTIONAL_MICROS,
            max_gross_notional_micros: MAX_GROSS_NOTIONAL_MICROS,
            max_daily_turnover_micros: MAX_DAILY_TURNOVER_MICROS,
            max_leverage_ppm: ONE_X_LEVERAGE_PPM,
            max_active_episodes: 1,
            sha256: String::new(),
        };
        manifest.sha256 = manifest.calculate_hash();
        manifest
    }

    #[test]
    fn fixed_live_manifest_validates() {
        assert_eq!(manifest().validate(), Ok(()));
    }

    #[test]
    fn policy_cannot_raise_cap() {
        let mut manifest = manifest();
        manifest.max_leg_notional_micros += 1;
        manifest.sha256 = manifest.calculate_hash();
        assert_eq!(
            manifest.validate(),
            Err(StrategyManifestError::InvalidPolicy)
        );
    }

    #[test]
    fn manifest_detects_tampering() {
        let mut manifest = manifest();
        manifest.route_sha256 = "e".repeat(64);
        assert_eq!(
            manifest.validate(),
            Err(StrategyManifestError::ChecksumMismatch)
        );
    }

    #[test]
    fn promotion_sequence_is_monotonic() {
        assert!(StrategyPromotionState::Paper.can_transition_to(StrategyPromotionState::Shadow));
        assert!(!StrategyPromotionState::Paper.can_transition_to(StrategyPromotionState::Public));
        assert!(StrategyPromotionState::Cohort.admits_live_capital());
        assert!(!StrategyPromotionState::Shadow.admits_live_capital());
        assert!(StrategyPromotionState::Public.can_transition_to(StrategyPromotionState::Retired));
    }
}
