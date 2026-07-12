use serde::{Deserialize, Serialize};

/// Inputs for continuous-return Kelly sizing of one delta-neutral basis trade. The trade is not a
/// binary bet, so sizing works off the expected holding-period return and its volatility rather
/// than a win probability.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SizingInput {
    pub bankroll_usd: f64,
    /// expected holding-period return on the position, as a fraction (basis + funding carry - costs)
    pub expected_return: f64,
    /// standard deviation of that return over the holding period
    pub return_vol: f64,
    /// fraction of full Kelly to apply; quarter-Kelly (0.25) is the default discipline
    pub kelly_fraction: f64,
    /// hard cap on the fraction of bankroll a single position may take
    pub max_position_pct: f64,
    /// halve size when this name is correlated with an existing position
    pub correlated: bool,
    /// current drawdown from the equity peak, as a percentage (0..100)
    pub drawdown_pct: f64,
}

impl Default for SizingInput {
    fn default() -> Self {
        Self {
            bankroll_usd: 1000.0,
            expected_return: 0.0,
            return_vol: 0.01,
            kelly_fraction: 0.25,
            max_position_pct: 0.05,
            correlated: false,
            drawdown_pct: 0.0,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SizingResult {
    pub skip: bool,
    pub full_kelly_fraction: f64,
    pub adjusted_fraction: f64,
    pub notional_usd: f64,
    pub edge_bps: i32,
    pub reason: String,
}

fn skip(reason: &str) -> SizingResult {
    SizingResult {
        skip: true,
        full_kelly_fraction: 0.0,
        adjusted_fraction: 0.0,
        notional_usd: 0.0,
        edge_bps: 0,
        reason: reason.to_string(),
    }
}

/// Size a position by continuous-return Kelly (f* = mu / sigma^2). The fractional-Kelly result is
/// first capped at the per-position limit, then reduced by the correlation penalty and the
/// drawdown circuit breaker. Applying the penalties after the cap is deliberate: a drawdown must
/// cut the position even when Kelly wanted more than the cap allowed. Skips when there is no
/// positive edge or no usable volatility estimate.
pub fn size(input: &SizingInput) -> SizingResult {
    if !input.bankroll_usd.is_finite()
        || !input.expected_return.is_finite()
        || !input.return_vol.is_finite()
        || !input.kelly_fraction.is_finite()
        || !input.max_position_pct.is_finite()
        || !input.drawdown_pct.is_finite()
    {
        return skip("sizing input is not finite");
    }
    if input.bankroll_usd <= 0.0 {
        return skip("bankroll is zero or negative");
    }
    if input.expected_return <= 0.0 {
        return skip("no positive expected return");
    }
    if input.return_vol <= 0.0 {
        return skip("no usable volatility estimate");
    }
    if input.kelly_fraction <= 0.0 || input.max_position_pct <= 0.0 || input.drawdown_pct < 0.0 {
        return skip("invalid sizing limits");
    }

    let full_kelly = input.expected_return / (input.return_vol * input.return_vol);
    if full_kelly <= 0.0 {
        return skip("kelly fraction non-positive");
    }

    let mut adjusted =
        (full_kelly * input.kelly_fraction.clamp(0.01, 1.0)).min(input.max_position_pct);

    if input.correlated {
        adjusted *= 0.5;
    }
    if input.drawdown_pct > 20.0 {
        adjusted *= 0.25;
    } else if input.drawdown_pct > 10.0 {
        adjusted *= 0.5;
    }

    adjusted = adjusted.max(0.0);
    let notional = input.bankroll_usd * adjusted;
    let edge_bps = (input.expected_return * 10_000.0).round() as i32;

    if notional <= 0.0 {
        return skip("sized notional is zero");
    }

    let reason = format!(
        "kelly: edge {edge_bps}bps, full {:.1}%, adj {:.2}%, ${:.2}",
        full_kelly * 100.0,
        adjusted * 100.0,
        notional
    );

    SizingResult {
        skip: false,
        full_kelly_fraction: full_kelly,
        adjusted_fraction: adjusted,
        notional_usd: notional,
        edge_bps,
        reason,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn base() -> SizingInput {
        SizingInput {
            bankroll_usd: 10_000.0,
            expected_return: 0.004, // 40 bps
            return_vol: 0.02,
            kelly_fraction: 0.25,
            max_position_pct: 0.10,
            correlated: false,
            drawdown_pct: 0.0,
        }
    }

    #[test]
    fn positive_edge_sizes() {
        let r = size(&base());
        assert!(!r.skip);
        assert!(r.notional_usd > 0.0);
        assert_eq!(r.edge_bps, 40);
    }

    #[test]
    fn no_edge_skips() {
        let mut i = base();
        i.expected_return = 0.0;
        assert!(size(&i).skip);
    }

    #[test]
    fn zero_vol_skips() {
        let mut i = base();
        i.return_vol = 0.0;
        assert!(size(&i).skip);
    }

    #[test]
    fn hard_cap_respected() {
        let mut i = base();
        i.expected_return = 0.05; // huge edge -> full kelly would blow past the cap
        i.return_vol = 0.02;
        let r = size(&i);
        assert!(r.adjusted_fraction <= i.max_position_pct + 1e-9);
    }

    #[test]
    fn correlation_reduces() {
        let r1 = size(&base());
        let mut c = base();
        c.correlated = true;
        let r2 = size(&c);
        assert!(r2.adjusted_fraction < r1.adjusted_fraction);
    }

    #[test]
    fn drawdown_breaker_reduces() {
        let r1 = size(&base());
        let mut d = base();
        d.drawdown_pct = 25.0;
        let r2 = size(&d);
        assert!(r2.adjusted_fraction <= r1.adjusted_fraction * 0.25 + 1e-9);
    }

    #[test]
    fn non_finite_input_skips() {
        let mut i = base();
        i.expected_return = f64::NAN;
        assert!(size(&i).skip);
    }
}
