# Tributary — project plan

A referentially-consistent Postgres subsetting & sync engine, written in Go.
Full plan with architecture diagram, tech stack rationale, and risk
register: https://claude.ai/code/artifact/9b740863-bcc4-474a-adb0-f9090d329e97

## Why

Neosync did this well — Go, referential-integrity-aware subsetting, PII
masking — until it was archived Aug 30, 2025. Nothing actively maintained
does incremental, repeat subset sync today.

## Scope

**v1 ships:** seed-predicate → referentially-consistent subset (FK graph
walk, merged with a user-declared `relations:` file for app-level FKs
pg_catalog can't see) → one-shot export/load → column masking → incremental
re-sync via logical replication → resumable via checkpoints.

**v2 stretch:** lightweight branch of a synced subset via ZFS/BTRFS
snapshot (`branch create` / `switch` / `reset-to-parent`).

**Explicitly cut:** automatic merge of diverged branches (not a solved
problem for arbitrary FK-linked writes — see the plan's risk register), a
custom CoW storage engine (Neon-scale, not a v1 feature), non-Postgres
sources.

## Phases

| # | Phase | Status |
|---|-------|--------|
| 0 | Repo & schema introspection (`tributary inspect`) | done |
| 1 | FK graph & subset closure, incl. declared relations (`tributary plan`) | done |
| 2 | One-shot export + load (usable MVP) (`tributary sync run`) | done |
| 3 | Masking & transform pipeline | not started |
| 4 | Incremental sync via logical replication | not started |
| 5 | Observability & hardening | not started |
| 6 | Lightweight branching (stretch) | not started |

See the linked artifact for the full write-up: pitch, architecture diagram,
tech stack table, per-phase deliverables, and the risk register.

## Phase 2 notes (superseding the linked artifact's risk register)

The design initially shipped `sync run` as a hard error on re-invocation
against an already-synced target (requiring `--fresh` to reload), on the
reasoning that idempotent re-sync was phase 4's job. Real usage
immediately showed that reasoning wrong: re-running against a target with
some overlapping rows is a completely ordinary thing to want to do, not
an edge case. `sync run` now does what the original risk register said
from the start — "upserts keyed on primary key, not blind inserts" —
as its default behavior: a row that already exists on target is updated
to match source, a new row is inserted, and nothing about a repeat
invocation errors. `--fresh` remains as a stronger reset (row-scoped
delete-then-reload) for when upsert alone isn't enough. Mechanically,
this is `COPY` into an unconstrained per-table temp staging table
followed by one `INSERT ... ON CONFLICT (primary key) DO UPDATE` merge —
see `internal/load`'s package doc comment.

Phase 2 also went further than the artifact's original scope: the target
database's schema is auto-created if missing (columns, types, `NOT NULL`,
`PRIMARY KEY`, real `FOREIGN KEY` constraints) rather than requiring a
pre-provisioned target — see `internal/load.EnsureSchema`. This does not
replicate defaults, sequences/identity, check constraints, indexes beyond
the implicit PK index, triggers, or views. Enum types are the one custom
type *definition* that does get auto-created (`CREATE TYPE ... AS ENUM`,
same labels/order as source, via `internal/catalog.Schema.Enums`) — real
usage against a production-shaped schema (TypeORM-style enum columns)
showed requiring every enum to be pre-created by hand was more friction
than the fidelity risk was worth. A domain, composite, or range type is
still a hard, named preflight error: those aren't safe to recreate from
just a type name.

## Phase 1 revision: downstream-only traversal became the default

`internal/graph.ComputeClosure` originally walked every discovered row in
both directions unconditionally — a row pulled in only to satisfy a
foreign key (a required parent) was just as much a fan-out point as the
seed itself. Real usage (via `sync run`, but the fix lives in phase 1's
closure algorithm and applies to `tributary plan` too) showed this
explodes badly through any shared "hub" row: seeding one user pulled in
their clinic membership, which needed their company to exist, and then
fanned back out from that company to *every other member's* membership
row and from there to every other user — the seed's whole tenant, not the
seed.

`ComputeClosure` now defaults to `ModeDownstreamOnly`: a row reached via
an outgoing edge (a required parent) is still fetched, but doesn't itself
become a new fan-out point — unless it turns out to also be genuinely
reachable by fanning out from the seed through some other path, in which
case it's promoted (see `rowKind` in `internal/graph/closure.go`). The
original unconditional behavior survives as `ModeFull`, opt-in via
`--include-upstream` on both `plan` and `sync run`, for when "this seed's
whole tenant" is actually the intended scope.
