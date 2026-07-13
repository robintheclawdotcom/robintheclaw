import json
from dataclasses import asdict, dataclass
from hashlib import sha256
from typing import Any


@dataclass(frozen=True)
class ModelArtifact:
    schema_version: int
    model_name: str
    dataset_manifest_sha256: str
    code_commit: str
    feature_schema_sha256: str
    cost_model_version: str
    training_start_ms: int
    training_end_ms: int
    embargo_end_ms: int
    parameters: dict[str, Any]
    sha256: str


def freeze_artifact(payload: dict[str, Any]) -> ModelArtifact:
    required = {
        "schema_version",
        "model_name",
        "dataset_manifest_sha256",
        "code_commit",
        "feature_schema_sha256",
        "cost_model_version",
        "training_start_ms",
        "training_end_ms",
        "embargo_end_ms",
        "parameters",
    }
    if set(payload) != required:
        raise ValueError("artifact fields do not match the schema")
    if payload["training_start_ms"] >= payload["training_end_ms"]:
        raise ValueError("training interval is invalid")
    if payload["training_end_ms"] > payload["embargo_end_ms"]:
        raise ValueError("embargo interval is invalid")
    for field in ("dataset_manifest_sha256", "feature_schema_sha256"):
        value = payload[field]
        if len(value) != 64 or any(character not in "0123456789abcdef" for character in value):
            raise ValueError(f"{field} is not a lowercase SHA-256 digest")
    encoded = json.dumps(payload, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()
    digest = sha256(encoded).hexdigest()
    return ModelArtifact(**payload, sha256=digest)


def artifact_json(artifact: ModelArtifact) -> bytes:
    return json.dumps(
        asdict(artifact), sort_keys=True, separators=(",", ":"), allow_nan=False
    ).encode()
