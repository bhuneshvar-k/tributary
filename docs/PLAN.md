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
| 0 | Repo & schema introspection (`tributary inspect`) | in progress |
| 1 | FK graph & subset closure, incl. declared relations | not started |
| 2 | One-shot export + load (usable MVP) | not started |
| 3 | Masking & transform pipeline | not started |
| 4 | Incremental sync via logical replication | not started |
| 5 | Observability & hardening | not started |
| 6 | Lightweight branching (stretch) | not started |

See the linked artifact for the full write-up: pitch, architecture diagram,
tech stack table, per-phase deliverables, and the risk register.
