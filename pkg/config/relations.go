package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ColumnRef identifies one or more columns on a single table, as declared
// in YAML by a scalar "table.column" / "schema.table.column" string, or a
// list of such strings sharing the same table (for composite keys). An
// unqualified table name defaults to the "public" schema.
//
// A malformed reference does not fail YAML decoding immediately — it's
// recorded in err and surfaced later by SchemaFile.Validate, so one bad
// reference doesn't prevent every other problem in the file from being
// reported in the same pass.
type ColumnRef struct {
	Schema  string
	Table   string
	Columns []string

	err error
}

// TableKey returns the schema-qualified table identifier ("schema.table"),
// matching the format internal/catalog uses to key tables.
func (c ColumnRef) TableKey() string {
	return c.Schema + "." + c.Table
}

func (c ColumnRef) String() string {
	if len(c.Columns) == 1 {
		return c.TableKey() + "." + c.Columns[0]
	}
	return c.TableKey() + ".[" + strings.Join(c.Columns, ",") + "]"
}

// Err reports a format error recorded while decoding this reference, if
// any. SchemaFile.Validate surfaces this; callers that skip Validate
// should check it themselves before trusting Schema/Table/Columns.
func (c ColumnRef) Err() error {
	return c.err
}

// UnmarshalYAML accepts either a scalar "table.column" string or a
// sequence of such strings, all referencing the same table, for composite
// keys. Malformed references are recorded on c.err rather than returned,
// so Validate can aggregate them alongside every other problem in the
// file instead of aborting the whole parse on the first bad string.
func (c *ColumnRef) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err != nil {
			return err
		}
		schema, table, column, err := parseTableColumn(s)
		if err != nil {
			c.err = err
			return nil
		}
		c.Schema, c.Table, c.Columns = schema, table, []string{column}
		return nil

	case yaml.SequenceNode:
		var raw []string
		if err := node.Decode(&raw); err != nil {
			return err
		}
		if len(raw) == 0 {
			c.err = fmt.Errorf("column reference list must not be empty")
			return nil
		}
		cols := make([]string, 0, len(raw))
		var schema, table string
		for i, s := range raw {
			sch, tbl, col, err := parseTableColumn(s)
			if err != nil {
				c.err = err
				return nil
			}
			if i == 0 {
				schema, table = sch, tbl
			} else if sch != schema || tbl != table {
				c.err = fmt.Errorf("composite reference %v must all reference the same table, got %q and %q",
					raw, schema+"."+table, sch+"."+tbl)
				return nil
			}
			cols = append(cols, col)
		}
		c.Schema, c.Table, c.Columns = schema, table, cols
		return nil

	default:
		c.err = fmt.Errorf("expected a string or a list of strings, got %v", node.Tag)
		return nil
	}
}

// ParseTableColumn splits "table.column" or "schema.table.column" into its
// parts. An unqualified reference defaults to the "public" schema.
// Exported so other packages (internal/graph) can parse plain string
// fields — Relation.Ignore, Relation.PolymorphicType, Relation.Targets
// values — the same way ColumnRef parses its own scalar form.
func ParseTableColumn(s string) (schema, table, column string, err error) {
	return parseTableColumn(s)
}

// parseTableColumn splits "table.column" or "schema.table.column" into its
// parts. An unqualified reference defaults to the "public" schema.
func parseTableColumn(s string) (schema, table, column string, err error) {
	parts := strings.Split(s, ".")
	switch len(parts) {
	case 2:
		return "public", parts[0], parts[1], nil
	case 3:
		return parts[0], parts[1], parts[2], nil
	default:
		return "", "", "", fmt.Errorf(`invalid table.column reference %q: expected "table.column" or "schema.table.column"`, s)
	}
}

// Relation is one entry in a tributary.schema.yaml `relations:` list.
// Exactly one shape is populated: a (possibly composite) foreign key
// (From + To), a polymorphic association (From + PolymorphicType +
// Targets), or a suppression of a real catalog FK (Ignore). Kind
// classifies which.
type Relation struct {
	From *ColumnRef `yaml:"from,omitempty"`
	To   *ColumnRef `yaml:"to,omitempty"`

	PolymorphicType string            `yaml:"polymorphic_type,omitempty"`
	Targets         map[string]string `yaml:"targets,omitempty"`

	Ignore string `yaml:"ignore,omitempty"`
}

// RelationKind classifies which shape a Relation takes.
type RelationKind int

const (
	KindForeignKey RelationKind = iota
	KindPolymorphic
	KindIgnore
)

func (k RelationKind) String() string {
	switch k {
	case KindForeignKey:
		return "foreign key"
	case KindPolymorphic:
		return "polymorphic"
	case KindIgnore:
		return "ignore"
	default:
		return "unknown"
	}
}

// Kind classifies a Relation into exactly one shape, or returns an error
// naming the incomplete or ambiguous combination of fields set.
func (r Relation) Kind() (RelationKind, error) {
	hasIgnore := r.Ignore != ""
	hasPoly := r.PolymorphicType != "" || len(r.Targets) > 0

	switch {
	case hasIgnore && (hasPoly || r.From != nil || r.To != nil):
		return 0, fmt.Errorf("relation must be exactly one of a foreign key, a polymorphic association, or an ignore, but 'ignore' is set alongside other fields")

	case hasIgnore:
		return KindIgnore, nil

	case hasPoly:
		if r.From == nil {
			return 0, fmt.Errorf("polymorphic relation missing 'from'")
		}
		if r.PolymorphicType == "" {
			return 0, fmt.Errorf("polymorphic relation on %s missing 'polymorphic_type'", r.From)
		}
		if len(r.Targets) == 0 {
			return 0, fmt.Errorf("polymorphic relation on %s missing 'targets'", r.From)
		}
		if r.To != nil {
			return 0, fmt.Errorf("polymorphic relation on %s must not also set 'to'", r.From)
		}
		return KindPolymorphic, nil

	case r.From != nil && r.To == nil:
		return 0, fmt.Errorf("relation from=%s missing 'to'", r.From)

	case r.From == nil && r.To != nil:
		return 0, fmt.Errorf("relation to=%s missing 'from'", r.To)

	case r.From != nil && r.To != nil:
		return KindForeignKey, nil

	default:
		return 0, fmt.Errorf("relation has none of 'from'/'to', 'polymorphic_type', or 'ignore' set")
	}
}

// DependencyBreak names one FK column that should be treated as
// deferrable when it participates in a cycle in the FK graph
// (config-driven cycle handling, Condenser-style dependency_breaks).
type DependencyBreak struct {
	Table  string `yaml:"table"`
	Column string `yaml:"column"`
}

// TableKey returns the schema-qualified table identifier, defaulting an
// unqualified table name to the "public" schema — same rule as ColumnRef.
func (d DependencyBreak) TableKey() string {
	if strings.Contains(d.Table, ".") {
		return d.Table
	}
	return "public." + d.Table
}

// SchemaFile is the parsed shape of tributary.schema.yaml: declared
// relations pg_catalog can't see, plus dependency_breaks controlling how
// cycles in the FK graph are handled.
type SchemaFile struct {
	Relations        []Relation        `yaml:"relations"`
	DependencyBreaks []DependencyBreak `yaml:"dependency_breaks"`
}

// LoadSchemaFile reads, parses, and validates a tributary.schema.yaml
// file. It does not check that referenced tables/columns exist in any
// actual database — that cross-check happens in internal/graph, once
// there's a catalog.Schema to check against.
func LoadSchemaFile(path string) (*SchemaFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var sf SchemaFile
	if err := yaml.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := sf.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return &sf, nil
}

// Validate checks internal consistency of the schema file: well-formed
// relation shapes, composite key column-count matches, and well-formed
// dependency_breaks entries. It aggregates every problem found via
// errors.Join rather than stopping at the first, so a user fixing the file
// sees everything wrong in one pass.
func (f *SchemaFile) Validate() error {
	var errs []error

	for i, r := range f.Relations {
		var refErrs []error
		if r.From != nil && r.From.err != nil {
			refErrs = append(refErrs, fmt.Errorf("from: %w", r.From.err))
		}
		if r.To != nil && r.To.err != nil {
			refErrs = append(refErrs, fmt.Errorf("to: %w", r.To.err))
		}
		if len(refErrs) > 0 {
			for _, e := range refErrs {
				errs = append(errs, fmt.Errorf("relations[%d]: %w", i, e))
			}
			continue
		}

		kind, err := r.Kind()
		if err != nil {
			errs = append(errs, fmt.Errorf("relations[%d]: %w", i, err))
			continue
		}

		switch kind {
		case KindForeignKey:
			if len(r.From.Columns) != len(r.To.Columns) {
				errs = append(errs, fmt.Errorf("relations[%d]: from=%s to=%s: composite key length mismatch (%d vs %d)",
					i, r.From, r.To, len(r.From.Columns), len(r.To.Columns)))
			}
			if r.From.TableKey() == r.To.TableKey() && columnsEqual(r.From.Columns, r.To.Columns) {
				errs = append(errs, fmt.Errorf("relations[%d]: from and to are identical (%s); a relation can't reference itself", i, r.From))
			}

		case KindPolymorphic:
			if _, _, _, err := parseTableColumn(r.PolymorphicType); err != nil {
				errs = append(errs, fmt.Errorf("relations[%d]: polymorphic_type: %w", i, err))
			}
			for typeVal, target := range r.Targets {
				if _, _, _, err := parseTableColumn(target); err != nil {
					errs = append(errs, fmt.Errorf("relations[%d]: targets[%q]=%q: %w", i, typeVal, target, err))
				}
			}

		case KindIgnore:
			if _, _, _, err := parseTableColumn(r.Ignore); err != nil {
				errs = append(errs, fmt.Errorf("relations[%d]: ignore: %w", i, err))
			}
		}
	}

	for i, b := range f.DependencyBreaks {
		if b.Table == "" || b.Column == "" {
			errs = append(errs, fmt.Errorf("dependency_breaks[%d]: both 'table' and 'column' are required", i))
		}
	}

	return errors.Join(errs...)
}

func columnsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
