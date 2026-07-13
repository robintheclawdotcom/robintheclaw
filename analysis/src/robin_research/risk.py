from dataclasses import dataclass

import numpy as np
from sklearn.covariance import LedoitWolf


@dataclass(frozen=True)
class PortfolioSize:
    weights: np.ndarray
    covariance: np.ndarray
    expected_portfolio_return: float
    expected_portfolio_volatility: float
    gross_fraction: float


def robust_portfolio_size(
    returns: np.ndarray,
    expected_returns: np.ndarray,
    standard_errors: np.ndarray,
    *,
    drawdown_fraction: float,
    max_gross_fraction: float,
    uncertainty_z: float = 1.96,
    kelly_fraction: float = 0.25,
) -> PortfolioSize:
    observations = np.asarray(returns, dtype=np.float64)
    expected = np.asarray(expected_returns, dtype=np.float64)
    errors = np.asarray(standard_errors, dtype=np.float64)
    if observations.ndim != 2 or observations.shape[0] < 100:
        raise ValueError("returns require at least 100 observations")
    if expected.shape != (observations.shape[1],) or errors.shape != expected.shape:
        raise ValueError("expected returns and errors must match the asset count")
    if not np.all(np.isfinite(observations)) or not np.all(np.isfinite(expected)):
        raise ValueError("risk inputs must be finite")
    if not 0 <= drawdown_fraction < 1 or not 0 < max_gross_fraction <= 1:
        raise ValueError("risk fractions are invalid")
    if not 0 < kelly_fraction <= 0.25 or uncertainty_z < 0:
        raise ValueError("Kelly and uncertainty controls are invalid")

    covariance = LedoitWolf().fit(observations).covariance_
    robust_expected = np.maximum(expected - uncertainty_z * errors, 0.0)
    unconstrained = np.linalg.pinv(covariance, hermitian=True) @ robust_expected
    unconstrained = np.maximum(unconstrained, 0.0)
    weights = unconstrained * kelly_fraction * (1.0 - drawdown_fraction)
    gross = float(np.sum(np.abs(weights)))
    if gross > max_gross_fraction:
        weights *= max_gross_fraction / gross
        gross = max_gross_fraction
    expected_portfolio_return = float(weights @ robust_expected)
    variance = float(weights @ covariance @ weights)
    return PortfolioSize(
        weights=weights,
        covariance=covariance,
        expected_portfolio_return=expected_portfolio_return,
        expected_portfolio_volatility=float(np.sqrt(max(variance, 0.0))),
        gross_fraction=gross,
    )
