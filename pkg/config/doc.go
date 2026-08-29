// Package config defines the YAML shapes tributary reads: connection
// details, the seed predicate, the tributary.schema.yaml relations file
// (internal/graph), and masking rules (internal/mask). Exported under pkg/
// rather than internal/ because other tools may want to read the same
// config shape without shelling out to the CLI.
//
// Not yet implemented.
package config
