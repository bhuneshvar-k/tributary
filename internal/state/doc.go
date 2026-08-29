// Package state persists sync checkpoints: the last-applied WAL LSN per
// subscription, and the subset key set, so a crashed run resumes instead
// of re-scanning from scratch. Backed by SQLite (modernc.org/sqlite,
// pure Go, no CGO) — this doesn't need a coordination service at this
// scale.
//
// Introduced in phase 2 (one-shot load resumability), extended in phase 4
// (replication checkpointing).
//
// Not yet implemented.
package state
