from dataclasses import dataclass


@dataclass(frozen=True)
class CostModel:
    spot_fee_bps: float
    perp_fee_bps: float
    spot_impact_bps: float
    perp_impact_bps: float
    gas_usd: float
    repair_reserve_bps: float

    def validate(self) -> None:
        values = (
            self.spot_fee_bps,
            self.perp_fee_bps,
            self.spot_impact_bps,
            self.perp_impact_bps,
            self.gas_usd,
            self.repair_reserve_bps,
        )
        if any(value < 0 or not float(value) < float("inf") for value in values):
            raise ValueError("cost model must contain finite non-negative values")


@dataclass(frozen=True)
class NetCostObservation:
    entry_basis_bps: float
    exit_basis_bps: float
    funding_bps: float
    per_leg_notional_usd: float

    def validate(self) -> None:
        if self.per_leg_notional_usd <= 0:
            raise ValueError("notional must be positive")
        values = (self.entry_basis_bps, self.exit_basis_bps, self.funding_bps)
        if any(abs(value) == float("inf") or value != value for value in values):
            raise ValueError("observation must be finite")


def net_return(observation: NetCostObservation, costs: CostModel) -> float:
    observation.validate()
    costs.validate()
    convergence_bps = observation.entry_basis_bps - observation.exit_basis_bps
    trading_bps = (
        costs.spot_fee_bps
        + costs.perp_fee_bps
        + costs.spot_impact_bps
        + costs.perp_impact_bps
        + costs.repair_reserve_bps
    )
    gas_bps = costs.gas_usd / observation.per_leg_notional_usd * 10_000
    return (convergence_bps + observation.funding_bps - trading_bps - gas_bps) / 10_000
