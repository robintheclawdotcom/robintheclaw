use serde::{Deserialize, Serialize};
use thiserror::Error;

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct WalkForwardFold {
    pub index: u32,
    pub train_start_ms: u64,
    pub train_end_ms: u64,
    pub embargo_end_ms: u64,
    pub test_start_ms: u64,
    pub test_end_ms: u64,
}

#[derive(Debug, Error, PartialEq, Eq)]
pub enum FoldError {
    #[error("at least one fold is required")]
    Empty,
    #[error("fold {0} has an invalid interval")]
    InvalidInterval(u32),
    #[error("fold indexes must be contiguous")]
    NonContiguousIndex,
    #[error("test windows overlap or run backwards")]
    OverlappingTest,
}

pub fn validate_folds(folds: &[WalkForwardFold]) -> Result<(), FoldError> {
    if folds.is_empty() {
        return Err(FoldError::Empty);
    }
    let mut previous_test_end = 0;
    for (position, fold) in folds.iter().enumerate() {
        if fold.index != position as u32 {
            return Err(FoldError::NonContiguousIndex);
        }
        if fold.train_start_ms >= fold.train_end_ms
            || fold.train_end_ms > fold.embargo_end_ms
            || fold.embargo_end_ms > fold.test_start_ms
            || fold.test_start_ms >= fold.test_end_ms
        {
            return Err(FoldError::InvalidInterval(fold.index));
        }
        if position > 0 && fold.test_start_ms < previous_test_end {
            return Err(FoldError::OverlappingTest);
        }
        previous_test_end = fold.test_end_ms;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn embargoed_folds_validate() {
        let folds = [
            WalkForwardFold {
                index: 0,
                train_start_ms: 1,
                train_end_ms: 10,
                embargo_end_ms: 12,
                test_start_ms: 12,
                test_end_ms: 20,
            },
            WalkForwardFold {
                index: 1,
                train_start_ms: 5,
                train_end_ms: 20,
                embargo_end_ms: 22,
                test_start_ms: 22,
                test_end_ms: 30,
            },
        ];
        assert_eq!(validate_folds(&folds), Ok(()));
    }

    #[test]
    fn missing_embargo_is_rejected() {
        let folds = [WalkForwardFold {
            index: 0,
            train_start_ms: 1,
            train_end_ms: 10,
            embargo_end_ms: 11,
            test_start_ms: 9,
            test_end_ms: 20,
        }];
        assert_eq!(validate_folds(&folds), Err(FoldError::InvalidInterval(0)));
    }
}
