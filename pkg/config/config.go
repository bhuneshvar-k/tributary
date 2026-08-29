package config

// Seed identifies the starting point for a subset: a table and a raw SQL
// predicate fragment selecting the seed rows within it (e.g. "id = 42" or
// "team_id = 7"). It is populated from CLI flags in phase 1; a later phase
// may promote it into a YAML-driven run config alongside masking rules and
// target connection details.
type Seed struct {
	Table     string
	Predicate string
}
