// Package graph models the relationship graph a subset is computed over:
// nodes are tables, edges are foreign keys — whether discovered from
// pg_catalog (internal/catalog, see graph.go/merge.go) or declared by hand
// in a tributary.schema.yaml relations file, for schemas that don't
// enforce relationships in the database (Rails/ActiveRecord-style soft
// FKs, polymorphic associations pg_catalog has no way to express).
//
// This is where the FK Graph Resolver (Build, in merge.go) and the subset
// closure algorithm (Closure, in closure.go — not yet implemented) live —
// phase 1 of the project plan, and the hardest, most novel part of the
// whole system. Cycle-breaking (config-driven, Condenser-style
// dependency_breaks) also belongs here.
package graph
