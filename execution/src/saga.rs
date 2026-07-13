use serde::{Deserialize, Serialize};
use thiserror::Error;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ExecutionState {
    Created,
    Prechecked,
    PerpSubmitted,
    PerpPartial,
    PerpFilled,
    SpotSubmitted,
    Hedged,
    Exiting,
    Unwinding,
    Closed,
    Cancelled,
    Expired,
    Unhedged,
    FailedSafe,
}

impl ExecutionState {
    pub fn is_terminal(self) -> bool {
        matches!(
            self,
            Self::Closed | Self::Cancelled | Self::Expired | Self::FailedSafe
        )
    }

    pub fn exposure_resolved(self) -> bool {
        matches!(self, Self::Closed | Self::Cancelled | Self::Expired)
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum ExecutionEvent {
    PrecheckPassed,
    PerpSubmitted,
    PerpPartiallyFilled {
        filled_base: u64,
    },
    PerpFilled {
        filled_base: u64,
    },
    PerpOverfilled {
        filled_base: u64,
    },
    PerpRejected,
    SpotSubmitted,
    SpotConfirmed {
        #[serde(with = "crate::intent::u128_string")]
        received_raw: u128,
    },
    SpotMismatched {
        #[serde(with = "crate::intent::u128_string")]
        received_raw: u128,
    },
    SpotRejected,
    ExitStarted,
    UnwindStarted,
    PerpUnwindProgress {
        unwound_base: u64,
    },
    PerpUnwindCompleted {
        unwound_base: u64,
    },
    PerpUnwindFailed {
        unwound_base: u64,
    },
    HedgeRestored,
    Closed,
    Cancelled,
    Expired,
    Unhedged,
    SafeFailure,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ExecutionSaga {
    pub intent_id: String,
    pub state: ExecutionState,
    pub version: u64,
    pub perp_filled_base: u64,
    #[serde(default)]
    pub perp_unwound_base: u64,
    #[serde(with = "crate::intent::u128_string")]
    pub spot_received_raw: u128,
}

#[derive(Debug, Error, PartialEq, Eq)]
pub enum SagaError {
    #[error("intent id is required")]
    MissingIntent,
    #[error("terminal saga cannot transition")]
    Terminal,
    #[error("invalid transition from {state:?}")]
    InvalidTransition { state: ExecutionState },
    #[error("fill amount must increase")]
    NonIncreasingFill,
}

impl ExecutionSaga {
    pub fn new(intent_id: impl Into<String>) -> Result<Self, SagaError> {
        let intent_id = intent_id.into();
        if intent_id.is_empty() {
            return Err(SagaError::MissingIntent);
        }
        Ok(Self {
            intent_id,
            state: ExecutionState::Created,
            version: 0,
            perp_filled_base: 0,
            perp_unwound_base: 0,
            spot_received_raw: 0,
        })
    }

    pub fn apply(&mut self, event: ExecutionEvent) -> Result<(), SagaError> {
        if self.state.is_terminal() {
            return Err(SagaError::Terminal);
        }

        let next = match (&self.state, event) {
            (ExecutionState::Created, ExecutionEvent::PrecheckPassed) => ExecutionState::Prechecked,
            (ExecutionState::Prechecked, ExecutionEvent::PerpSubmitted) => {
                ExecutionState::PerpSubmitted
            }
            (
                ExecutionState::PerpSubmitted | ExecutionState::PerpPartial,
                ExecutionEvent::PerpPartiallyFilled { filled_base },
            ) => {
                if filled_base <= self.perp_filled_base {
                    return Err(SagaError::NonIncreasingFill);
                }
                self.perp_filled_base = filled_base;
                ExecutionState::PerpPartial
            }
            (
                ExecutionState::PerpSubmitted | ExecutionState::PerpPartial,
                ExecutionEvent::PerpFilled { filled_base },
            ) => {
                if filled_base < self.perp_filled_base || filled_base == 0 {
                    return Err(SagaError::NonIncreasingFill);
                }
                self.perp_filled_base = filled_base;
                ExecutionState::PerpFilled
            }
            (
                ExecutionState::PerpSubmitted | ExecutionState::PerpPartial,
                ExecutionEvent::PerpOverfilled { filled_base },
            ) => {
                if filled_base <= self.perp_filled_base {
                    return Err(SagaError::NonIncreasingFill);
                }
                self.perp_filled_base = filled_base;
                ExecutionState::Unwinding
            }
            (ExecutionState::PerpSubmitted, ExecutionEvent::PerpRejected) => {
                ExecutionState::Cancelled
            }
            (
                ExecutionState::PerpFilled | ExecutionState::PerpPartial,
                ExecutionEvent::SpotSubmitted,
            ) => ExecutionState::SpotSubmitted,
            (ExecutionState::SpotSubmitted, ExecutionEvent::SpotConfirmed { received_raw }) => {
                if received_raw == 0 {
                    return Err(SagaError::InvalidTransition { state: self.state });
                }
                self.spot_received_raw = received_raw;
                ExecutionState::Hedged
            }
            (ExecutionState::SpotSubmitted, ExecutionEvent::SpotMismatched { received_raw }) => {
                if received_raw == 0 {
                    return Err(SagaError::InvalidTransition { state: self.state });
                }
                self.spot_received_raw = received_raw;
                ExecutionState::Unwinding
            }
            (ExecutionState::SpotSubmitted, ExecutionEvent::SpotRejected) => {
                ExecutionState::Unwinding
            }
            (ExecutionState::Hedged, ExecutionEvent::ExitStarted) => ExecutionState::Exiting,
            (
                ExecutionState::PerpPartial
                | ExecutionState::PerpFilled
                | ExecutionState::SpotSubmitted
                | ExecutionState::Hedged
                | ExecutionState::Exiting
                | ExecutionState::Unhedged,
                ExecutionEvent::UnwindStarted,
            ) => ExecutionState::Unwinding,
            (ExecutionState::Unwinding, ExecutionEvent::PerpUnwindProgress { unwound_base }) => {
                if unwound_base <= self.perp_unwound_base || unwound_base >= self.perp_filled_base {
                    return Err(SagaError::InvalidTransition { state: self.state });
                }
                self.perp_unwound_base = unwound_base;
                ExecutionState::Unwinding
            }
            (ExecutionState::Unwinding, ExecutionEvent::PerpUnwindCompleted { unwound_base }) => {
                if unwound_base != self.perp_filled_base
                    || unwound_base < self.perp_unwound_base
                    || unwound_base == 0
                {
                    return Err(SagaError::InvalidTransition { state: self.state });
                }
                self.perp_unwound_base = unwound_base;
                if self.spot_received_raw == 0 {
                    ExecutionState::Closed
                } else {
                    ExecutionState::Unwinding
                }
            }
            (ExecutionState::Unwinding, ExecutionEvent::PerpUnwindFailed { unwound_base }) => {
                if unwound_base < self.perp_unwound_base || unwound_base > self.perp_filled_base {
                    return Err(SagaError::InvalidTransition { state: self.state });
                }
                self.perp_unwound_base = unwound_base;
                ExecutionState::Unhedged
            }
            (ExecutionState::Unwinding, ExecutionEvent::HedgeRestored) => ExecutionState::Hedged,
            (ExecutionState::Exiting | ExecutionState::Unwinding, ExecutionEvent::Closed) => {
                ExecutionState::Closed
            }
            (
                ExecutionState::Created
                | ExecutionState::Prechecked
                | ExecutionState::PerpSubmitted,
                ExecutionEvent::Cancelled,
            ) => ExecutionState::Cancelled,
            (
                ExecutionState::Created
                | ExecutionState::Prechecked
                | ExecutionState::PerpSubmitted,
                ExecutionEvent::Expired,
            ) => ExecutionState::Expired,
            (ExecutionState::Unwinding, ExecutionEvent::Unhedged) => ExecutionState::Unhedged,
            (_, ExecutionEvent::SafeFailure) => self.state,
            _ => return Err(SagaError::InvalidTransition { state: self.state }),
        };

        self.state = next;
        self.version = self.version.saturating_add(1);
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn happy_path_reaches_closed() {
        let mut saga = ExecutionSaga::new("intent-1").unwrap();
        let events = [
            ExecutionEvent::PrecheckPassed,
            ExecutionEvent::PerpSubmitted,
            ExecutionEvent::PerpFilled { filled_base: 100 },
            ExecutionEvent::SpotSubmitted,
            ExecutionEvent::SpotConfirmed { received_raw: 100 },
            ExecutionEvent::ExitStarted,
            ExecutionEvent::Closed,
        ];
        for event in events {
            saga.apply(event).unwrap();
        }
        assert_eq!(saga.state, ExecutionState::Closed);
        assert_eq!(saga.version, 7);
    }

    #[test]
    fn spot_failure_requires_unwind() {
        let mut saga = ExecutionSaga::new("intent-1").unwrap();
        for event in [
            ExecutionEvent::PrecheckPassed,
            ExecutionEvent::PerpSubmitted,
            ExecutionEvent::PerpFilled { filled_base: 100 },
            ExecutionEvent::SpotSubmitted,
            ExecutionEvent::SpotRejected,
        ] {
            saga.apply(event).unwrap();
        }
        assert_eq!(saga.state, ExecutionState::Unwinding);
        saga.apply(ExecutionEvent::Closed).unwrap();
        assert_eq!(saga.state, ExecutionState::Closed);
    }

    #[test]
    fn duplicate_or_decreasing_fill_is_rejected() {
        let mut saga = ExecutionSaga::new("intent-1").unwrap();
        saga.apply(ExecutionEvent::PrecheckPassed).unwrap();
        saga.apply(ExecutionEvent::PerpSubmitted).unwrap();
        saga.apply(ExecutionEvent::PerpPartiallyFilled { filled_base: 50 })
            .unwrap();
        assert_eq!(
            saga.apply(ExecutionEvent::PerpPartiallyFilled { filled_base: 50 }),
            Err(SagaError::NonIncreasingFill)
        );
    }

    #[test]
    fn terminal_state_is_immutable() {
        let mut saga = ExecutionSaga::new("intent-1").unwrap();
        saga.apply(ExecutionEvent::Cancelled).unwrap();
        assert_eq!(
            saga.apply(ExecutionEvent::SafeFailure),
            Err(SagaError::Terminal)
        );
    }

    #[test]
    fn safe_failure_preserves_recovery_state() {
        let mut saga = ExecutionSaga::new("intent-1").unwrap();
        saga.apply(ExecutionEvent::PrecheckPassed).unwrap();
        saga.apply(ExecutionEvent::PerpSubmitted).unwrap();
        saga.apply(ExecutionEvent::SafeFailure).unwrap();
        assert_eq!(saga.state, ExecutionState::PerpSubmitted);
    }

    #[test]
    fn mismatched_spot_fill_requires_unwind() {
        let mut saga = ExecutionSaga::new("intent-1").unwrap();
        for event in [
            ExecutionEvent::PrecheckPassed,
            ExecutionEvent::PerpSubmitted,
            ExecutionEvent::PerpFilled { filled_base: 100 },
            ExecutionEvent::SpotSubmitted,
            ExecutionEvent::SpotMismatched { received_raw: 99 },
        ] {
            saga.apply(event).unwrap();
        }
        assert_eq!(saga.state, ExecutionState::Unwinding);
        assert_eq!(saga.spot_received_raw, 99);
        saga.apply(ExecutionEvent::Unhedged).unwrap();
        assert_eq!(saga.state, ExecutionState::Unhedged);
    }

    #[test]
    fn overfill_records_exposure_and_enters_unwind() {
        let mut saga = ExecutionSaga::new("intent-1").unwrap();
        saga.apply(ExecutionEvent::PrecheckPassed).unwrap();
        saga.apply(ExecutionEvent::PerpSubmitted).unwrap();
        saga.apply(ExecutionEvent::PerpOverfilled { filled_base: 101 })
            .unwrap();
        assert_eq!(saga.state, ExecutionState::Unwinding);
        assert_eq!(saga.perp_filled_base, 101);
    }

    #[test]
    fn unresolved_failures_retain_the_exposure_lock() {
        assert!(!ExecutionState::Unhedged.is_terminal());
        assert!(!ExecutionState::Unhedged.exposure_resolved());
        assert!(ExecutionState::FailedSafe.is_terminal());
        assert!(!ExecutionState::FailedSafe.exposure_resolved());
    }

    #[test]
    fn unhedged_exposure_can_resume_unwind() {
        let mut saga = ExecutionSaga::new("intent-1").unwrap();
        for event in [
            ExecutionEvent::PrecheckPassed,
            ExecutionEvent::PerpSubmitted,
            ExecutionEvent::PerpFilled { filled_base: 100 },
            ExecutionEvent::UnwindStarted,
            ExecutionEvent::Unhedged,
            ExecutionEvent::UnwindStarted,
        ] {
            saga.apply(event).unwrap();
        }
        assert_eq!(saga.state, ExecutionState::Unwinding);
    }
}
