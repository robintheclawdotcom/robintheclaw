pub mod artifact;
pub mod gates;
pub mod strategy;
pub mod walk_forward;

pub use artifact::{DatasetManifest, ModelArtifact, ModelArtifactError, Regime};
pub use gates::{GateFailure, PromotionEvidence, PromotionState};
pub use strategy::{
    StrategyManifest, StrategyManifestError, StrategyPromotionState, BASIS_AAPL_V1,
};
pub use walk_forward::{validate_folds, FoldError, WalkForwardFold};
