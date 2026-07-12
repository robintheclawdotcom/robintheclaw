use crate::basis::{BasisSignal, Direction};
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Venue {
    Spot,
    Perp,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Side {
    Long,
    Short,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Leg {
    pub venue: Venue,
    pub side: Side,
    pub qty: f64,
    pub price: f64,
    pub notional_usd: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NeutralPlan {
    pub symbol: String,
    pub spot: Leg,
    pub perp: Leg,
    pub gross_notional_usd: f64,
    pub net_delta_usd: f64,
}

/// Build the two matched legs of a delta-neutral basis trade. Share quantity is matched across
/// legs (not notional), so a move in the underlying cancels and the captured edge is the basis on
/// those shares. Perp rich (basis > 0) means long spot / short perp; perp cheap means the reverse.
pub fn build(signal: &BasisSignal, per_leg_notional_usd: f64) -> Option<NeutralPlan> {
    if !signal.spot_price.is_finite()
        || !signal.perp_mark.is_finite()
        || !per_leg_notional_usd.is_finite()
        || signal.spot_price <= 0.0
        || signal.perp_mark <= 0.0
        || per_leg_notional_usd <= 0.0
    {
        return None;
    }

    let qty = per_leg_notional_usd / signal.spot_price;
    let (spot_side, perp_side) = match signal.direction {
        Direction::PerpRich => (Side::Long, Side::Short),
        Direction::PerpCheap => (Side::Short, Side::Long),
    };

    let spot = Leg {
        venue: Venue::Spot,
        side: spot_side,
        qty,
        price: signal.spot_price,
        notional_usd: qty * signal.spot_price,
    };
    let perp = Leg {
        venue: Venue::Perp,
        side: perp_side,
        qty,
        price: signal.perp_mark,
        notional_usd: qty * signal.perp_mark,
    };

    // Delta is valued on the underlying (use spot as the reference price for both legs); matched
    // share quantity with opposite signs nets the directional exposure to zero.
    let signed = |side: Side| if side == Side::Long { qty } else { -qty };
    let net_delta_usd = (signed(spot.side) + signed(perp.side)) * signal.spot_price;

    Some(NeutralPlan {
        symbol: signal.symbol.clone(),
        gross_notional_usd: spot.notional_usd + perp.notional_usd,
        net_delta_usd,
        spot,
        perp,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::basis::{BasisSignal, Direction};

    fn sig(dir: Direction, spot: f64, perp: f64) -> BasisSignal {
        BasisSignal {
            symbol: "NVDA".into(),
            spot_price: spot,
            perp_mark: perp,
            basis_bps: crate::basis::basis_bps(spot, perp),
            direction: dir,
        }
    }

    #[test]
    fn perp_rich_is_long_spot_short_perp() {
        let p = build(&sig(Direction::PerpRich, 212.0, 214.0), 1_000.0).unwrap();
        assert_eq!(p.spot.side, Side::Long);
        assert_eq!(p.perp.side, Side::Short);
    }

    #[test]
    fn perp_cheap_is_short_spot_long_perp() {
        let p = build(&sig(Direction::PerpCheap, 214.0, 212.0), 1_000.0).unwrap();
        assert_eq!(p.spot.side, Side::Short);
        assert_eq!(p.perp.side, Side::Long);
    }

    #[test]
    fn matched_qty_is_delta_neutral() {
        let p = build(&sig(Direction::PerpRich, 212.0, 214.0), 1_000.0).unwrap();
        assert!((p.spot.qty - p.perp.qty).abs() < 1e-9);
        assert!(p.net_delta_usd.abs() < 1e-6);
    }

    #[test]
    fn per_leg_notional_matches() {
        let p = build(&sig(Direction::PerpRich, 200.0, 202.0), 1_000.0).unwrap();
        assert!((p.spot.notional_usd - 1_000.0).abs() < 1e-6);
    }

    #[test]
    fn zero_size_is_none() {
        assert!(build(&sig(Direction::PerpRich, 200.0, 202.0), 0.0).is_none());
    }

    #[test]
    fn non_finite_size_is_none() {
        assert!(build(&sig(Direction::PerpRich, 200.0, 202.0), f64::NAN).is_none());
    }
}
