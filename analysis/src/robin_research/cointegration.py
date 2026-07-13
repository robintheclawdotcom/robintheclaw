from dataclasses import dataclass

import numpy as np
from statsmodels.tsa.stattools import adfuller


@dataclass(frozen=True)
class CointegrationResult:
    intercept: float
    hedge_ratio: float
    adf_statistic: float
    adf_p_value: float
    ou_half_life: float | None
    stationary: bool


def fit_cointegration(
    spot_log_prices: np.ndarray,
    perp_log_prices: np.ndarray,
    *,
    significance: float = 0.05,
) -> CointegrationResult:
    spot = _series(spot_log_prices)
    perp = _series(perp_log_prices)
    if spot.size != perp.size or spot.size < 100:
        raise ValueError("aligned series require at least 100 observations")
    if not 0 < significance < 1:
        raise ValueError("significance must be between zero and one")

    design = np.column_stack((np.ones(spot.size), spot))
    intercept, hedge_ratio = np.linalg.lstsq(design, perp, rcond=None)[0]
    residual = perp - intercept - hedge_ratio * spot
    adf_statistic, adf_p_value, *_ = adfuller(residual, regression="c", autolag="AIC")
    half_life = _ou_half_life(residual)
    stationary = bool(adf_p_value <= significance and half_life is not None)
    return CointegrationResult(
        intercept=float(intercept),
        hedge_ratio=float(hedge_ratio),
        adf_statistic=float(adf_statistic),
        adf_p_value=float(adf_p_value),
        ou_half_life=half_life,
        stationary=stationary,
    )


def _ou_half_life(residual: np.ndarray) -> float | None:
    lagged = residual[:-1]
    delta = np.diff(residual)
    design = np.column_stack((np.ones(lagged.size), lagged))
    slope = float(np.linalg.lstsq(design, delta, rcond=None)[0][1])
    if slope >= 0:
        return None
    half_life = -np.log(2.0) / slope
    if not np.isfinite(half_life) or half_life <= 0:
        return None
    return float(half_life)


def _series(value: np.ndarray) -> np.ndarray:
    series = np.asarray(value, dtype=np.float64)
    if series.ndim != 1 or not np.all(np.isfinite(series)):
        raise ValueError("price series must be a finite vector")
    return series
