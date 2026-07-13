use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use thiserror::Error;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Regime {
    Normal,
    Illiquid,
    Dislocated,
    Unknown,
}

impl Regime {
    pub fn admits_new_risk(self) -> bool {
        self == Self::Normal
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct DatasetSegment {
    pub object_key: String,
    pub sha256: String,
    pub event_count: u64,
    pub first_received_at_ms: u64,
    pub last_received_at_ms: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct DatasetManifest {
    pub schema_version: u32,
    pub source_session: String,
    pub generated_at_ms: u64,
    pub segments: Vec<DatasetSegment>,
    pub sha256: String,
}

impl DatasetManifest {
    pub fn calculate_hash(&self) -> String {
        let mut hasher = Sha256::new();
        hasher.update(self.schema_version.to_be_bytes());
        write_field(&mut hasher, self.source_session.as_bytes());
        hasher.update(self.generated_at_ms.to_be_bytes());
        hasher.update((self.segments.len() as u64).to_be_bytes());
        for segment in &self.segments {
            write_field(&mut hasher, segment.object_key.as_bytes());
            write_field(&mut hasher, segment.sha256.as_bytes());
            hasher.update(segment.event_count.to_be_bytes());
            hasher.update(segment.first_received_at_ms.to_be_bytes());
            hasher.update(segment.last_received_at_ms.to_be_bytes());
        }
        hex::encode(hasher.finalize())
    }

    pub fn verify(&self) -> bool {
        self.schema_version > 0
            && !self.source_session.is_empty()
            && !self.segments.is_empty()
            && self.segments.iter().all(|segment| {
                !segment.object_key.is_empty()
                    && is_sha256(&segment.sha256)
                    && segment.event_count > 0
                    && segment.first_received_at_ms <= segment.last_received_at_ms
            })
            && self.sha256 == self.calculate_hash()
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ModelArtifact {
    pub schema_version: u32,
    pub model_name: String,
    pub dataset_manifest_sha256: String,
    pub code_commit: String,
    pub feature_schema_sha256: String,
    pub cost_model_version: String,
    pub training_start_ms: u64,
    pub training_end_ms: u64,
    pub embargo_end_ms: u64,
    pub parameters: BTreeMap<String, String>,
    pub sha256: String,
}

#[derive(Debug, Error, PartialEq, Eq)]
pub enum ModelArtifactError {
    #[error("artifact identity is incomplete")]
    MissingIdentity,
    #[error("artifact digest is malformed")]
    InvalidDigest,
    #[error("artifact intervals are invalid")]
    InvalidInterval,
    #[error("artifact checksum does not match")]
    ChecksumMismatch,
}

impl ModelArtifact {
    pub fn calculate_hash(&self) -> String {
        let mut hasher = Sha256::new();
        hasher.update(self.schema_version.to_be_bytes());
        for value in [
            &self.model_name,
            &self.dataset_manifest_sha256,
            &self.code_commit,
            &self.feature_schema_sha256,
            &self.cost_model_version,
        ] {
            write_field(&mut hasher, value.as_bytes());
        }
        hasher.update(self.training_start_ms.to_be_bytes());
        hasher.update(self.training_end_ms.to_be_bytes());
        hasher.update(self.embargo_end_ms.to_be_bytes());
        hasher.update((self.parameters.len() as u64).to_be_bytes());
        for (key, value) in &self.parameters {
            write_field(&mut hasher, key.as_bytes());
            write_field(&mut hasher, value.as_bytes());
        }
        hex::encode(hasher.finalize())
    }

    pub fn validate(&self) -> Result<(), ModelArtifactError> {
        if self.schema_version == 0
            || self.model_name.is_empty()
            || self.code_commit.is_empty()
            || self.cost_model_version.is_empty()
        {
            return Err(ModelArtifactError::MissingIdentity);
        }
        if !is_sha256(&self.dataset_manifest_sha256)
            || !is_sha256(&self.feature_schema_sha256)
            || !is_sha256(&self.sha256)
        {
            return Err(ModelArtifactError::InvalidDigest);
        }
        if self.training_start_ms >= self.training_end_ms
            || self.training_end_ms > self.embargo_end_ms
        {
            return Err(ModelArtifactError::InvalidInterval);
        }
        if self.sha256 != self.calculate_hash() {
            return Err(ModelArtifactError::ChecksumMismatch);
        }
        Ok(())
    }
}

fn write_field(hasher: &mut Sha256, value: &[u8]) {
    hasher.update((value.len() as u64).to_be_bytes());
    hasher.update(value);
}

fn is_sha256(value: &str) -> bool {
    value.len() == 64 && value.bytes().all(|byte| byte.is_ascii_hexdigit())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn digest() -> String {
        "a".repeat(64)
    }

    #[test]
    fn dataset_manifest_detects_tampering() {
        let mut manifest = DatasetManifest {
            schema_version: 1,
            source_session: "session".into(),
            generated_at_ms: 10,
            segments: vec![DatasetSegment {
                object_key: "raw/segment.zst".into(),
                sha256: digest(),
                event_count: 20,
                first_received_at_ms: 1,
                last_received_at_ms: 2,
            }],
            sha256: String::new(),
        };
        manifest.sha256 = manifest.calculate_hash();
        assert!(manifest.verify());
        manifest.segments[0].event_count += 1;
        assert!(!manifest.verify());
    }

    #[test]
    fn model_artifact_is_canonical() {
        let mut artifact = ModelArtifact {
            schema_version: 1,
            model_name: "net-cost-baseline".into(),
            dataset_manifest_sha256: digest(),
            code_commit: "abc123".into(),
            feature_schema_sha256: digest(),
            cost_model_version: "v1".into(),
            training_start_ms: 1,
            training_end_ms: 2,
            embargo_end_ms: 3,
            parameters: BTreeMap::from([("fee_bps".into(), "2".into())]),
            sha256: String::new(),
        };
        artifact.sha256 = artifact.calculate_hash();
        assert_eq!(artifact.validate(), Ok(()));
        artifact.parameters.insert("fee_bps".into(), "3".into());
        assert_eq!(
            artifact.validate(),
            Err(ModelArtifactError::ChecksumMismatch)
        );
    }

    #[test]
    fn unknown_regime_fails_closed() {
        assert!(!Regime::Unknown.admits_new_risk());
        assert!(Regime::Normal.admits_new_risk());
    }
}
