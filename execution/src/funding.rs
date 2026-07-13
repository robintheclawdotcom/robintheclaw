use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct FundingInput {
    pub spot_notional_micros: u64,
    pub perp_notional_micros: u64,
    pub lighter_fee_buffer_micros: u64,
    pub repair_buffer_micros: u64,
    pub round_trip_gas_wei: u128,
    pub safe_recovery_gas_wei: u128,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct FundingPlan {
    pub vault_usdg_micros: u64,
    pub lighter_usdc_micros: u64,
    pub executor_eth_wei: u128,
    pub safe_eth_wei: u128,
}

impl FundingPlan {
    pub fn calculate(input: &FundingInput) -> Option<Self> {
        if input.spot_notional_micros == 0 || input.perp_notional_micros == 0 {
            return None;
        }
        let lighter_usdc_micros = input
            .perp_notional_micros
            .checked_add(input.lighter_fee_buffer_micros)?
            .checked_add(input.repair_buffer_micros)?;
        let executor_eth_wei = input.round_trip_gas_wei.checked_mul(20)?;
        let safe_eth_wei = input.safe_recovery_gas_wei.checked_mul(2)?;
        Some(Self {
            vault_usdg_micros: input.spot_notional_micros,
            lighter_usdc_micros,
            executor_eth_wei,
            safe_eth_wei,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn funding_assets_remain_separate() {
        let plan = FundingPlan::calculate(&FundingInput {
            spot_notional_micros: 25_000_000,
            perp_notional_micros: 25_000_000,
            lighter_fee_buffer_micros: 250_000,
            repair_buffer_micros: 1_000_000,
            round_trip_gas_wei: 100,
            safe_recovery_gas_wei: 200,
        })
        .unwrap();
        assert_eq!(plan.vault_usdg_micros, 25_000_000);
        assert_eq!(plan.lighter_usdc_micros, 26_250_000);
        assert_eq!(plan.executor_eth_wei, 2_000);
        assert_eq!(plan.safe_eth_wei, 400);
    }
}
