//! Proof of PnL: prove that an agent's net return over a committed set of trades cleared a
//! public threshold, without revealing any individual trade.
//!
//! Private: per-trade net PnL and notional (scaled integers), the trade count, and a blinding.
//! Public inputs: the agent identity, the claimed minimum net return in basis points, and the
//! number of trades the claim covers. Public output: a Poseidon commitment binding the private
//! trades to this proof.
//!
//! The circuit proves the aggregate net return in basis points over the committed trades is at
//! least the threshold, and returns the commitment so the prover cannot substitute a different
//! dataset after the fact. Binding that commitment to the agent's complete on-chain anchored
//! record is an integration step performed outside this circuit; see the zk README.
//!
//! Signed PnL is carried as an unsigned value biased by `PNL_BIAS` so the commitment hashes clean
//! field elements and the off-chain prover reproduces the encoding with a single addition.

use poseidon::poseidon2::Poseidon2;

global MAX_TRADES: u32 = 32;
global PREIMAGE_LEN: u32 = 2 * MAX_TRADES + 3;
global BPS_DENOMINATOR: i64 = 10000;

// Per-trade magnitude bound. Biasing a trade's signed PnL by this keeps the biased value in
// [0, 2*PNL_BIAS] and every intermediate product inside i64:
//   |total_pnl| <= MAX_TRADES * PNL_BIAS = 3.2e13, * 10000 = 3.2e17 < i64::MAX (9.2e18).
global PNL_BIAS: i64 = 1_000_000_000_000;
global MAX_NOTIONAL: u64 = 1_000_000_000_000;
// Bounds threshold_bps so threshold_bps * total_notional (<= 2e5 * 3.2e13 = 6.4e18) stays in i64.
global MAX_THRESHOLD_BPS: i64 = 200_000;

fn main(
    net_pnl: [i64; MAX_TRADES],
    notional: [u64; MAX_TRADES],
    count: u32,
    blinding: Field,
    agent_id: pub Field,
    threshold_bps: pub i64,
    trade_count: pub u32,
) -> pub Field {
    assert(count == trade_count, "count mismatch");
    assert(count > 0, "no trades");
    assert(count <= MAX_TRADES, "too many trades");
    assert(
        (threshold_bps <= MAX_THRESHOLD_BPS) & (threshold_bps >= -MAX_THRESHOLD_BPS),
        "threshold out of range",
    );

    let mut total_pnl: i64 = 0;
    let mut total_notional: i64 = 0;

    for i in 0..MAX_TRADES {
        let active = i < count;
        let pnl = net_pnl[i];
        let notl = notional[i];

        assert((pnl <= PNL_BIAS) & (pnl >= -PNL_BIAS), "pnl out of range");
        assert(notl <= MAX_NOTIONAL, "notional out of range");

        if active {
            assert(notl > 0, "active trade needs positive notional");
        } else {
            // Padded slots must be exactly zero so the commitment over the fixed-size arrays is
            // unambiguous and cannot smuggle hidden value past the count.
            assert(pnl == 0, "padding pnl must be zero");
            assert(notl == 0, "padding notional must be zero");
        }

        total_pnl += if active { pnl } else { 0 };
        total_notional += if active { notl as i64 } else { 0 };
    }

    assert(total_notional > 0, "no notional");

    // net_return_bps >= threshold_bps, proven without division:
    //   total_pnl / total_notional * 10000 >= threshold_bps
    //   <=> total_pnl * 10000 >= threshold_bps * total_notional   (total_notional > 0)
    let lhs = total_pnl * BPS_DENOMINATOR;
    let rhs = threshold_bps * total_notional;
    assert(lhs >= rhs, "below threshold");

    commitment_hash(agent_id, count, blinding, net_pnl, notional)
}

/// Poseidon commitment over the agent id, trade count, blinding, and the full fixed-size PnL and
/// notional arrays. PnL is biased into the non-negative range before hashing so the encoding is a
/// plain field element; the off-chain prover reproduces it by adding `PNL_BIAS`.
pub fn commitment_hash(
    agent_id: Field,
    count: u32,
    blinding: Field,
    net_pnl: [i64; MAX_TRADES],
    notional: [u64; MAX_TRADES],
) -> Field {
    let mut preimage: [Field; PREIMAGE_LEN] = [0; PREIMAGE_LEN];
    preimage[0] = agent_id;
    preimage[1] = count as Field;
    preimage[2] = blinding;
    for i in 0..MAX_TRADES {
        let biased = (net_pnl[i] + PNL_BIAS) as u64;
        preimage[3 + i] = biased as Field;
        preimage[3 + MAX_TRADES + i] = notional[i] as Field;
    }
    Poseidon2::hash(preimage, PREIMAGE_LEN)
}

#[test]
fn meets_threshold() {
    let mut net_pnl: [i64; MAX_TRADES] = [0; MAX_TRADES];
    let mut notional: [u64; MAX_TRADES] = [0; MAX_TRADES];
    net_pnl[0] = 30;
    net_pnl[1] = -10;
    net_pnl[2] = 25;
    notional[0] = 1000;
    notional[1] = 1000;
    notional[2] = 1000;
    let count: u32 = 3;
    // total_pnl 45 over notional 3000 = 150 bps; claim >= 100 bps.
    let out = main(net_pnl, notional, count, 0x1234, 0xabcd, 100, count);
    let expected = commitment_hash(0xabcd, count, 0x1234, net_pnl, notional);
    assert(out == expected, "commitment output mismatch");
}

#[test(should_fail_with = "below threshold")]
fn below_threshold() {
    let mut net_pnl: [i64; MAX_TRADES] = [0; MAX_TRADES];
    let mut notional: [u64; MAX_TRADES] = [0; MAX_TRADES];
    net_pnl[0] = 30;
    net_pnl[1] = -10;
    net_pnl[2] = 25;
    notional[0] = 1000;
    notional[1] = 1000;
    notional[2] = 1000;
    // 150 bps actual, claim >= 200 bps must fail.
    let _ = main(net_pnl, notional, 3, 0x1234, 0xabcd, 200, 3);
}

#[test]
fn negative_threshold_bounds_loss() {
    let mut net_pnl: [i64; MAX_TRADES] = [0; MAX_TRADES];
    let mut notional: [u64; MAX_TRADES] = [0; MAX_TRADES];
    net_pnl[0] = -5;
    net_pnl[1] = -3;
    notional[0] = 1000;
    notional[1] = 1000;
    // -8 over 2000 = -40 bps; claim "lost no more than 100 bps" (>= -100) passes.
    let _ = main(net_pnl, notional, 2, 0x55, 0x66, -100, 2);
}

#[test(should_fail_with = "padding pnl must be zero")]
fn nonzero_padding_rejected() {
    let mut net_pnl: [i64; MAX_TRADES] = [0; MAX_TRADES];
    let mut notional: [u64; MAX_TRADES] = [0; MAX_TRADES];
    net_pnl[0] = 50;
    notional[0] = 1000;
    net_pnl[5] = 100; // hidden value in a padded slot
    let _ = main(net_pnl, notional, 1, 0x1, 0x2, 100, 1);
}

#[test(should_fail_with = "count mismatch")]
fn count_must_match_public() {
    let mut net_pnl: [i64; MAX_TRADES] = [0; MAX_TRADES];
    let mut notional: [u64; MAX_TRADES] = [0; MAX_TRADES];
    net_pnl[0] = 50;
    notional[0] = 1000;
    // public trade_count (2) disagrees with the private count (1).
    let _ = main(net_pnl, notional, 1, 0x1, 0x2, 100, 2);
}

#[test(should_fail_with = "active trade needs positive notional")]
fn active_trade_needs_notional() {
    let mut net_pnl: [i64; MAX_TRADES] = [0; MAX_TRADES];
    let mut notional: [u64; MAX_TRADES] = [0; MAX_TRADES];
    net_pnl[0] = 50;
    notional[0] = 0; // active slot with zero notional
    let _ = main(net_pnl, notional, 1, 0x1, 0x2, 100, 1);
}
