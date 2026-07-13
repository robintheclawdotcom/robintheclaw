# Data-plane archive

## Acceptance boundary

An event is accepted only when its normalized envelope and exact wire payload commit in the same
Postgres transaction. R2 is an immutable archive, not the ingestion queue. A failed upload leaves
the accepted payload in `event_staging`; an upload followed by a failed acknowledgement leaves an
unreferenced content-addressed object that the next attempt may safely overwrite.

The source identity is `(source, source_session, source_event_id)`. Connectors must derive the event
identifier from the venue or chain protocol. A payload digest protects bytes in transit and at rest,
but is not an event identity: distinct protocol events may legitimately carry identical bytes.

## Staging and archival

`event_staging` is range-partitioned by receive month. The collector creates the required partition
inside the acceptance transaction. Archivers claim work with `FOR UPDATE SKIP LOCKED` and a five-
minute lease, so multiple workers can share the queue without processing an accepted event twice.
Expired leases are reclaimable after a worker crash.

Each segment contains events from one source session, ordered by receive time and event UUID. A
segment spans no more than 30 seconds and contains no more than 64 MiB of uncompressed NDJSON. The
NDJSON stores envelope fields and a base64 encoding of the exact wire bytes. Zstandard level 3 is
used for compression. The SHA-256 digest of the compressed bytes determines the R2 object key.

After upload, one database transaction records the segment, binds every event to its position,
updates the event's object key, and marks all staging rows archived. If any lease changed before the
transaction, the acknowledgement fails closed. Archived staging bytes remain recoverable for seven
days and are then deleted by the collector's hourly maintenance cycle.

## Manifests and replay

The maintenance cycle publishes a deterministic manifest for the previous UTC day. Entries are
sorted by object key and include compressed and uncompressed digests, byte counts, event counts,
source sessions, and time bounds. The manifest has its own digest and content-addressed key under
`manifests/`.

Replay verifies the segment digest before decompression, rejects duplicate source identities, checks
each wire-payload digest, and decodes the payload from the archived bytes. A digest failure or an
invalid JSON line invalidates the entire segment; replay never skips a corrupt event.

## Operations

Monitor pending and expired staging rows, lease attempts, archive latency, object-store failures,
manifest publication, and the count of accepted events without an archive binding. Alert when the
oldest pending event exceeds 60 seconds. Retain raw R2 objects for at least 365 days and manifests
indefinitely. Partition removal, retention-policy changes, and restore tests are operator-approved
maintenance actions.

The runtime contains no venue write API, wallet integration, signing material, or live execution
switch. Archival credentials must be scoped to the private research bucket.
