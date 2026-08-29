# Tributary

A referentially-consistent Postgres subsetting & sync engine, written in Go
— the tool [Neosync](https://github.com/nucleuscloud/neosync) stopped being
when it was archived in August 2025.

Given a seed predicate (`team_id = X`), Tributary walks the foreign-key
graph — real `pg_catalog` constraints merged with a user-declared
`tributary.schema.yaml` for relationships the database doesn't enforce
(Rails/ActiveRecord, polymorphic associations) — to compute exactly the
rows that need to move to stay referentially valid. It loads that subset
into a target database, masks anything sensitive on the way in, and keeps
it current afterwards via logical replication instead of re-dumping.

By default the subset stays scoped **downstream** of the seed: a row
needed only to satisfy a foreign key (e.g. the company a seeded user's
membership references) is included, but isn't itself used to fan back out
to everything else that references it (every other member of that
company). Pass `--include-upstream` to get the old fully bidirectional
walk instead — useful when you deliberately want "this seed's whole
tenant," not just what's reachable downstream of it.

Full project plan (architecture, tech stack, phased roadmap, risk
register): see [`docs/PLAN.md`](docs/PLAN.md).

## Status

Phase 2 — one-shot export + load, the first usable MVP. `tributary inspect`
prints the schema graph as JSON (phase 0). `tributary plan` computes a
referentially-consistent subset from a seed table + predicate — merging
real `pg_catalog` foreign keys with a declared `tributary.schema.yaml`
(soft FKs, composite keys, polymorphic associations, ignores, and
`dependency_breaks` for self-referencing/cyclic tables) — and prints a
row-count-per-table report (phase 1). `tributary sync run` actually copies
that subset from a source database into a target one, auto-creating the
target schema if it's missing.

## Getting started

```sh
# requires Go 1.25+ (pinned transitively by pgx v5.9+) — brew install go
go mod tidy
make build

export TRIBUTARY_DSN="postgres://user:pass@localhost:5432/mydb?sslmode=disable"
./bin/tributary inspect

./bin/tributary plan \
  --seed-table users --seed-predicate "id = 42" \
  --schema-file tributary.schema.example.yaml

export TRIBUTARY_SOURCE_DSN="postgres://user:pass@localhost:5432/mydb?sslmode=disable"
export TRIBUTARY_TARGET_DSN="postgres://user:pass@localhost:5432/mydb_staging?sslmode=disable"
./bin/tributary sync run \
  --seed-table users --seed-predicate "id = 42" \
  --schema-file tributary.schema.example.yaml
```

`--seed-predicate` is a raw SQL `WHERE`-clause fragment, interpolated
directly — tributary is an admin CLI, not a web input path, so it is not
sanitized against injection.

`sync run` requires either a pre-existing, compatible target schema or
lets Tributary auto-create missing tables (columns, types, `NOT NULL`,
`PRIMARY KEY`, real `FOREIGN KEY` constraints, and missing enum types —
not defaults, sequences, check constraints, indexes beyond the PK,
triggers, views, or non-enum custom types like domains/composites;
`--no-create-schema` disables auto-creation and fails preflight on a
missing table instead). **Re-running is safe and expected**:
a row that already exists on target is updated to match source (upsert,
keyed on primary key), a new row is inserted — nothing errors just because
you ran it before. `--fresh` is a stronger reset: it deletes exactly this
subset's previously-loaded rows before reloading, for when you want target
to end up with nothing but exactly today's source data. A crashed run
resumes automatically (per-table checkpoints in a local SQLite file,
default `./.tributary/state.db`); `--no-resume` disables this.

See [`tributary.schema.example.yaml`](tributary.schema.example.yaml) for
the declared-relations file shape: soft FKs, composite keys, polymorphic
associations, ignores, and `dependency_breaks`.

### Testing

`go test ./...` runs the unit suite everywhere (`pkg/config`, `internal/
state`). `internal/load`'s and `cmd/tributary`'s integration tests
additionally need Docker (they spin up real Postgres via
`testcontainers-go` — subset/load correctness isn't trusted to mocks) and
skip themselves cleanly with a clear message if no Docker daemon is
reachable. `internal/graph` and `internal/subset` currently have no test
coverage of their own (a prior fixture using business-domain table names
was removed; not yet replaced).

## License

MIT — see [LICENSE](LICENSE).
