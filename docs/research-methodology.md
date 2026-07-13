# Edge research methodology

## Objective

Robin the Claw is being built to find and operate durable, risk-adjusted net-profit opportunities
in tokenized-asset spot/perpetual basis and funding. The return objective is explicit: a strategy
must demonstrate positive performance after fees, funding, gas, impact, latency, failed hedges,
and capital constraints.

The methodology does not assume that a measured spread is an opportunity. It treats a candidate as
unproven until it survives the model, execution, and validation gates below.

## Research principles

- **Small, repeated edges over concentrated bets.** The portfolio should prefer many sufficiently
  independent opportunities over a small number of large positions.
- **Mechanism before scale.** A candidate needs a plausible structural source: oracle cadence,
  redemption mechanics, market closures, liquidity fragmentation, predictable flow, or a
  cross-venue microstructure effect.
- **Net economics, not displayed spread.** Every candidate is evaluated against the full cost of
  entering, hedging, maintaining, and unwinding both legs.
- **Evidence before capital.** An unexplained anomaly can be researched with a small shadow
  allocation. It cannot graduate into live capital merely because a backtest looks attractive.
- **Portfolio context is mandatory.** Position sizing, concentration, factor exposure, and
  correlated failure modes are evaluated across the book, not trade by trade.

## Fast decision loop

The execution-time loop is private, deterministic, and deliberately narrow. It consumes
venue-native events and produces a trade, hedge, decline, or unwind decision. An LLM is not in
this path and has no signing access.

### Spread and convergence models

For each registered pair, the research pipeline tests cointegration between realizable spot value
and the matched perpetual. A rolling Ornstein-Uhlenbeck residual model estimates deviation,
half-life, and expected convergence. A Kalman filter may adapt the hedge ratio where the static
relationship drifts.

A candidate is rejected when stationarity, convergence, capacity, or net economics cannot be
demonstrated out of sample. The model must use executable bid/ask and depth, not mid-price spreads.

### Regime veto

A hidden-Markov model classifies the market as `normal`, `illiquid`, `dislocated`, or `unknown`
from liquidity, volatility, spread, funding, oracle freshness, sequencer health, and event-flow
features. The classifier is a veto, not a source of leverage: `unknown`, stale, or dislocated
conditions decline new risk and can trigger an unwind.

### Portfolio and sizing model

The fast loop uses robust fractional Kelly only after a positive lower-confidence net-return
estimate. It is constrained by shrinkage covariance, concentration caps, factor limits, gross and
net exposure limits, liquidity capacity, and drawdown state. Quarter-Kelly is a ceiling, not a
default allocation.

## RWA-specific inputs

The private event store is designed to compound into a proprietary dataset. Required sources are:

- L2 order-book deltas, trades, mark/index prices, funding, open interest, and source health.
- Chain blocks, reorg state, gas, pool state, sequencer events, and oracle updates.
- Tokenized-asset NAV, issuer disclosures, redemption and settlement windows, market calendars,
  yield curves, and corporate actions where applicable.
- Cross-chain and cross-venue price, liquidity, bridge, and settlement state.
- Wallet and flow features only when their provenance, privacy, and predictive value are reviewed.

Sub-second collection is used where a venue publishes meaningful sub-second events. The strategy
does not manufacture frequency with polling: a daily NAV update or a redemption window is an
economically relevant event even when it occurs far slower than an order book.

## Adversarial execution research

On-chain arbitrage is an adversarial market. The model must account for mempool visibility, builder
and sequencer ordering, gas auctions, competing routes, private order flow, AMM curvature, and
failed or partial hedges. A spread that cannot survive the expected auction and routing outcome is
not an edge.

Private routes and colocated infrastructure may be evaluated only after their venue semantics,
counterparty risks, and operational controls are documented. No routing mechanism may bypass the
typed execution intent and its asset, route, recipient, maximum-input, minimum-output, deadline,
and slippage constraints.

## Slow research loop

Large models belong in the private slow loop. They can parse issuer documents, normalize event
schemas, generate candidate hypotheses, inspect market structure, propose tests, and perform
post-trade forensics. They cannot sign, submit, amend, or cancel orders and cannot alter a live
threshold.

The slow loop submits a versioned hypothesis to the research registry. It then follows a fixed
path: data snapshot, embargoed backtest, multiple-testing adjustment, walk-forward evaluation,
shadow execution, capacity analysis, and human review. Continuous improvement means improving this
pipeline and retiring decayed signals, not allowing an LLM to learn directly from live capital.

## Statistical hygiene

RWA histories are short and sparse. The research process therefore requires:

1. A registered hypothesis and parameters before evaluation.
2. Immutable source data and a versioned dataset manifest.
3. Embargoed walk-forward periods and multiple-testing adjustment.
4. Positive lower-confidence net return, not only a favorable point estimate.
5. Stability across off-hours, weekends, volatile periods, and low-liquidity regimes.
6. Capacity curves and stress tests for stale feeds, reorgs, depegs, redemption failures, slippage,
   rejected hedges, margin deterioration, and emergency unwind.
7. Ongoing decay monitoring with automatic retirement or size reduction when the evidence weakens.

## Implementation status

| Capability | Status | Promotion condition |
| --- | --- | --- |
| Private Lighter and Robinhood Chain capture | Implemented | R2-backed worker running continuously. |
| Immutable raw archive and normalized event schema | Implemented | Archive credentials and worker deployment configured. |
| Spot executable quote adapter and NAV/redemption feeds | Planned | Venue schemas, freshness rules, and reconciliation tests. |
| Cointegration, OU, and Kalman research models | Planned | Frozen datasets and out-of-sample calibration. |
| HMM regime veto | Planned | Regime labels, confusion analysis, and stale/unknown fail-closed tests. |
| Shrinkage covariance and portfolio-factor limits | Planned | Portfolio simulator and capacity tests. |
| Adversarial routing and private-order-flow analysis | Planned | Venue-specific economics and no-leak operational review. |
| LLM slow research loop | Planned | Isolated credentials, immutable experiment logs, and no execution authority. |
| Live execution | Blocked | All venue, research, contract-audit, and operational gates pass. |

The methodology is a build specification, not evidence that any model is currently profitable.
Only complete, versioned results can establish that claim.
