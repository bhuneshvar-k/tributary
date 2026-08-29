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

Phase 0 — schema introspection. `tributary inspect` connects to a Postgres
database and prints its schema graph (tables, columns, foreign keys) as
JSON. Nothing else is implemented yet.

## Getting started

```sh
# requires Go 1.22+ — brew install go
go mod tidy    # resolves the pinned pgx version to the current release
make build
TRIBUTARY_DSN="postgres://user:pass@localhost:5432/mydb?sslmode=disable" \
  ./bin/tributary inspect
```

See [`tributary.schema.example.yaml`](tributary.schema.example.yaml) for
what the (not-yet-implemented) declared-relations file will look like.

## License

MIT — see [LICENSE](LICENSE).
