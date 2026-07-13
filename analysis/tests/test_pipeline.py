import numpy as np

from robin_research.artifact import freeze_artifact
from robin_research.baseline import CostModel, NetCostObservation, net_return
from robin_research.cointegration import fit_cointegration
from robin_research.regime import Regime, RegimeModel
from robin_research.risk import robust_portfolio_size
from robin_research.validation import (
    adjusted_p_value,
    block_bootstrap_lower_bound,
    deflated_sharpe_probability,
    purged_walk_forward,
)


def test_net_cost_baseline_includes_gas_and_repair() -> None:
    observation = NetCostObservation(80, 20, 5, 1_000)
    costs = CostModel(1, 2, 3, 4, 1, 5)
    assert np.isclose(net_return(observation, costs), 0.004)


def test_cointegration_recovers_stationary_residual() -> None:
    rng = np.random.default_rng(7)
    spot = np.cumsum(rng.normal(0, 0.01, 2_000))
    residual = np.zeros(spot.size)
    for index in range(1, residual.size):
        residual[index] = 0.8 * residual[index - 1] + rng.normal(0, 0.002)
    perp = 0.05 + 1.02 * spot + residual
    result = fit_cointegration(spot, perp)
    assert result.stationary
    assert abs(result.hedge_ratio - 1.02) < 0.02
    assert result.ou_half_life is not None


def test_robust_portfolio_size_shrinks_uncertain_edge() -> None:
    rng = np.random.default_rng(9)
    returns = rng.multivariate_normal(
        [0, 0],
        [[0.0004, 0.0002], [0.0002, 0.0005]],
        size=1_000,
    )
    base = robust_portfolio_size(
        returns,
        np.array([0.002, 0.0015]),
        np.array([0.0001, 0.0001]),
        drawdown_fraction=0,
        max_gross_fraction=0.1,
    )
    uncertain = robust_portfolio_size(
        returns,
        np.array([0.002, 0.0015]),
        np.array([0.001, 0.001]),
        drawdown_fraction=0,
        max_gross_fraction=0.1,
    )
    assert uncertain.gross_fraction <= base.gross_fraction


def test_walk_forward_embargo_and_block_bootstrap() -> None:
    folds = purged_walk_forward(1_000, train_size=400, test_size=100, embargo_size=20)
    assert folds[0].train.stop + 20 == folds[0].test.start
    returns = np.full(100, 0.001)
    assert block_bootstrap_lower_bound(returns, block_size=5, samples=1_000) > 0
    assert adjusted_p_value(0.0001, 10) == 0.001


def test_regime_model_labels_clear_states() -> None:
    rng = np.random.default_rng(11)
    normal = rng.normal([100, 0, 1], [2, 0.2, 0.1], size=(200, 3))
    illiquid = rng.normal([5, 1, 3], [0.5, 0.2, 0.2], size=(200, 3))
    dislocated = rng.normal([70, 10, 5], [2, 0.5, 0.2], size=(200, 3))
    features = np.vstack((normal, illiquid, dislocated))
    predictions = RegimeModel(posterior_floor=0.6).fit(features).predict(features)
    assert set(predictions) <= {Regime.NORMAL, Regime.ILLIQUID, Regime.DISLOCATED, Regime.UNKNOWN}
    assert Regime.ILLIQUID in predictions
    assert Regime.DISLOCATED in predictions


def test_deflated_sharpe_penalizes_multiple_trials() -> None:
    rng = np.random.default_rng(13)
    returns = rng.normal(0.002, 0.01, size=500)
    few_trials = deflated_sharpe_probability(returns, tested_strategies=2)
    many_trials = deflated_sharpe_probability(returns, tested_strategies=1_000)
    assert many_trials <= few_trials


def test_artifact_freeze_is_deterministic() -> None:
    payload = {
        "schema_version": 1,
        "model_name": "baseline",
        "dataset_manifest_sha256": "a" * 64,
        "code_commit": "abc123",
        "feature_schema_sha256": "b" * 64,
        "cost_model_version": "v1",
        "training_start_ms": 1,
        "training_end_ms": 2,
        "embargo_end_ms": 3,
        "parameters": {"fee_bps": 2},
    }
    assert freeze_artifact(payload).sha256 == freeze_artifact(payload).sha256
