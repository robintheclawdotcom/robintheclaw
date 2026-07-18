use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

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

impl PromotionState {
    pub fn can_transition_to(self, next: Self) -> bool {
        matches!(
            (self, next),
            (Self::Registered, Self::Research)
                | (Self::Research, Self::ShadowEligible)
                | (Self::ShadowEligible, Self::Shadow)
                | (Self::Shadow, Self::AuditReady)
                | (
                    Self::Registered
                        | Self::Research
                        | Self::ShadowEligible
                        | Self::Shadow
                        | Self::AuditReady,
                    Self::CanaryEligible
                )
                | (
                    Self::Registered
                        | Self::Research
                        | Self::ShadowEligible
                        | Self::Shadow
                        | Self::AuditReady,
                    Self::Rejected | Self::Retired
                )
                | (Self::CanaryEligible, Self::Retired)
        )
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "approval_type", rename_all = "snake_case", deny_unknown_fields)]
pub enum PromotionEvidence {
    Research {
        hypothesis_registered: bool,
        testing_family_registered: bool,
        frozen_dataset_verified: bool,
        walk_forward_verified: bool,
        adjusted_p_value_ppb: u32,
        deflated_sharpe_probability_ppm: u32,
        bootstrap_net_return_lower_bound_ppm: i64,
        canary_capacity_micros: u64,
        capacity_curve_bounded: bool,
        internal_audit_approved: bool,
        executor_review_approved: bool,
        key_review_approved: bool,
        restore_drill_approved: bool,
    },
    EngineeringCanary {
        max_accounts: u16,
        max_leg_notional_micros: u64,
        max_gross_notional_micros: u64,
        max_daily_turnover_micros: u64,
        max_leverage_ppm: u32,
        internal_audit_sha256: String,
        internal_audit_approved: bool,
        executor_review_approved: bool,
        key_review_approved: bool,
        restore_drill_approved: bool,
    },
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
    InternalAudit,
    ExecutorReview,
    KeyReview,
    RestoreDrill,
    EngineeringCanaryScope,
    InternalAuditDigest,
}

impl PromotionEvidence {
    pub fn calculate_hash(&self) -> String {
        let mut hasher = Sha256::new();
        match self {
            Self::Research {
                hypothesis_registered,
                testing_family_registered,
                frozen_dataset_verified,
                walk_forward_verified,
                adjusted_p_value_ppb,
                deflated_sharpe_probability_ppm,
                bootstrap_net_return_lower_bound_ppm,
                canary_capacity_micros,
                capacity_curve_bounded,
                internal_audit_approved,
                executor_review_approved,
                key_review_approved,
                restore_drill_approved,
            } => {
                hasher.update([
                    *hypothesis_registered as u8,
                    *testing_family_registered as u8,
                    *frozen_dataset_verified as u8,
                    *walk_forward_verified as u8,
                ]);
                hasher.update(adjusted_p_value_ppb.to_be_bytes());
                hasher.update(deflated_sharpe_probability_ppm.to_be_bytes());
                hasher.update(bootstrap_net_return_lower_bound_ppm.to_be_bytes());
                hasher.update(canary_capacity_micros.to_be_bytes());
                hasher.update([*capacity_curve_bounded as u8]);
                hasher.update([
                    *internal_audit_approved as u8,
                    *executor_review_approved as u8,
                    *key_review_approved as u8,
                    *restore_drill_approved as u8,
                ]);
            }
            Self::EngineeringCanary {
                max_accounts,
                max_leg_notional_micros,
                max_gross_notional_micros,
                max_daily_turnover_micros,
                max_leverage_ppm,
                internal_audit_sha256,
                internal_audit_approved,
                executor_review_approved,
                key_review_approved,
                restore_drill_approved,
            } => {
                hasher.update(b"engineering-canary-v1");
                hasher.update(max_accounts.to_be_bytes());
                hasher.update(max_leg_notional_micros.to_be_bytes());
                hasher.update(max_gross_notional_micros.to_be_bytes());
                hasher.update(max_daily_turnover_micros.to_be_bytes());
                hasher.update(max_leverage_ppm.to_be_bytes());
                hasher.update(internal_audit_sha256.as_bytes());
                hasher.update([
                    *internal_audit_approved as u8,
                    *executor_review_approved as u8,
                    *key_review_approved as u8,
                    *restore_drill_approved as u8,
                ]);
            }
        }
        hex::encode(hasher.finalize())
    }

    pub fn canary_failures(&self) -> Vec<GateFailure> {
        let mut failures = Vec::new();
        match self {
            Self::Research {
                hypothesis_registered,
                testing_family_registered,
                frozen_dataset_verified,
                walk_forward_verified,
                adjusted_p_value_ppb,
                deflated_sharpe_probability_ppm,
                bootstrap_net_return_lower_bound_ppm,
                canary_capacity_micros,
                capacity_curve_bounded,
                internal_audit_approved,
                executor_review_approved,
                key_review_approved,
                restore_drill_approved,
            } => {
                let checks = [
                    (*hypothesis_registered, GateFailure::Hypothesis),
                    (*testing_family_registered, GateFailure::TestingFamily),
                    (*frozen_dataset_verified, GateFailure::Dataset),
                    (*walk_forward_verified, GateFailure::WalkForward),
                    (
                        *adjusted_p_value_ppb <= THREE_SIGMA_P_VALUE_PPB,
                        GateFailure::AdjustedSignificance,
                    ),
                    (
                        *deflated_sharpe_probability_ppm >= DSR_MIN_PPM,
                        GateFailure::DeflatedSharpe,
                    ),
                    (
                        *bootstrap_net_return_lower_bound_ppm > 0,
                        GateFailure::NetReturnConfidence,
                    ),
                    (
                        *canary_capacity_micros >= 25_000_000 && *capacity_curve_bounded,
                        GateFailure::Capacity,
                    ),
                    (*internal_audit_approved, GateFailure::InternalAudit),
                    (*executor_review_approved, GateFailure::ExecutorReview),
                    (*key_review_approved, GateFailure::KeyReview),
                    (*restore_drill_approved, GateFailure::RestoreDrill),
                ];
                for (passed, failure) in checks {
                    if !passed {
                        failures.push(failure);
                    }
                }
            }
            Self::EngineeringCanary {
                max_accounts,
                max_leg_notional_micros,
                max_gross_notional_micros,
                max_daily_turnover_micros,
                max_leverage_ppm,
                internal_audit_sha256,
                internal_audit_approved,
                executor_review_approved,
                key_review_approved,
                restore_drill_approved,
            } => {
                if !(1..=2).contains(max_accounts)
                    || *max_leg_notional_micros > 25_000_000
                    || *max_gross_notional_micros > 50_000_000
                    || *max_daily_turnover_micros > 50_000_000
                    || *max_leverage_ppm > 1_000_000
                {
                    failures.push(GateFailure::EngineeringCanaryScope);
                }
                if internal_audit_sha256.len() != 64
                    || !internal_audit_sha256
                        .bytes()
                        .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
                {
                    failures.push(GateFailure::InternalAuditDigest);
                }
                for (passed, failure) in [
                    (*internal_audit_approved, GateFailure::InternalAudit),
                    (*executor_review_approved, GateFailure::ExecutorReview),
                    (*key_review_approved, GateFailure::KeyReview),
                    (*restore_drill_approved, GateFailure::RestoreDrill),
                ] {
                    if !passed {
                        failures.push(failure);
                    }
                }
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
        PromotionEvidence::Research {
            hypothesis_registered: true,
            testing_family_registered: true,
            frozen_dataset_verified: true,
            walk_forward_verified: true,
            adjusted_p_value_ppb: 1_350_000,
            deflated_sharpe_probability_ppm: 990_000,
            bootstrap_net_return_lower_bound_ppm: 1,
            canary_capacity_micros: 25_000_000,
            capacity_curve_bounded: true,
            internal_audit_approved: true,
            executor_review_approved: true,
            key_review_approved: true,
            restore_drill_approved: true,
        }
    }

    fn engineering_canary() -> PromotionEvidence {
        PromotionEvidence::EngineeringCanary {
            max_accounts: 1,
            max_leg_notional_micros: 25_000_000,
            max_gross_notional_micros: 50_000_000,
            max_daily_turnover_micros: 50_000_000,
            max_leverage_ppm: 1_000_000,
            internal_audit_sha256:
                "19e928337af7381e09d0a088e6df02a9b1833533b8c9d8801ed4a7e8fe30a729".into(),
            internal_audit_approved: true,
            executor_review_approved: true,
            key_review_approved: true,
            restore_drill_approved: true,
        }
    }

    #[test]
    fn complete_evidence_promotes() {
        assert!(approved().can_promote_to_canary());
        assert_eq!(
            approved().calculate_hash(),
            "b3b6207ac05e2a66830d391ae06d0470e2a957b0c52d856cd0e064162da45646"
        );
    }

    #[test]
    fn evidence_hash_detects_changes() {
        let evidence = approved();
        let hash = evidence.calculate_hash();
        let mut changed = evidence;
        let PromotionEvidence::Research {
            internal_audit_approved,
            ..
        } = &mut changed
        else {
            unreachable!()
        };
        *internal_audit_approved = false;
        assert_eq!(hash.len(), 64);
        assert_ne!(hash, changed.calculate_hash());
    }

    #[test]
    fn every_gate_fails_closed() {
        let mut evidence = approved();
        let PromotionEvidence::Research {
            internal_audit_approved,
            ..
        } = &mut evidence
        else {
            unreachable!()
        };
        *internal_audit_approved = false;
        assert_eq!(evidence.canary_failures(), vec![GateFailure::InternalAudit]);
    }

    #[test]
    fn confidence_bound_must_be_strictly_positive() {
        let mut evidence = approved();
        let PromotionEvidence::Research {
            bootstrap_net_return_lower_bound_ppm,
            ..
        } = &mut evidence
        else {
            unreachable!()
        };
        *bootstrap_net_return_lower_bound_ppm = 0;
        assert_eq!(
            evidence.canary_failures(),
            vec![GateFailure::NetReturnConfidence]
        );
    }

    #[test]
    fn engineering_canary_uses_bounded_internal_evidence() {
        let evidence = engineering_canary();
        assert!(evidence.can_promote_to_canary());
        assert_eq!(
            evidence.calculate_hash(),
            "2a6bc1f8b43d24714e83a478a4c454439bd6cebedca334bd37963924d3ab9711"
        );

        let mut out_of_scope = engineering_canary();
        let PromotionEvidence::EngineeringCanary { max_accounts, .. } = &mut out_of_scope else {
            unreachable!()
        };
        *max_accounts = 3;
        assert_eq!(
            out_of_scope.canary_failures(),
            vec![GateFailure::EngineeringCanaryScope]
        );
    }

    #[test]
    fn internal_canary_can_bypass_observation_stages() {
        assert!(PromotionState::Registered.can_transition_to(PromotionState::Research));
        assert!(!PromotionState::Registered.can_transition_to(PromotionState::Shadow));
        assert!(PromotionState::Registered.can_transition_to(PromotionState::CanaryEligible));
        assert!(!PromotionState::CanaryEligible.can_transition_to(PromotionState::Research));
        assert!(PromotionState::CanaryEligible.can_transition_to(PromotionState::Retired));
    }
}
