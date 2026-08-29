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

Full project plan (architecture, tech stack, phased roadmap, risk
register): see [`docs/PLAN.md`](docs/PLAN.md).

## Status

Phase 1 — FK graph & subset closure. `tributary inspect` prints the schema
graph as JSON (phase 0). `tributary plan` computes a referentially-
consistent subset from a seed table + predicate — merging real `pg_catalog`
foreign keys with a declared `tributary.schema.yaml` (soft FKs, composite
keys, polymorphic associations, ignores, and `dependency_breaks` for
self-referencing/cyclic tables) — and prints a row-count-per-table report.
Nothing moves data yet; that's phase 2.

## Getting started

```sh
# requires Go 1.25+ (pinned transitively by pgx v5.9+) — brew install go
go mod tidy
make build

TRIBUTARY_DSN="postgres://user:pass@localhost:5432/mydb?sslmode=disable"

./bin/tributary inspect

./bin/tributary plan \
  --seed-table users --seed-predicate "id = 42" \
  --schema-file tributary.schema.example.yaml
```

`--seed-predicate` is a raw SQL `WHERE`-clause fragment, interpolated
directly — tributary is an admin CLI, not a web input path, so it is not
sanitized against injection.

See [`tributary.schema.example.yaml`](tributary.schema.example.yaml) for
the declared-relations file shape: soft FKs, composite keys, polymorphic
associations, ignores, and `dependency_breaks`.

### Testing

`go test ./...` runs the unit suite everywhere. `internal/graph`'s closure
tests additionally need Docker (they spin up real Postgres via
`testcontainers-go` — subset/replication correctness isn't trusted to
mocks) and skip themselves cleanly with a clear message if no Docker
daemon is reachable.

## License

MIT — see [LICENSE](LICENSE).
