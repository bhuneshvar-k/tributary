// Package config defines the YAML shapes tributary reads: the
// tributary.schema.yaml file — relations and dependency_breaks, see
// relations.go — and the seed predicate, see config.go. Exported under
// pkg/ rather than internal/ because other tools may want to read the
// same config shape without shelling out to the CLI.
//
// Masking rules (internal/mask) and a full run-config file land in a
// later phase; only what phase 1's `tributary plan` needs exists so far.
package config
