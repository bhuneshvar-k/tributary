// Package load upserts a computed subset into a target database: first
// EnsureSchema (ddl.go) stands up any missing target tables/constraints
// from the source schema, then Load (load.go) processes each table's rows
// in the FK-safe order internal/subset produced, one transaction per
// table. Re-running sync run is expected to just refresh the target — a
// row that already exists there is updated to match source, not treated
// as an error.
//
// COPY has no conflict handling, so each table's true row values are
// COPY'd into an unconstrained TEMP staging table first (fast, and never
// hits a constraint since staging has none), then merged into the real
// table with one INSERT ... ON CONFLICT (primary key) DO UPDATE.
//
// A self-referencing FK column can't be satisfied by any single row
// insertion order (Postgres checks a non-deferred FK per row, not batched
// at the end), so such a column is always forced NULL in the merge — on
// both the insert and the conflict-update path — and backfilled via a
// batched UPDATE in the same transaction afterward. See loadOneTable's doc
// comment for exactly which rows that leaves permanently NULL and why.
//
// Phase 2 of the project plan — the first end-to-end usable version of the
// tool.
package load
