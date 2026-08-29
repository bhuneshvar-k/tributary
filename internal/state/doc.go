// Package state persists sync checkpoints in SQLite (modernc.org/sqlite,
// pure Go, no CGO — keeps the "single binary" deliverable true, and this
// doesn't need a real coordination service at this scale).
//
// Phase 2 (see state.go) tracks one-shot load resumability at per-table
// granularity: a run is identified by a stable hash of source+target+
// seed+schema-file (RunKey.ID), and each table's load is checkpointed only
// after its transaction commits — so a crash mid-table leaves nothing to
// resume from half-way (Postgres discards the whole uncommitted
// transaction), and a resumed run simply skips tables already marked done.
//
// Phase 4 extends this with the last-applied WAL LSN per subscription for
// logical-replication resumability — not yet implemented.
package state
