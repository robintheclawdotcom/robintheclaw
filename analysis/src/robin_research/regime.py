from dataclasses import dataclass
from enum import StrEnum

import numpy as np
from hmmlearn.hmm import GaussianHMM
from sklearn.preprocessing import StandardScaler


class Regime(StrEnum):
    NORMAL = "normal"
    ILLIQUID = "illiquid"
    DISLOCATED = "dislocated"
    UNKNOWN = "unknown"


@dataclass
class RegimeModel:
    posterior_floor: float = 0.70
    random_state: int = 7

    def __post_init__(self) -> None:
        if not 0.5 < self.posterior_floor < 1:
            raise ValueError("posterior floor must be between 0.5 and 1")
        self._scaler = StandardScaler()
        self._model = GaussianHMM(
            n_components=3,
            covariance_type="full",
            n_iter=500,
            random_state=self.random_state,
            min_covar=1e-5,
        )
        self._labels: dict[int, Regime] = {}
        self._fitted = False

    def fit(self, features: np.ndarray) -> "RegimeModel":
        values = _features(features)
        if values.shape[0] < 500:
            raise ValueError("regime training requires at least 500 observations")
        scaled = self._scaler.fit_transform(values)
        states = self._model.fit(scaled).predict(scaled)
        self._labels = _label_states(values, states)
        self._fitted = True
        return self

    def predict(self, features: np.ndarray) -> list[Regime]:
        if not self._fitted:
            raise RuntimeError("regime model is not fitted")
        values = _features(features)
        posterior = self._model.predict_proba(self._scaler.transform(values))
        result: list[Regime] = []
        for probabilities in posterior:
            state = int(np.argmax(probabilities))
            if float(probabilities[state]) < self.posterior_floor:
                result.append(Regime.UNKNOWN)
            else:
                result.append(self._labels.get(state, Regime.UNKNOWN))
        return result


def _label_states(features: np.ndarray, states: np.ndarray) -> dict[int, Regime]:
    liquidity = features[:, 0]
    dislocation = np.abs(features[:, 1])
    medians = {
        state: (
            float(np.median(liquidity[states == state])),
            float(np.median(dislocation[states == state])),
        )
        for state in np.unique(states)
    }
    illiquid = min(medians, key=lambda state: medians[state][0])
    dislocated = max(
        (state for state in medians if state != illiquid),
        key=lambda state: medians[state][1],
    )
    return {
        int(state): (
            Regime.ILLIQUID
            if state == illiquid
            else Regime.DISLOCATED
            if state == dislocated
            else Regime.NORMAL
        )
        for state in medians
    }


def _features(value: np.ndarray) -> np.ndarray:
    features = np.asarray(value, dtype=np.float64)
    if features.ndim != 2 or features.shape[1] < 3 or not np.all(np.isfinite(features)):
        raise ValueError("features must be a finite matrix with at least three columns")
    return features
