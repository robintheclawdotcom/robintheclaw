use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Direction {
    /// perp trades above spot: long spot, short perp
    PerpRich,
    /// perp trades below spot: short spot, long perp
    PerpCheap,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BasisObservation {
    pub symbol: String,
    pub spot_price: f64,
    pub perp_mark: f64,
    /// AMM pool liquidity backing the spot price; a thin pool is a stale mark, not a real spread
    pub spot_liquidity: f64,
    pub age_secs: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BasisConfig {
    pub entry_bps: f64,
    pub min_liquidity: f64,
    pub max_age_secs: f64,
}

impl Default for BasisConfig {
    fn default() -> Self {
        Self {
            entry_bps: 30.0,
            min_liquidity: 1e14,
            max_age_secs: 60.0,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BasisSignal {
    pub symbol: String,
    pub spot_price: f64,
    pub perp_mark: f64,
    pub basis_bps: f64,
    pub direction: Direction,
}

pub fn basis_bps(spot: f64, perp: f64) -> f64 {
    if spot <= 0.0 {
        return 0.0;
    }
    (perp - spot) / spot * 10_000.0
}

/// A tradeable basis is fresh, backed by real liquidity, and past the entry threshold. Below the
/// threshold there is nothing to capture net of costs; a stale or thin observation is rejected
/// because its wide basis is a pricing artifact rather than a spread you can close into.
pub fn evaluate(obs: &BasisObservation, cfg: &BasisConfig) -> Option<BasisSignal> {
    if obs.spot_price <= 0.0 || obs.perp_mark <= 0.0 {
        return None;
    }
    if obs.age_secs > cfg.max_age_secs {
        return None;
    }
    if obs.spot_liquidity < cfg.min_liquidity {
        return None;
    }
    let bps = basis_bps(obs.spot_price, obs.perp_mark);
    if bps.abs() < cfg.entry_bps {
        return None;
    }
    let direction = if bps > 0.0 {
        Direction::PerpRich
    } else {
        Direction::PerpCheap
    };
    Some(BasisSignal {
        symbol: obs.symbol.clone(),
        spot_price: obs.spot_price,
        perp_mark: obs.perp_mark,
        basis_bps: bps,
        direction,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn obs(spot: f64, perp: f64) -> BasisObservation {
        BasisObservation {
            symbol: "NVDA".into(),
            spot_price: spot,
            perp_mark: perp,
            spot_liquidity: 1e16,
            age_secs: 5.0,
        }
    }

    #[test]
    fn below_threshold_is_none() {
        let cfg = BasisConfig::default();
        assert!(evaluate(&obs(212.0, 212.1), &cfg).is_none()); // ~5bps < 30
    }

    #[test]
    fn perp_rich_signal() {
        let cfg = BasisConfig::default();
        let s = evaluate(&obs(212.0, 214.0), &cfg).unwrap(); // ~94bps
        assert_eq!(s.direction, Direction::PerpRich);
        assert!(s.basis_bps > 0.0);
    }

    #[test]
    fn perp_cheap_signal() {
        let cfg = BasisConfig::default();
        let s = evaluate(&obs(214.0, 212.0), &cfg).unwrap();
        assert_eq!(s.direction, Direction::PerpCheap);
        assert!(s.basis_bps < 0.0);
    }

    #[test]
    fn stale_is_rejected() {
        let cfg = BasisConfig::default();
        let mut o = obs(212.0, 214.0);
        o.age_secs = 120.0;
        assert!(evaluate(&o, &cfg).is_none());
    }

    #[test]
    fn thin_pool_is_rejected() {
        let cfg = BasisConfig::default();
        let mut o = obs(212.0, 214.0);
        o.spot_liquidity = 1e12;
        assert!(evaluate(&o, &cfg).is_none());
    }
}
