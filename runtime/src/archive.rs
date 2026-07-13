use crate::{sha256, CanonicalState, Finality, MarketEventKind, RawMarketEvent};
use anyhow::Context;
use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use chrono::{DateTime, NaiveDate, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::HashSet;
use uuid::Uuid;

pub const MAX_SEGMENT_AGE_SECONDS: i64 = 30;
pub const MAX_SEGMENT_UNCOMPRESSED_BYTES: usize = 64 * 1024 * 1024;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ArchiveSegment {
    pub object_key: String,
    pub content_sha256: String,
    pub uncompressed_sha256: String,
    pub source: String,
    pub source_session: String,
    pub starts_at: DateTime<Utc>,
    pub ends_at: DateTime<Utc>,
    pub event_ids: Vec<Uuid>,
    pub uncompressed_bytes: usize,
    pub compressed: Vec<u8>,
}

impl ArchiveSegment {
    pub fn build(mut events: Vec<RawMarketEvent>) -> anyhow::Result<Self> {
        anyhow::ensure!(!events.is_empty(), "cannot archive an empty segment");
        events.sort_by_key(|event| (event.received_at, event.id));
        let source = events[0].source.clone();
        let source_session = events[0].source_session.clone();
        anyhow::ensure!(
            events
                .iter()
                .all(|event| event.source == source && event.source_session == source_session),
            "an archive segment cannot mix source sessions"
        );

        let starts_at = events[0].received_at;
        let ends_at = events.last().expect("non-empty segment").received_at;
        anyhow::ensure!(
            (ends_at - starts_at).num_milliseconds() <= MAX_SEGMENT_AGE_SECONDS * 1_000,
            "archive segment exceeds maximum age"
        );

        let mut ndjson = Vec::new();
        let mut event_ids = Vec::with_capacity(events.len());
        for event in events {
            event_ids.push(event.id);
            serde_json::to_writer(&mut ndjson, &ArchivedEvent::from(event))?;
            ndjson.push(b'\n');
            anyhow::ensure!(
                ndjson.len() <= MAX_SEGMENT_UNCOMPRESSED_BYTES,
                "archive segment exceeds maximum uncompressed size"
            );
        }

        let uncompressed_sha256 = sha256(&ndjson);
        let compressed =
            zstd::stream::encode_all(ndjson.as_slice(), 3).context("compress archive segment")?;
        let content_sha256 = sha256(&compressed);
        let day = starts_at.format("%Y-%m-%d");
        let object_key = format!(
            "raw/{}/{}/{}/{}.ndjson.zst",
            path_component(&source),
            path_component(&source_session),
            day,
            content_sha256
        );

        Ok(Self {
            object_key,
            content_sha256,
            uncompressed_sha256,
            source,
            source_session,
            starts_at,
            ends_at,
            event_ids,
            uncompressed_bytes: ndjson.len(),
            compressed,
        })
    }

    pub fn event_count(&self) -> usize {
        self.event_ids.len()
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct ArchivedEvent {
    id: Uuid,
    schema_version: String,
    source: String,
    source_session: String,
    source_event_id: String,
    connector_version: String,
    kind: MarketEventKind,
    symbol: Option<String>,
    source_timestamp_ms: Option<i64>,
    received_at: DateTime<Utc>,
    source_sequence: Option<String>,
    block_number: Option<i64>,
    block_hash: Option<String>,
    parent_block_hash: Option<String>,
    canonical_state: CanonicalState,
    finality: Finality,
    payload_sha256: String,
    raw_base64: String,
}

impl From<RawMarketEvent> for ArchivedEvent {
    fn from(event: RawMarketEvent) -> Self {
        Self {
            id: event.id,
            schema_version: event.schema_version,
            source: event.source,
            source_session: event.source_session,
            source_event_id: event.source_event_id,
            connector_version: event.connector_version,
            kind: event.kind,
            symbol: event.symbol,
            source_timestamp_ms: event.source_timestamp_ms,
            received_at: event.received_at,
            source_sequence: event.source_sequence,
            block_number: event.block_number,
            block_hash: event.block_hash,
            parent_block_hash: event.parent_block_hash,
            canonical_state: event.canonical_state,
            finality: event.finality,
            payload_sha256: event.payload_sha256,
            raw_base64: BASE64.encode(event.raw),
        }
    }
}

impl ArchivedEvent {
    fn into_event(self) -> anyhow::Result<RawMarketEvent> {
        let raw = BASE64
            .decode(self.raw_base64)
            .context("decode archived event payload")?;
        anyhow::ensure!(
            sha256(&raw) == self.payload_sha256,
            "archived event payload digest mismatch"
        );
        let payload: Value =
            serde_json::from_slice(&raw).context("decode archived JSON payload")?;
        Ok(RawMarketEvent {
            id: self.id,
            schema_version: self.schema_version,
            source: self.source,
            source_session: self.source_session,
            source_event_id: self.source_event_id,
            connector_version: self.connector_version,
            kind: self.kind,
            symbol: self.symbol,
            source_timestamp_ms: self.source_timestamp_ms,
            received_at: self.received_at,
            source_sequence: self.source_sequence,
            block_number: self.block_number,
            block_hash: self.block_hash,
            parent_block_hash: self.parent_block_hash,
            canonical_state: self.canonical_state,
            finality: self.finality,
            payload_sha256: self.payload_sha256,
            payload,
            raw,
        })
    }
}

pub fn replay_segment(
    compressed: &[u8],
    expected_sha256: &str,
) -> anyhow::Result<Vec<RawMarketEvent>> {
    anyhow::ensure!(
        sha256(compressed) == expected_sha256,
        "archive segment digest mismatch"
    );
    let ndjson = zstd::stream::decode_all(compressed).context("decompress archive segment")?;
    let mut identities = HashSet::new();
    let mut events = Vec::new();
    for (line_number, line) in ndjson.split(|byte| *byte == b'\n').enumerate() {
        if line.is_empty() {
            continue;
        }
        let archived: ArchivedEvent = serde_json::from_slice(line)
            .with_context(|| format!("decode archive line {}", line_number + 1))?;
        let identity = (
            archived.source.clone(),
            archived.source_session.clone(),
            archived.source_event_id.clone(),
        );
        anyhow::ensure!(
            identities.insert(identity),
            "archive segment contains a duplicate source event identity"
        );
        events.push(archived.into_event()?);
    }
    anyhow::ensure!(!events.is_empty(), "archive segment contains no events");
    Ok(events)
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ManifestEntry {
    pub object_key: String,
    pub content_sha256: String,
    pub uncompressed_sha256: String,
    pub source: String,
    pub source_session: String,
    pub starts_at: DateTime<Utc>,
    pub ends_at: DateTime<Utc>,
    pub event_count: usize,
    pub compressed_bytes: usize,
    pub uncompressed_bytes: usize,
}

impl From<&ArchiveSegment> for ManifestEntry {
    fn from(segment: &ArchiveSegment) -> Self {
        Self {
            object_key: segment.object_key.clone(),
            content_sha256: segment.content_sha256.clone(),
            uncompressed_sha256: segment.uncompressed_sha256.clone(),
            source: segment.source.clone(),
            source_session: segment.source_session.clone(),
            starts_at: segment.starts_at,
            ends_at: segment.ends_at,
            event_count: segment.event_count(),
            compressed_bytes: segment.compressed.len(),
            uncompressed_bytes: segment.uncompressed_bytes,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct DailyManifest {
    pub schema_version: u32,
    pub day: NaiveDate,
    pub entries: Vec<ManifestEntry>,
    pub event_count: usize,
    pub manifest_sha256: String,
}

impl DailyManifest {
    pub fn build(day: NaiveDate, mut entries: Vec<ManifestEntry>) -> anyhow::Result<Self> {
        anyhow::ensure!(
            !entries.is_empty(),
            "cannot build an empty archive manifest"
        );
        entries.sort_by(|left, right| left.object_key.cmp(&right.object_key));
        let event_count = entries.iter().map(|entry| entry.event_count).sum();
        let unsigned = serde_json::to_vec(&(1_u32, day, &entries, event_count))?;
        Ok(Self {
            schema_version: 1,
            day,
            entries,
            event_count,
            manifest_sha256: sha256(&unsigned),
        })
    }

    pub fn object_key(&self) -> String {
        format!(
            "manifests/{}/{}.json",
            self.day.format("%Y-%m-%d"),
            self.manifest_sha256
        )
    }

    pub fn verify(&self) -> anyhow::Result<()> {
        let unsigned = serde_json::to_vec(&(
            self.schema_version,
            self.day,
            &self.entries,
            self.event_count,
        ))?;
        anyhow::ensure!(
            sha256(&unsigned) == self.manifest_sha256,
            "archive manifest digest mismatch"
        );
        Ok(())
    }
}

fn path_component(value: &str) -> String {
    value
        .chars()
        .map(|character| {
            if character.is_ascii_alphanumeric() || matches!(character, '-' | '_' | '.') {
                character
            } else {
                '_'
            }
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{MarketEventKind, SourceIdentity};
    use chrono::Duration;

    fn event(sequence: u64, received_at: DateTime<Utc>) -> RawMarketEvent {
        let mut event = RawMarketEvent::from_source(
            "lighter",
            "1.2.3",
            SourceIdentity::new(
                "session-1",
                format!("order-book:{sequence}"),
                Some(sequence.to_string()),
            )
            .unwrap(),
            MarketEventKind::OrderBook,
            format!(r#"{{"nonce":{sequence}}}"#),
        )
        .unwrap();
        event.received_at = received_at;
        event
    }

    #[test]
    fn builds_and_replays_a_deterministic_segment() {
        let start = DateTime::parse_from_rfc3339("2026-07-13T10:00:00Z")
            .unwrap()
            .with_timezone(&Utc);
        let events = vec![event(2, start + Duration::seconds(1)), event(1, start)];
        let first = ArchiveSegment::build(events.clone()).unwrap();
        let second = ArchiveSegment::build(events).unwrap();
        assert_eq!(first.content_sha256, second.content_sha256);
        assert_eq!(first.compressed, second.compressed);

        let replayed = replay_segment(&first.compressed, &first.content_sha256).unwrap();
        assert_eq!(replayed.len(), 2);
        assert_eq!(replayed[0].source_event_id, "order-book:1");
        assert_eq!(replayed[1].raw, br#"{"nonce":2}"#);
    }

    #[test]
    fn rejects_mixed_sessions_and_oversized_windows() {
        let start = Utc::now();
        let mut other_session = event(2, start);
        other_session.source_session = "session-2".to_string();
        assert!(ArchiveSegment::build(vec![event(1, start), other_session]).is_err());
        assert!(ArchiveSegment::build(vec![
            event(1, start),
            event(2, start + Duration::seconds(31)),
        ])
        .is_err());
    }

    #[test]
    fn detects_segment_and_payload_tampering() {
        let segment = ArchiveSegment::build(vec![event(1, Utc::now())]).unwrap();
        let mut corrupt = segment.compressed.clone();
        corrupt[0] ^= 1;
        assert!(replay_segment(&corrupt, &segment.content_sha256).is_err());
    }

    #[test]
    fn manifest_digest_is_order_independent_and_verifiable() {
        let now = Utc::now();
        let first = ArchiveSegment::build(vec![event(1, now)]).unwrap();
        let second = ArchiveSegment::build(vec![event(2, now)]).unwrap();
        let day = now.date_naive();
        let a = DailyManifest::build(day, vec![(&first).into(), (&second).into()]).unwrap();
        let b = DailyManifest::build(day, vec![(&second).into(), (&first).into()]).unwrap();
        assert_eq!(a.manifest_sha256, b.manifest_sha256);
        a.verify().unwrap();
    }
}
