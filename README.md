# Tributary

Tributary is a Go CLI for creating and syncing **referentially consistent Postgres subsets**.

You choose seed rows (for example, one tenant or one user), Tributary walks real foreign keys plus user-declared relationships, computes the closure of rows that must travel together, and loads that subset into a target Postgres database.

## What it does today

Current implemented commands:

- `tributary inspect` — introspect schema (tables, columns, PKs, FKs, enums) as JSON
- `tributary plan` — compute subset row closure and print per-table row counts
- `tributary sync run` — copy the computed subset from source DB to target DB

Current phase status is tracked in [`docs/PLAN.md`](docs/PLAN.md).

## Why Tributary

Tributary is designed for cases like:

- creating lightweight staging/dev datasets from production-like schemas
- keeping a scoped subset synchronized to another Postgres database
- working with schemas that rely on app-level relations not enforced in DB constraints

## Requirements

- Go `1.25+` (see `go.mod`)
- Postgres source database
- Postgres target database (for `sync run`)
- Optional: Docker for integration tests

## Install / Build

```sh
git clone https://github.com/bhuneshvar-k/tributary.git
cd tributary
go mod tidy
make build
```

Binary location:

- `/home/runner/work/tributary/tributary/bin/tributary` (in this environment)
- `./bin/tributary` (relative from repo root)

## CLI overview

```sh
./bin/tributary --help
```

Top-level commands:

- `inspect`
- `plan`
- `sync run`

## Typical workflow

1. **Inspect** source schema
2. (Optional) define app-level relations in `tributary.schema.yaml`
3. **Plan** a seeded subset
4. **Sync** the subset into target DB
5. Re-run `sync run` to refresh target data

---

## Command reference

### `tributary inspect`

Prints source schema metadata as JSON.

```sh
./bin/tributary inspect --dsn "$TRIBUTARY_DSN"
# or:
TRIBUTARY_DSN='postgres://...'
./bin/tributary inspect
```

Flags:

- `--dsn string` (or env `TRIBUTARY_DSN`)

Output includes:

- tables
- columns and nullability/type metadata
- primary keys
- foreign keys
- enum definitions

---

### `tributary plan`

Computes a referentially consistent subset from a seed table + predicate.

```sh
./bin/tributary plan \
  --dsn "$TRIBUTARY_DSN" \
  --seed-table users \
  --seed-predicate "id = 42" \
  --schema-file tributary.schema.yaml
```

Flags:

- `--dsn string` (or env `TRIBUTARY_DSN`)
- `--seed-table string` (required)
- `--seed-predicate string` (required)
- `--schema-file string` (optional)
- `--strict-cycles` (fail instead of best-effort auto cycle break)
- `--include-upstream` (use full bidirectional fan-out)
- `--format text|json` (default `text`)

Behavior notes:

- Default mode is **downstream-only**: required parent rows are included but not used to fan out to all siblings.
- `--include-upstream` enables full fan-out from any discovered row.
- `--seed-predicate` is a raw SQL `WHERE` fragment and is used directly.

---

### `tributary sync run`

Executes one-shot subset copy from source to target.

```sh
./bin/tributary sync run \
  --source-dsn "$TRIBUTARY_SOURCE_DSN" \
  --target-dsn "$TRIBUTARY_TARGET_DSN" \
  --seed-table users \
  --seed-predicate "id = 42" \
  --schema-file tributary.schema.yaml
```

Flags:

- `--source-dsn string` (or env `TRIBUTARY_SOURCE_DSN`) — required
- `--target-dsn string` (or env `TRIBUTARY_TARGET_DSN`) — required
- `--seed-table string` — required
- `--seed-predicate string` — required
- `--schema-file string` — optional
- `--strict-cycles`
- `--include-upstream`
- `--fresh`
- `--no-create-schema`
- `--state-db string` (default `./.tributary/state.db`)
- `--no-resume`
- `--format text|json` (default `text`)

Behavior notes:

- Default write mode is **upsert** (`INSERT ... ON CONFLICT DO UPDATE` by PK).
- Re-running `sync run` with same scope is expected and safe.
- `--fresh` performs subset-scoped cleanup before reload and resets stuck in-progress run state.
- If enabled (default), missing target tables are auto-created from source schema.
- Run checkpointing is stored in local SQLite (`--state-db`) for crash resume.

---

## `tributary.schema.yaml` (declared relationships)

Use this file when real DB constraints do not represent all relationships.

Examples supported:

- soft FK (`from` + `to`)
- composite key (`from` list + `to` list)
- polymorphic association (`from` + `polymorphic_type` + `targets`)
- ignored FK edge (`ignore`)
- cycle handling (`dependency_breaks`)

Start from [`tributary.schema.example.yaml`](tributary.schema.example.yaml).

Example:

```yaml
relations:
  - from: orders.user_id
    to: users.id

  - from: [line_items.order_id, line_items.tenant_id]
    to: [orders.id, orders.tenant_id]

  - from: comments.commentable_id
    polymorphic_type: comments.commentable_type
    targets:
      Post: posts.id
      Photo: photos.id

  - ignore: audit_logs.actor_id

dependency_breaks:
  - table: employees
    column: manager_id
```

Validation rules include:

- each relation must be exactly one shape
- composite key list lengths must match
- malformed `table.column` references fail validation

Unqualified table references default to `public`.

## Schema creation behavior (`sync run`)

When auto-create is enabled (default), Tributary can create missing target tables with:

- columns + data types
- `NOT NULL`
- `PRIMARY KEY`
- real foreign keys
- missing enum type definitions used by copied tables

It does **not** replicate all source DDL. Not covered includes:

- defaults
- sequences/identity
- check constraints
- indexes beyond PK
- triggers
- views
- non-enum custom types (for example domains/composites/ranges)

If an unsupported custom type is needed on target, `sync run` fails with a named preflight error.

## Resume and run identity

`sync run` checkpoints table completion in a local SQLite DB.

- run identity is derived from source/target fingerprints + seed table + seed predicate + schema file hash
- completed/failed runs are reset and reprocessed on new invocation
- in-progress runs resume by skipping already completed tables
- `--fresh` forces reset even for in-progress runs

## Output formats

`plan` and `sync run` support:

- `--format text` (human-readable table output)
- `--format json` (machine-readable)

Warnings (such as unrecognized polymorphic target values) are emitted to stderr.

## Testing

Run all tests:

```sh
make test
# or:
go test ./...
```

Notes:

- Unit tests run without Docker.
- Integration tests in `internal/load` and `cmd/tributary` use `testcontainers-go` with real Postgres and require Docker.
- If Docker is unavailable, those tests self-skip with a clear message.

## Development notes

- `Makefile` targets: `build`, `run`, `tidy`, `test`
- Root command implementation: `cmd/tributary`
- Core packages:
  - `internal/catalog`: schema introspection
  - `internal/graph`: graph build + closure walk
  - `internal/subset`: dependency ordering
  - `internal/load`: schema ensure + copy/upsert load
  - `internal/state`: SQLite checkpointing

## Current limitations

Planned but not implemented yet:

- masking/transform pipeline
- continuous incremental sync via logical replication (`sync watch`)
- observability/hardening phase
- lightweight branching workflow

See [`docs/PLAN.md`](docs/PLAN.md) for roadmap.

## License

MIT — see [LICENSE](LICENSE).
