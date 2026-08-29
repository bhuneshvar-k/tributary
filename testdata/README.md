# testdata

Fixture schemas for `testcontainers-go` integration tests — real Postgres in
a shared container per test binary run, per the project plan. Subset and
replication correctness can't be trusted to mocks.

- `fixture.sql` — the phase 1 schema: a composite FK (`line_items` →
  `orders`, with `orders` keyed `(tenant_id, id)` so the composite pairing
  is actually exercised, not just column names that happen to be globally
  unique), a self-referencing table (`employees.manager_id`), a soft FK
  with no DB constraint (`orders.user_id` → `users.id`), a polymorphic
  pair (`comments` → `posts`/`photos`), and a real FK meant to be ignored
  (`audit_logs.actor_id`).
- `relations.yaml` — the declared side of the above (soft FK, polymorphic,
  ignore) against `fixture.sql`. `dependency_breaks` for `employees` is
  deliberately left out here; `internal/graph`'s cycle tests construct
  that case programmatically to exercise both the best-effort-auto-break
  and explicit-break paths against the same base relations.

Exercised by `internal/graph/closure_test.go`, which starts one Postgres
container per test binary run (loaded from `fixture.sql`) and shares it
across every test — closure computation only reads, never mutates, so this
is safe as long as each test uses a non-overlapping id range.

Requires Docker. If none is reachable, `TestMain` logs why and the
Docker-dependent tests skip themselves individually (`t.Skip`) rather than
failing the whole package — the pure in-memory tests elsewhere in
`internal/graph` (graph merge/build, cycle detection) don't need a
container and always run.

No CI workflow exists yet in this repo to run these automatically —
GitHub Actions' `ubuntu-latest` runners have Docker available by default,
so a future workflow shouldn't need special setup for this.
