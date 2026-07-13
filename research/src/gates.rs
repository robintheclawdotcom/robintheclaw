use serde::{Deserialize, Serialize};

const THREE_SIGMA_P_VALUE_PPB: u32 = 1_350_000;
const DSR_MIN_PPM: u32 = 990_000;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum PromotionState {
    Registered,
    Research,
    ShadowEligible,
    Shadow,
    AuditReady,
    CanaryEligible,
    Rejected,
    Retired,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct PromotionEvidence {
    pub hypothesis_registered: bool,
    pub testing_family_registered: bool,
    pub frozen_dataset_verified: bool,
    pub walk_forward_verified: bool,
    pub adjusted_p_value_ppb: u32,
    pub deflated_sharpe_probability_ppm: u32,
    pub bootstrap_net_return_lower_bound_ppm: i64,
    pub canary_capacity_micros: u64,
    pub capacity_curve_bounded: bool,
    pub capture_days: u16,
    pub continuous_shadow_days: u16,
    pub contract_audit_approved: bool,
    pub executor_review_approved: bool,
    pub key_review_approved: bool,
    pub legal_approved: bool,
    pub restore_drill_approved: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum GateFailure {
    Hypothesis,
    TestingFamily,
    Dataset,
    WalkForward,
    AdjustedSignificance,
    DeflatedSharpe,
    NetReturnConfidence,
    Capacity,
    CaptureWindow,
    ShadowWindow,
    ContractAudit,
    ExecutorReview,
    KeyReview,
    LegalApproval,
    RestoreDrill,
}

impl PromotionEvidence {
    pub fn canary_failures(&self) -> Vec<GateFailure> {
        let mut failures = Vec::new();
        let checks = [
            (self.hypothesis_registered, GateFailure::Hypothesis),
            (self.testing_family_registered, GateFailure::TestingFamily),
            (self.frozen_dataset_verified, GateFailure::Dataset),
            (self.walk_forward_verified, GateFailure::WalkForward),
            (
                self.adjusted_p_value_ppb <= THREE_SIGMA_P_VALUE_PPB,
                GateFailure::AdjustedSignificance,
            ),
            (
                self.deflated_sharpe_probability_ppm >= DSR_MIN_PPM,
                GateFailure::DeflatedSharpe,
            ),
            (
                self.bootstrap_net_return_lower_bound_ppm > 0,
                GateFailure::NetReturnConfidence,
            ),
            (
                self.canary_capacity_micros >= 25_000_000 && self.capacity_curve_bounded,
                GateFailure::Capacity,
            ),
            (self.capture_days >= 180, GateFailure::CaptureWindow),
            (self.continuous_shadow_days >= 60, GateFailure::ShadowWindow),
            (self.contract_audit_approved, GateFailure::ContractAudit),
            (self.executor_review_approved, GateFailure::ExecutorReview),
            (self.key_review_approved, GateFailure::KeyReview),
            (self.legal_approved, GateFailure::LegalApproval),
            (self.restore_drill_approved, GateFailure::RestoreDrill),
        ];
        for (passed, failure) in checks {
            if !passed {
                failures.push(failure);
            }
        }
        failures
    }

    pub fn can_promote_to_canary(&self) -> bool {
        self.canary_failures().is_empty()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn approved() -> PromotionEvidence {
        PromotionEvidence {
            hypothesis_registered: true,
            testing_family_registered: true,
            frozen_dataset_verified: true,
            walk_forward_verified: true,
            adjusted_p_value_ppb: 1_350_000,
            deflated_sharpe_probability_ppm: 990_000,
            bootstrap_net_return_lower_bound_ppm: 1,
            canary_capacity_micros: 25_000_000,
            capacity_curve_bounded: true,
            capture_days: 180,
            continuous_shadow_days: 60,
            contract_audit_approved: true,
            executor_review_approved: true,
            key_review_approved: true,
            legal_approved: true,
            restore_drill_approved: true,
        }
    }

    #[test]
    fn complete_evidence_promotes() {
        assert!(approved().can_promote_to_canary());
    }

    #[test]
    fn every_gate_fails_closed() {
        let mut evidence = approved();
        evidence.capture_days = 179;
        evidence.legal_approved = false;
        assert_eq!(
            evidence.canary_failures(),
            vec![GateFailure::CaptureWindow, GateFailure::LegalApproval]
        );
    }

    #[test]
    fn confidence_bound_must_be_strictly_positive() {
        let mut evidence = approved();
        evidence.bootstrap_net_return_lower_bound_ppm = 0;
        assert_eq!(
            evidence.canary_failures(),
            vec![GateFailure::NetReturnConfidence]
        );
    }
}
