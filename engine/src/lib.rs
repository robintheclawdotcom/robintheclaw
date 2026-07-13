//! Decision engine for delta-neutral basis trades. It turns a cross-venue basis observation
//! (Uniswap spot vs Lighter perp) into a sized, delta-neutral order plan bounded by portfolio
//! risk limits. Pure logic: no chain client, no network, no database. Callers feed observations
//! and portfolio state in and receive a plan out, so every decision is reproducible and testable
//! off-line, and the same plan can be replayed against the onchain record.

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

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DecisionInput {
    pub observation: BasisObservation,
    pub basis: BasisConfig,
    pub sizing: SizingInput,
    pub risk_state: RiskState,
    pub risk_limits: RiskLimits,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum DecisionStage {
    Basis,
    Sizing,
    Risk,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "status", rename_all = "snake_case")]
pub enum Decision {
    Approved {
        plan: TradePlan,
    },
    Declined {
        stage: DecisionStage,
        reason: String,
    },
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

    let plan = neutral::build(&signal, sizing.notional_usd)?;
    if !risk::check_order(risk_state, risk_limits, plan.gross_notional_usd).allowed {
        return None;
    }

    Some(TradePlan {
        signal,
        sizing,
        plan,
    })
}

/// Produces a serializable decision record for an observation. A decline is a normal outcome:
/// callers persist it alongside approved plans so an operator can audit why no order was sent.
pub fn decide(input: &DecisionInput) -> Decision {
    let Some(signal) = basis::evaluate(&input.observation, &input.basis) else {
        return Decision::Declined {
            stage: DecisionStage::Basis,
            reason: "basis observation did not satisfy the entry gates".to_string(),
        };
    };

    let sizing = sizing::size(&input.sizing);
    if sizing.skip || sizing.notional_usd <= 0.0 {
        return Decision::Declined {
            stage: DecisionStage::Sizing,
            reason: sizing.reason,
        };
    }

    let Some(plan) = neutral::build(&signal, sizing.notional_usd) else {
        return Decision::Declined {
            stage: DecisionStage::Basis,
            reason: "unable to construct a neutral plan".to_string(),
        };
    };

    let risk = risk::check_order(
        &input.risk_state,
        &input.risk_limits,
        plan.gross_notional_usd,
    );
    if !risk.allowed {
        return Decision::Declined {
            stage: DecisionStage::Risk,
            reason: risk
                .reason
                .unwrap_or_else(|| "risk check declined the order".to_string()),
        };
    }

    Decision::Approved {
        plan: TradePlan {
            signal,
            sizing,
            plan,
        },
    }
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
            max_position_pct: 0.0001, // the total two-leg gross remains below the default cap
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

    #[test]
    fn decision_records_risk_decline() {
        let input = DecisionInput {
            observation: good_obs(),
            basis: BasisConfig::default(),
            sizing: good_sizing(),
            risk_state: RiskState::new(1_000.0),
            risk_limits: RiskLimits::default(),
        };
        assert!(matches!(
            decide(&input),
            Decision::Declined {
                stage: DecisionStage::Risk,
                ..
            }
        ));
    }

    #[test]
    fn risk_gate_uses_both_legs() {
        let mut sizing = good_sizing();
        sizing.max_position_pct = 0.0002;
        assert!(plan_trade(
            &good_obs(),
            &BasisConfig::default(),
            &sizing,
            &RiskState::new(100_000.0),
            &RiskLimits::default(),
        )
        .is_none());
    }
}
