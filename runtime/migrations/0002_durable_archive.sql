ALTER TYPE finality_state ADD VALUE IF NOT EXISTS 'l1_posted' AFTER 'confirmed';

CREATE TYPE canonical_state AS ENUM ('canonical', 'orphaned', 'not_applicable');
CREATE TYPE archive_state AS ENUM ('pending', 'leased', 'archived');

ALTER TABLE raw_market_events
    ADD COLUMN schema_version TEXT NOT NULL DEFAULT '1',
    ADD COLUMN source_session TEXT NOT NULL DEFAULT 'legacy',
    ADD COLUMN source_event_id TEXT,
    ADD COLUMN parent_block_hash TEXT,
    ADD COLUMN canonical_state canonical_state NOT NULL DEFAULT 'not_applicable',
    ALTER COLUMN raw_object_key DROP NOT NULL;

UPDATE raw_market_events
SET source_event_id = 'payload:' || payload_sha256
WHERE source_event_id IS NULL;

ALTER TABLE raw_market_events
    ALTER COLUMN source_event_id SET NOT NULL,
    DROP CONSTRAINT raw_market_events_source_connector_version_payload_sha256_key,
    ADD CONSTRAINT raw_market_events_source_identity_key
        UNIQUE (source, source_session, source_event_id);

CREATE INDEX raw_market_events_block_idx
    ON raw_market_events (block_number DESC, block_hash)
    WHERE block_number IS NOT NULL;

CREATE TABLE event_staging (
    event_id UUID NOT NULL REFERENCES raw_market_events(id) ON DELETE RESTRICT,
    received_at TIMESTAMPTZ NOT NULL,
    raw_payload BYTEA NOT NULL,
    state archive_state NOT NULL DEFAULT 'pending',
    leased_until TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error TEXT,
    archived_at TIMESTAMPTZ,
    PRIMARY KEY (event_id, received_at),
    CHECK (
        (state = 'leased' AND leased_until IS NOT NULL)
        OR (state <> 'leased' AND leased_until IS NULL)
    )
) PARTITION BY RANGE (received_at);

CREATE INDEX event_staging_pending_idx
    ON event_staging (received_at, event_id)
    WHERE state <> 'archived';

CREATE FUNCTION ensure_event_staging_partition(event_time TIMESTAMPTZ)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
    month_start TIMESTAMPTZ := date_trunc('month', event_time);
    month_end TIMESTAMPTZ := month_start + INTERVAL '1 month';
    partition_name TEXT := format('event_staging_y%sm%s',
        to_char(month_start, 'YYYY'), to_char(month_start, 'MM'));
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext(partition_name));
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF event_staging FOR VALUES FROM (%L) TO (%L)',
        partition_name, month_start, month_end
    );
END;
$$;

CREATE TABLE archive_segments (
    id UUID PRIMARY KEY,
    object_key TEXT NOT NULL UNIQUE,
    content_sha256 CHAR(64) NOT NULL UNIQUE,
    uncompressed_sha256 CHAR(64) NOT NULL,
    source TEXT NOT NULL,
    source_session TEXT NOT NULL,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    event_count INTEGER NOT NULL CHECK (event_count > 0),
    compressed_bytes BIGINT NOT NULL CHECK (compressed_bytes > 0),
    uncompressed_bytes BIGINT NOT NULL CHECK (uncompressed_bytes > 0),
    archived_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ends_at >= starts_at)
);

CREATE INDEX archive_segments_time_idx
    ON archive_segments (starts_at, ends_at);

CREATE TABLE archive_segment_events (
    segment_id UUID NOT NULL REFERENCES archive_segments(id) ON DELETE RESTRICT,
    event_id UUID NOT NULL REFERENCES raw_market_events(id) ON DELETE RESTRICT,
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (segment_id, position),
    UNIQUE (event_id)
);

CREATE TABLE archive_manifests (
    id UUID PRIMARY KEY,
    day DATE NOT NULL,
    object_key TEXT NOT NULL UNIQUE,
    manifest_sha256 CHAR(64) NOT NULL UNIQUE,
    event_count BIGINT NOT NULL CHECK (event_count > 0),
    segment_count INTEGER NOT NULL CHECK (segment_count > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (day, manifest_sha256)
);

COMMENT ON TABLE event_staging IS
    'Seven-day recovery outbox for exact wire payloads awaiting content-addressed archival.';
COMMENT ON TABLE archive_segment_events IS
    'Transactional acknowledgement binding accepted events to one immutable R2 segment.';
