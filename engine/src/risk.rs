use serde::{Deserialize, Serialize};

/// Portfolio risk limits. The engine holds these in memory; persistence and the on-chain mandate
/// (MandateGuard) are the caller's job. These bound a single entry and the book as a whole, and
/// arm the drawdown kill switch.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RiskLimits {
    pub max_entry_notional: f64,
    pub max_bankroll_fraction_per_entry: f64,
    pub max_gross_exposure_fraction: f64,
    pub daily_drawdown_kill: f64,
    pub weekly_drawdown_kill: f64,
}

impl Default for RiskLimits {
    fn default() -> Self {
        Self {
            max_entry_notional: 25.0,
            max_bankroll_fraction_per_entry: 0.01,
            max_gross_exposure_fraction: 0.10,
            daily_drawdown_kill: 0.03,
            weekly_drawdown_kill: 0.08,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RiskState {
    pub bankroll_usd: f64,
    pub daily_pnl_usd: f64,
    pub weekly_high_usd: f64,
    pub gross_open_usd: f64,
    pub kill_switch: Option<String>,
}

impl RiskState {
    pub fn new(bankroll_usd: f64) -> Self {
        Self {
            bankroll_usd,
            daily_pnl_usd: 0.0,
            weekly_high_usd: bankroll_usd,
            gross_open_usd: 0.0,
            kill_switch: None,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RiskCheck {
    pub allowed: bool,
    pub reason: Option<String>,
}

fn deny(reason: String) -> RiskCheck {
    RiskCheck {
        allowed: false,
        reason: Some(reason),
    }
}

/// Reject an entry's total gross notional when it trips the kill switch, exceeds the caps, or
/// would push the book past its gross-exposure ceiling.
pub fn check_order(state: &RiskState, limits: &RiskLimits, gross_notional: f64) -> RiskCheck {
    if !gross_notional.is_finite() || gross_notional <= 0.0 {
        return deny("gross notional must be positive and finite".to_string());
    }
    if !state.bankroll_usd.is_finite()
        || !state.gross_open_usd.is_finite()
        || state.bankroll_usd <= 0.0
        || state.gross_open_usd < 0.0
        || !limits.max_entry_notional.is_finite()
        || !limits.max_bankroll_fraction_per_entry.is_finite()
        || !limits.max_gross_exposure_fraction.is_finite()
        || limits.max_entry_notional <= 0.0
        || !(0.0..=1.0).contains(&limits.max_bankroll_fraction_per_entry)
        || !(0.0..=1.0).contains(&limits.max_gross_exposure_fraction)
    {
        return deny("invalid risk state or limits".to_string());
    }
    if let Some(r) = &state.kill_switch {
        return deny(format!("kill switch active: {r}"));
    }
    if gross_notional > limits.max_entry_notional {
        return deny(format!(
            "gross notional ${gross_notional:.2} exceeds per-entry cap ${:.2}",
            limits.max_entry_notional
        ));
    }
    let max_from_bankroll = state.bankroll_usd * limits.max_bankroll_fraction_per_entry;
    if gross_notional > max_from_bankroll {
        return deny(format!(
            "gross notional ${gross_notional:.2} exceeds {:.1}% of bankroll (${max_from_bankroll:.2})",
            limits.max_bankroll_fraction_per_entry * 100.0
        ));
    }
    let max_exposure = state.bankroll_usd * limits.max_gross_exposure_fraction;
    if state.gross_open_usd + gross_notional > max_exposure {
        return deny(format!(
            "gross ${:.2} + ${gross_notional:.2} exceeds {:.1}% cap (${max_exposure:.2})",
            state.gross_open_usd,
            limits.max_gross_exposure_fraction * 100.0
        ));
    }
    RiskCheck {
        allowed: true,
        reason: None,
    }
}

/// Apply a confirmed fill: accrue PnL and gross exposure, lift the weekly high, and trip the kill
/// switch if the daily or weekly drawdown limit is breached.
pub fn record_fill(state: &mut RiskState, limits: &RiskLimits, notional: f64, realized_pnl: f64) {
    state.daily_pnl_usd += realized_pnl;
    state.gross_open_usd = (state.gross_open_usd + notional).max(0.0);
    let equity = state.bankroll_usd + state.daily_pnl_usd;
    state.weekly_high_usd = state.weekly_high_usd.max(equity);

    let daily_dd = -state.daily_pnl_usd / state.bankroll_usd;
    if daily_dd >= limits.daily_drawdown_kill {
        state.kill_switch = Some(format!(
            "daily drawdown {:.1}% >= {:.1}%",
            daily_dd * 100.0,
            limits.daily_drawdown_kill * 100.0
        ));
        return;
    }
    let weekly_dd = (state.weekly_high_usd - equity) / state.weekly_high_usd;
    if weekly_dd >= limits.weekly_drawdown_kill {
        state.kill_switch = Some(format!(
            "weekly drawdown {:.1}% >= {:.1}%",
            weekly_dd * 100.0,
            limits.weekly_drawdown_kill * 100.0
        ));
    }
}

pub fn record_close(state: &mut RiskState, notional_closed: f64) {
    state.gross_open_usd = (state.gross_open_usd - notional_closed).max(0.0);
}

pub fn reset_daily(state: &mut RiskState) {
    state.daily_pnl_usd = 0.0;
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn per_entry_cap_blocks() {
        let s = RiskState::new(100_000.0);
        let l = RiskLimits::default();
        assert!(!check_order(&s, &l, 26.0).allowed); // over the $25 hard cap
    }

    #[test]
    fn bankroll_fraction_blocks() {
        let s = RiskState::new(1_000.0);
        let l = RiskLimits::default();
        // 1% of 1000 = 10; a 20 notional is under the $25 hard cap but over the bankroll fraction
        assert!(!check_order(&s, &l, 20.0).allowed);
    }

    #[test]
    fn gross_exposure_blocks() {
        let mut s = RiskState::new(1_000.0);
        s.gross_open_usd = 99.0;
        let l = RiskLimits::default(); // 10% of 1000 = 100 cap
        assert!(!check_order(&s, &l, 5.0).allowed);
    }

    #[test]
    fn within_limits_allowed() {
        let s = RiskState::new(100_000.0);
        let l = RiskLimits::default();
        assert!(check_order(&s, &l, 10.0).allowed);
    }

    #[test]
    fn daily_drawdown_trips_kill() {
        let mut s = RiskState::new(1_000.0);
        let l = RiskLimits::default();
        record_fill(&mut s, &l, 10.0, -35.0); // -3.5% > 3% daily limit
        assert!(s.kill_switch.is_some());
        assert!(!check_order(&s, &l, 1.0).allowed);
    }

    #[test]
    fn weekly_drawdown_trips_kill() {
        let mut s = RiskState::new(1_000.0);
        let l = RiskLimits::default();
        s.weekly_high_usd = 1_100.0; // prior peak above bankroll
        record_fill(&mut s, &l, 10.0, -20.0); // equity 980 vs high 1100 = ~10.9% weekly
        assert!(s.kill_switch.is_some());
    }

    #[test]
    fn close_reduces_gross() {
        let mut s = RiskState::new(1_000.0);
        s.gross_open_usd = 50.0;
        record_close(&mut s, 30.0);
        assert!((s.gross_open_usd - 20.0).abs() < 1e-9);
    }

    #[test]
    fn non_finite_notional_is_rejected() {
        let s = RiskState::new(1_000.0);
        assert!(!check_order(&s, &RiskLimits::default(), f64::NAN).allowed);
    }
}
