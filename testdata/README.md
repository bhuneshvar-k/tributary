# testdata

Fixture schemas for `testcontainers-go` integration tests — real Postgres in
Docker per test, per the project plan. Subset and replication correctness
can't be trusted to mocks.

Nothing here yet; the first fixture lands with phase 1 (FK graph + subset
closure tests), including a schema with a self-referencing table and a
composite foreign key, since those are the cases most likely to break the
resolver.
