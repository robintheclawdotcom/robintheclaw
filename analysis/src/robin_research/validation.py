from dataclasses import dataclass

import numpy as np
from scipy.stats import kurtosis, norm, skew


@dataclass(frozen=True)
class WalkForwardFold:
    train: slice
    test: slice


@dataclass(frozen=True)
class ValidationEvidence:
    adjusted_p_value: float
    deflated_sharpe_probability: float
    net_return_lower_bound: float
    observation_count: int

    @property
    def passes(self) -> bool:
        return (
            self.adjusted_p_value <= 0.00135
            and self.deflated_sharpe_probability >= 0.99
            and self.net_return_lower_bound > 0
        )


def purged_walk_forward(
    observation_count: int,
    *,
    train_size: int,
    test_size: int,
    embargo_size: int,
) -> list[WalkForwardFold]:
    if min(observation_count, train_size, test_size) <= 0 or embargo_size < 0:
        raise ValueError("walk-forward sizes are invalid")
    folds: list[WalkForwardFold] = []
    test_start = train_size + embargo_size
    while test_start + test_size <= observation_count:
        train_end = test_start - embargo_size
        train_start = max(0, train_end - train_size)
        folds.append(
            WalkForwardFold(
                train=slice(train_start, train_end),
                test=slice(test_start, test_start + test_size),
            )
        )
        test_start += test_size
    if not folds:
        raise ValueError("no complete walk-forward fold fits the dataset")
    return folds


def block_bootstrap_lower_bound(
    episode_returns: np.ndarray,
    *,
    block_size: int,
    samples: int = 10_000,
    confidence: float = 0.95,
    seed: int = 7,
) -> float:
    returns = _returns(episode_returns)
    if not 1 <= block_size <= returns.size or samples < 1_000 or not 0 < confidence < 1:
        raise ValueError("bootstrap parameters are invalid")
    rng = np.random.default_rng(seed)
    starts = np.arange(0, returns.size - block_size + 1)
    blocks_needed = int(np.ceil(returns.size / block_size))
    means = np.empty(samples)
    for index in range(samples):
        selected = rng.choice(starts, size=blocks_needed, replace=True)
        sample = np.concatenate([returns[start : start + block_size] for start in selected])
        means[index] = np.mean(sample[: returns.size])
    return float(np.quantile(means, 1.0 - confidence))


def deflated_sharpe_probability(
    episode_returns: np.ndarray,
    *,
    tested_strategies: int,
    benchmark_sharpe: float = 0.0,
) -> float:
    returns = _returns(episode_returns)
    if tested_strategies < 1:
        raise ValueError("tested strategies must be positive")
    volatility = float(np.std(returns, ddof=1))
    if volatility <= 0:
        return 0.0
    sharpe = float(np.mean(returns) / volatility)
    expected_max = norm.ppf(1.0 - 1.0 / max(tested_strategies, 2))
    adjusted_benchmark = max(benchmark_sharpe, expected_max / np.sqrt(returns.size))
    sample_skew = float(skew(returns, bias=False))
    sample_kurtosis = float(kurtosis(returns, fisher=False, bias=False))
    denominator = np.sqrt(
        max(
            (
                1.0
                - sample_skew * sharpe
                + (sample_kurtosis - 1.0) * sharpe**2 / 4.0
            )
            / (returns.size - 1),
            np.finfo(float).eps,
        )
    )
    return float(norm.cdf((sharpe - adjusted_benchmark) / denominator))


def adjusted_p_value(raw_p_value: float, tested_strategies: int) -> float:
    if not 0 <= raw_p_value <= 1 or tested_strategies < 1:
        raise ValueError("p-value adjustment inputs are invalid")
    return min(1.0, raw_p_value * tested_strategies)


def _returns(value: np.ndarray) -> np.ndarray:
    returns = np.asarray(value, dtype=np.float64)
    if returns.ndim != 1 or returns.size < 30 or not np.all(np.isfinite(returns)):
        raise ValueError("episode returns must be a finite vector with at least 30 values")
    return returns
