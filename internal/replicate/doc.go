// Package replicate subscribes to Postgres logical replication (pgoutput,
// via jackc/pglogrepl), filters incoming WAL changes down to rows already
// inside the tracked subset (the Subset Boundary Filter), and re-evaluates
// rows that start or stop matching the seed predicate.
//
// Phase 4 of the project plan — the second genuinely hard problem in the
// system, after the FK graph closure in internal/graph.
//
// Not yet implemented.
package replicate
