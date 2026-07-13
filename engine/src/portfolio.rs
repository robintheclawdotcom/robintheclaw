use serde::{Deserialize, Serialize};

pub const PPM: u64 = 1_000_000;
pub const QUARTER_KELLY_PPM: u64 = 250_000;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Regime {
    Normal,
    Illiquid,
    Dislocated,
    Unknown,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct FactorLimit {
    pub factor: String,
    pub current_micros: i64,
    pub proposed_micros: i64,
    pub max_abs_micros: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct PortfolioRiskInput {
    pub regime: Regime,
    pub gross_open_micros: u64,
    pub proposed_gross_micros: u64,
    pub max_gross_micros: u64,
    pub issuer_open_micros: u64,
    pub proposed_issuer_micros: u64,
    pub max_issuer_micros: u64,
    pub margin_utilization_ppm: u64,
    pub proposed_margin_utilization_ppm: u64,
    pub max_margin_utilization_ppm: u64,
    pub drawdown_ppm: u64,
    pub max_drawdown_ppm: u64,
    pub covariance_risk_micros: u64,
    pub max_covariance_risk_micros: u64,
    pub factors: Vec<FactorLimit>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum PortfolioDecline {
    Regime,
    GrossExposure,
    IssuerConcentration,
    Margin,
    Drawdown,
    Covariance,
    FactorExposure,
    InvalidInput,
}

pub fn check_portfolio(input: &PortfolioRiskInput) -> Result<(), PortfolioDecline> {
    if input.regime != Regime::Normal {
        return Err(PortfolioDecline::Regime);
    }
    if input.max_gross_micros == 0
        || input.max_issuer_micros == 0
        || input.max_margin_utilization_ppm > PPM
        || input.max_drawdown_ppm > PPM
    {
        return Err(PortfolioDecline::InvalidInput);
    }
    if input
        .gross_open_micros
        .checked_add(input.proposed_gross_micros)
        .is_none_or(|value| value > input.max_gross_micros)
    {
        return Err(PortfolioDecline::GrossExposure);
    }
    if input
        .issuer_open_micros
        .checked_add(input.proposed_issuer_micros)
        .is_none_or(|value| value > input.max_issuer_micros)
    {
        return Err(PortfolioDecline::IssuerConcentration);
    }
    if input
        .margin_utilization_ppm
        .checked_add(input.proposed_margin_utilization_ppm)
        .is_none_or(|value| value > input.max_margin_utilization_ppm)
    {
        return Err(PortfolioDecline::Margin);
    }
    if input.drawdown_ppm >= input.max_drawdown_ppm {
        return Err(PortfolioDecline::Drawdown);
    }
    if input.covariance_risk_micros > input.max_covariance_risk_micros {
        return Err(PortfolioDecline::Covariance);
    }
    if input.factors.iter().any(|factor| {
        factor.max_abs_micros == 0
            || factor
                .current_micros
                .checked_add(factor.proposed_micros)
                .is_none_or(|value| value.unsigned_abs() > factor.max_abs_micros)
    }) {
        return Err(PortfolioDecline::FactorExposure);
    }
    Ok(())
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct RobustSizingInput {
    pub bankroll_micros: u64,
    pub expected_net_return_ppm: u64,
    pub estimate_uncertainty_ppm: u64,
    pub return_volatility_ppm: u64,
    pub kelly_cap_ppm: u64,
    pub position_cap_micros: u64,
    pub liquidity_capacity_micros: u64,
    pub drawdown_ppm: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct RobustSizingResult {
    pub notional_micros: u64,
    pub applied_fraction_ppm: u64,
    pub robust_edge_ppm: u64,
}

pub fn robust_size(input: &RobustSizingInput) -> Option<RobustSizingResult> {
    if input.bankroll_micros == 0
        || input.return_volatility_ppm == 0
        || input.kelly_cap_ppm == 0
        || input.kelly_cap_ppm > QUARTER_KELLY_PPM
        || input.position_cap_micros == 0
        || input.liquidity_capacity_micros == 0
        || input.drawdown_ppm >= PPM
    {
        return None;
    }
    let robust_edge = input
        .expected_net_return_ppm
        .checked_sub(input.estimate_uncertainty_ppm)?;
    if robust_edge == 0 {
        return None;
    }

    let numerator = u128::from(robust_edge).checked_mul(1_000_000_000_000)?;
    let volatility = u128::from(input.return_volatility_ppm);
    let full_kelly_ppm = numerator.checked_div(volatility.checked_mul(volatility)?)?;
    let capped_kelly_ppm = full_kelly_ppm
        .min(u128::from(input.kelly_cap_ppm))
        .min(u128::from(PPM));
    let drawdown_multiplier_ppm = PPM.checked_sub(input.drawdown_ppm)?;
    let applied_fraction_ppm = capped_kelly_ppm
        .checked_mul(u128::from(drawdown_multiplier_ppm))?
        .checked_div(u128::from(PPM))? as u64;
    let bankroll_size = u128::from(input.bankroll_micros)
        .checked_mul(u128::from(applied_fraction_ppm))?
        .checked_div(u128::from(PPM))? as u64;
    let notional = bankroll_size
        .min(input.position_cap_micros)
        .min(input.liquidity_capacity_micros);
    if notional == 0 {
        return None;
    }
    Some(RobustSizingResult {
        notional_micros: notional,
        applied_fraction_ppm,
        robust_edge_ppm: robust_edge,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn portfolio() -> PortfolioRiskInput {
        PortfolioRiskInput {
            regime: Regime::Normal,
            gross_open_micros: 10,
            proposed_gross_micros: 10,
            max_gross_micros: 100,
            issuer_open_micros: 5,
            proposed_issuer_micros: 5,
            max_issuer_micros: 20,
            margin_utilization_ppm: 100_000,
            proposed_margin_utilization_ppm: 100_000,
            max_margin_utilization_ppm: 500_000,
            drawdown_ppm: 10_000,
            max_drawdown_ppm: 100_000,
            covariance_risk_micros: 10,
            max_covariance_risk_micros: 20,
            factors: vec![FactorLimit {
                factor: "technology".into(),
                current_micros: 5,
                proposed_micros: -2,
                max_abs_micros: 10,
            }],
        }
    }

    #[test]
    fn unknown_regime_declines() {
        let mut input = portfolio();
        input.regime = Regime::Unknown;
        assert_eq!(check_portfolio(&input), Err(PortfolioDecline::Regime));
    }

    #[test]
    fn factor_and_margin_limits_decline() {
        let mut input = portfolio();
        input.factors[0].proposed_micros = 10;
        assert_eq!(
            check_portfolio(&input),
            Err(PortfolioDecline::FactorExposure)
        );

        let mut input = portfolio();
        input.proposed_margin_utilization_ppm = 500_000;
        assert_eq!(check_portfolio(&input), Err(PortfolioDecline::Margin));
    }

    #[test]
    fn worse_uncertainty_never_increases_size() {
        let base = RobustSizingInput {
            bankroll_micros: 1_000_000_000,
            expected_net_return_ppm: 4_000,
            estimate_uncertainty_ppm: 500,
            return_volatility_ppm: 20_000,
            kelly_cap_ppm: QUARTER_KELLY_PPM,
            position_cap_micros: 25_000_000,
            liquidity_capacity_micros: 25_000_000,
            drawdown_ppm: 0,
        };
        let first = robust_size(&base).unwrap();
        let mut worse = base;
        worse.estimate_uncertainty_ppm = 3_500;
        let second = robust_size(&worse).unwrap();
        assert!(second.notional_micros <= first.notional_micros);
        assert!(second.applied_fraction_ppm <= first.applied_fraction_ppm);
    }

    #[test]
    fn quarter_kelly_is_an_absolute_ceiling() {
        let input = RobustSizingInput {
            bankroll_micros: 1_000_000_000,
            expected_net_return_ppm: 100_000,
            estimate_uncertainty_ppm: 1,
            return_volatility_ppm: 1_000,
            kelly_cap_ppm: QUARTER_KELLY_PPM,
            position_cap_micros: u64::MAX,
            liquidity_capacity_micros: u64::MAX,
            drawdown_ppm: 0,
        };
        assert_eq!(
            robust_size(&input).unwrap().applied_fraction_ppm,
            QUARTER_KELLY_PPM
        );
    }
}
