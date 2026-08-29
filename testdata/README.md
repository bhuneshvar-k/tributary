# testdata

Fixture schemas for `testcontainers-go` integration tests — real Postgres in
Docker per test binary run. Subset and load correctness can't be trusted to
mocks.

## `phase2/`

`source.sql` — the schema `internal/load`'s and `cmd/tributary`'s
integration tests seed a source container from: leaf and single-FK tables,
a composite-key parent/child pair (parent keyed `(tenant_id, id)` so the
composite pairing is actually exercised, not just column names that happen
to be globally unique), a nullable self-referencing table, a `NOT NULL`
self-referencing table, a polymorphic source/target pair, and a column
using a custom Postgres enum type (deliberately not created on the target
database, to exercise the missing-custom-type preflight check).
`orphaned_fk_table` is also declared, shaped for the not-yet-written
orphaned-FK test (see Coverage status below) — a test using it still needs
to insert its dangling row itself (trigger disabled around the insert),
since none of the current tests do. `relations.yaml` declares the
polymorphic association (the only relationship in `source.sql` that isn't
a real FK constraint). All table names are generic/structural
(`leaf_table`, `self_ref_table`, etc.), not a specific business domain —
a prior fixture set using business-domain names (orders/line_items/
employees) was removed at the project's own request.

Exercised by `internal/load/load_test.go` and `cmd/tributary/sync_test.go`,
each starting **two** Postgres containers (source, seeded from
`source.sql`; target, empty — `EnsureSchema` stands it up as needed) per
test binary run, shared across tests via non-overlapping id ranges per
table.

## Coverage status

`internal/load` and `cmd/tributary`'s core phase-2 logic (schema
auto-creation, composite-key loading, self-referencing null-then-backfill
in both the full-DAG and excluded-parent cases, the `NOT NULL`
self-reference preflight error, polymorphic loading, and upsert-on-rerun —
a changed source row updates in place on target rather than erroring or
duplicating) has integration test coverage. **Not yet covered** by an
automated test: crash/resume behavior (interrupting a run mid-sequence or
mid-transaction and verifying it picks back up correctly), the
orphaned-FK error-translation path (`orphaned_fk_table` is declared but no
test seeds its dangling row yet), and a `--no-create-schema` /
pre-existing-target-table run. `internal/graph` and `internal/subset`
(phase 1) currently have no test coverage of their own — the prior
fixture there was removed and not yet replaced.

Requires Docker. If none is reachable, each `TestMain` logs why and every
test in that package skips itself individually (`t.Skip`) rather than
failing outright.

No CI workflow exists yet in this repo to run these automatically —
GitHub Actions' `ubuntu-latest` runners have Docker available by default,
so a future workflow shouldn't need special setup for this.
