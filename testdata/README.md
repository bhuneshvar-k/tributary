# testdata

Fixture schemas for `testcontainers-go` integration tests — real Postgres in
Docker per test, per the project plan. Subset and replication correctness
can't be trusted to mocks.

Nothing here right now: the previous fixture set (an example order/
line_item/employee schema exercising composite keys, self-references,
polymorphic associations, and soft FKs) was removed. `internal/graph`'s FK
graph, merge, and closure logic (`Build`, `ComputeClosure`,
`internal/subset.TableOrder`) has no test coverage until a replacement
fixture lands here, using generic table names rather than a specific
business domain.
