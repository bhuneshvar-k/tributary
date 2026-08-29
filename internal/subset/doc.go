// Package subset turns a graph closure (internal/graph) into an ordered
// plan: which rows move, and in what order, so foreign key constraints on
// the target never get violated mid-load (a topological sort over the
// dependency graph).
//
// Phase 1 of the project plan.
//
// Not yet implemented.
package subset
