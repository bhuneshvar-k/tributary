// Package graph models the relationship graph a subset is computed over:
// nodes are tables, edges are foreign keys — whether discovered from
// pg_catalog (internal/catalog) or declared by hand in a
// tributary.schema.yaml relations file, for schemas that don't enforce
// relationships in the database (Rails/ActiveRecord-style soft FKs,
// polymorphic associations pg_catalog has no way to express).
//
// This is where the FK Graph Resolver and the subset closure algorithm
// live — phase 1 of the project plan, and the hardest, most novel part of
// the whole system. Cycle-breaking (config-driven, Condenser-style) also
// belongs here.
//
// Not yet implemented.
package graph
