//! Decision engine for delta-neutral basis trades. It turns a cross-venue basis observation
//! (Uniswap spot vs Lighter perp) into a sized, delta-neutral order plan bounded by portfolio
//! risk limits. Pure logic: no chain client, no network, no database. Callers feed observations
//! and portfolio state in and receive a plan out, so every decision is reproducible and testable
//! off-line, and the same plan can be replayed against the on-chain record.

pub mod basis;
pub mod neutral;
pub mod risk;
pub mod sizing;

pub use basis::{basis_bps, evaluate, BasisConfig, BasisObservation, BasisSignal, Direction};
pub use neutral::{Leg, NeutralPlan, Side, Venue};
pub use risk::{
    check_order, record_close, record_fill, reset_daily, RiskCheck, RiskLimits, RiskState,
};
pub use sizing::{size, SizingInput, SizingResult};

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TradePlan {
    pub signal: BasisSignal,
    pub sizing: SizingResult,
    pub plan: NeutralPlan,
}

/// Full pipeline: evaluate the basis, size it by Kelly, clear it against risk limits, and build
/// the neutral legs. Returns None, with no state change, whenever any stage declines: no
/// tradeable basis, no positive edge, a risk limit, or an unbuildable plan. The order of the
/// gates matters: a trade must pass every one, and the cheapest checks run first.
pub fn plan_trade(
    obs: &BasisObservation,
    bcfg: &BasisConfig,
    sizing_in: &SizingInput,
    risk_state: &RiskState,
    risk_limits: &RiskLimits,
) -> Option<TradePlan> {
    let signal = basis::evaluate(obs, bcfg)?;

    let sizing = sizing::size(sizing_in);
    if sizing.skip || sizing.notional_usd <= 0.0 {
        return None;
    }

    if !risk::check_order(risk_state, risk_limits, sizing.notional_usd).allowed {
        return None;
    }

    let plan = neutral::build(&signal, sizing.notional_usd)?;
    Some(TradePlan {
        signal,
        sizing,
        plan,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn good_obs() -> BasisObservation {
        BasisObservation {
            symbol: "NVDA".into(),
            spot_price: 212.0,
            perp_mark: 214.0, // ~94 bps, perp rich
            spot_liquidity: 1e16,
            age_secs: 4.0,
        }
    }

    fn good_sizing() -> SizingInput {
        SizingInput {
            bankroll_usd: 100_000.0,
            expected_return: 0.004,
            return_vol: 0.02,
            kelly_fraction: 0.25,
            max_position_pct: 0.0002, // keep the sized notional under the default per-entry cap
            correlated: false,
            drawdown_pct: 0.0,
        }
    }

    #[test]
    fn full_pipeline_produces_plan() {
        let plan = plan_trade(
            &good_obs(),
            &BasisConfig::default(),
            &good_sizing(),
            &RiskState::new(100_000.0),
            &RiskLimits::default(),
        )
        .expect("should plan a trade");
        assert_eq!(plan.plan.spot.side, Side::Long);
        assert_eq!(plan.plan.perp.side, Side::Short);
        assert!(plan.plan.net_delta_usd.abs() < 1e-6);
    }

    #[test]
    fn no_basis_no_plan() {
        let mut o = good_obs();
        o.perp_mark = 212.05; // ~2 bps, below entry
        assert!(plan_trade(
            &o,
            &BasisConfig::default(),
            &good_sizing(),
            &RiskState::new(100_000.0),
            &RiskLimits::default()
        )
        .is_none());
    }

    #[test]
    fn kill_switch_blocks_plan() {
        let mut s = RiskState::new(100_000.0);
        s.kill_switch = Some("halted".into());
        assert!(plan_trade(
            &good_obs(),
            &BasisConfig::default(),
            &good_sizing(),
            &s,
            &RiskLimits::default()
        )
        .is_none());
    }
}
