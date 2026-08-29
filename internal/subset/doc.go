// Package subset orders the tables in an internal/graph.Graph so that
// parents are listed before children — used in phase 1 to print `tributary
// plan`'s row-count report in a readable, dependency-respecting order (see
// TableOrder).
//
// Turning this into a row-level INSERT plan — the ordered sequence of
// actual rows to load, plus the insert-then-backfill sequencing a
// dependency_break requires — is phase 2's job, once there's a loader for
// an order to serve. That's not implemented here yet.
package subset
