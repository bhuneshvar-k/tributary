package load

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
	"github.com/bhuneshvar-k/tributary/internal/graph"
)

// SchemaReport summarizes what EnsureSchema did.
type SchemaReport struct {
	TypesCreated       []string
	TablesCreated      []graph.NodeID
	ConstraintsCreated []string
	Warnings           []string
}

// EnsureSchema makes sure every table with rows in closure exists on the
// target, creating missing ones — columns, types, NOT NULL, PRIMARY KEY —
// from sourceSchema, then adding real FOREIGN KEY constraints in a second
// pass once every table involved exists (so self-referencing and even
// multi-table-cyclic constraints are simply added after the fact, with no
// creation-order problem at all — that problem only exists for the row
// data in Load, not for DDL).
//
// FK constraints are read from catalog.Table.ForeignKeys directly — the
// real source constraints — not from graph.Graph: a schema-file `ignore:`
// only affects subset-closure traversal, and must never suppress
// replicating a real constraint onto the target.
//
// Tables that already exist on target are validated for column/nullability
// compatibility (see preflightCompatible) rather than altered. Everything
// this function creates runs in one transaction: a schema that fails
// partway through creation is left completely uncreated, not half-built.
//
// Not replicated, by design (see internal/catalog's doc comment on
// Column): defaults, sequences/identity, check constraints, indexes beyond
// the implicit PK index, triggers, views, and most custom type
// *definitions*. The one exception is enum types: a column using one
// (data_type = "USER-DEFINED", sourceSchema.Enums has its labels) gets
// that type auto-created on target if it's missing, since enums are
// common and trivially faithful to recreate (CREATE TYPE ... AS ENUM with
// the same labels in the same order). A domain, composite, or range type —
// USER-DEFINED but absent from sourceSchema.Enums — still requires the
// target to already have it; EnsureSchema fails with a specific, named
// error rather than a raw "type does not exist" DDL error.
// createMissing controls what happens when a needed table doesn't exist on
// target: true auto-creates it (the default CLI behavior); false — set via
// --no-create-schema — fails preflight instead, naming every missing
// table, for callers who've deliberately pre-provisioned their own target
// schema and want a strict compatibility check rather than Tributary
// standing anything up.
func EnsureSchema(ctx context.Context, target *pgx.Conn, sourceSchema *catalog.Schema, closure *graph.Closure, createMissing bool) (*SchemaReport, error) {
	targetSchema, err := catalog.InspectConn(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("inspect target schema: %w", err)
	}

	sourceByKey := make(map[graph.NodeID]catalog.Table, len(sourceSchema.Tables))
	for _, t := range sourceSchema.Tables {
		sourceByKey[graph.NodeID(t.Schema+"."+t.Name)] = t
	}
	targetByKey := make(map[graph.NodeID]catalog.Table, len(targetSchema.Tables))
	for _, t := range targetSchema.Tables {
		targetByKey[graph.NodeID(t.Schema+"."+t.Name)] = t
	}

	var needed []graph.NodeID
	for table, rows := range closure.Rows {
		if len(rows) > 0 {
			needed = append(needed, table)
		}
	}

	report := &SchemaReport{}

	var missing []catalog.Table
	for _, table := range needed {
		if _, ok := targetByKey[table]; ok {
			if err := preflightCompatible(targetByKey[table], sourceByKey[table]); err != nil {
				return nil, fmt.Errorf("target table %s is incompatible with the subset being loaded: %w", table, err)
			}
			continue
		}
		src, ok := sourceByKey[table]
		if !ok {
			return nil, fmt.Errorf("internal error: table %s has rows in the closure but is missing from the source schema", table)
		}
		missing = append(missing, src)
	}

	if len(missing) == 0 {
		return report, nil
	}

	if !createMissing {
		names := make([]string, len(missing))
		for i, t := range missing {
			names[i] = t.Schema + "." + t.Name
		}
		return nil, fmt.Errorf("target is missing %d table(s) needed by this subset and --no-create-schema was set: %s",
			len(missing), strings.Join(names, ", "))
	}

	tx, err := target.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin schema-creation transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if already committed

	createdTypes, err := ensureCustomTypesExist(ctx, tx, sourceSchema.Enums, missing)
	if err != nil {
		return nil, err
	}
	report.TypesCreated = createdTypes

	createdSchemas := map[string]bool{}
	for _, t := range missing {
		if t.Schema != "public" && !createdSchemas[t.Schema] {
			if _, err := tx.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quotedIdent(t.Schema))); err != nil {
				return nil, fmt.Errorf("create schema %q on target: %w", t.Schema, err)
			}
			createdSchemas[t.Schema] = true
		}

		if _, err := tx.Exec(ctx, createTableSQL(t)); err != nil {
			return nil, fmt.Errorf("create table %s.%s on target: %w", t.Schema, t.Name, err)
		}
		report.TablesCreated = append(report.TablesCreated, graph.NodeID(t.Schema+"."+t.Name))

		// This table now exists on target for the FK pass below, whether or
		// not other missing tables still need creating.
		targetByKey[graph.NodeID(t.Schema+"."+t.Name)] = t
	}

	for _, t := range missing {
		for _, fk := range t.ForeignKeys {
			if _, ok := targetByKey[graph.NodeID(fk.ToTable)]; !ok {
				report.Warnings = append(report.Warnings, fmt.Sprintf(
					"did not create FK constraint %s (%s -> %s): referenced table is outside this subset and does not exist on target",
					fk.ConstraintName, fk.FromTable, fk.ToTable))
				continue
			}
			if _, err := tx.Exec(ctx, addForeignKeySQL(fk)); err != nil {
				return nil, fmt.Errorf("add foreign key %s on target: %w", fk.ConstraintName, err)
			}
			report.ConstraintsCreated = append(report.ConstraintsCreated, fk.ConstraintName)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit schema creation: %w", err)
	}

	return report, nil
}

// preflightCompatible checks that every column the subset actually carries
// data for exists on the target table with compatible nullability. It does
// not require the target to have every source column (extra target-only
// columns are fine), only that nothing the loader needs to write is
// missing or would silently reject a value.
func preflightCompatible(target, source catalog.Table) error {
	targetCols := make(map[string]catalog.Column, len(target.Columns))
	for _, c := range target.Columns {
		targetCols[c.Name] = c
	}
	for _, sc := range source.Columns {
		tc, ok := targetCols[sc.Name]
		if !ok {
			return fmt.Errorf("column %q exists in the subset's data but not on the target table", sc.Name)
		}
		if sc.IsNullable && !tc.IsNullable {
			return fmt.Errorf("column %q is nullable in source but NOT NULL on target", sc.Name)
		}
	}
	return nil
}

// ensureCustomTypesExist checks every USER-DEFINED column in tables
// against target: a missing type that's a known enum (present in
// sourceEnums) is created (CREATE TYPE ... AS ENUM, same labels, same
// order) and its name returned in created; a missing type that isn't a
// known enum — a domain, composite, or range — is a hard, named error,
// since Tributary doesn't know how to recreate those faithfully.
func ensureCustomTypesExist(ctx context.Context, tx pgx.Tx, sourceEnums map[string][]string, tables []catalog.Table) (created []string, err error) {
	checked := map[string]bool{}
	for _, t := range tables {
		for _, c := range t.Columns {
			if c.Type != "USER-DEFINED" || checked[c.UDTName] {
				continue
			}
			checked[c.UDTName] = true

			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_type WHERE typname = $1)`, c.UDTName).Scan(&exists); err != nil {
				return created, fmt.Errorf("check custom type %q on target: %w", c.UDTName, err)
			}
			if exists {
				continue
			}

			labels, isEnum := sourceEnums[c.UDTName]
			if !isEnum {
				return created, fmt.Errorf(
					"column %s.%s uses type %q, which does not exist on the target and isn't a recognized enum — create it before running sync (Tributary auto-creates missing enum types, but not domains/composites/ranges)",
					graph.NodeID(t.Schema+"."+t.Name), c.Name, c.UDTName)
			}

			if _, err := tx.Exec(ctx, createEnumTypeSQL(c.UDTName, labels)); err != nil {
				return created, fmt.Errorf("create enum type %q on target: %w", c.UDTName, err)
			}
			created = append(created, c.UDTName)
		}
	}
	return created, nil
}

func createEnumTypeSQL(name string, labels []string) string {
	quoted := make([]string, len(labels))
	for i, l := range labels {
		quoted[i] = "'" + strings.ReplaceAll(l, "'", "''") + "'"
	}
	return fmt.Sprintf("CREATE TYPE %s AS ENUM (%s)", quotedIdent(name), strings.Join(quoted, ", "))
}

func createTableSQL(t catalog.Table) string {
	cols := make([]string, 0, len(t.Columns)+1)
	for _, c := range t.Columns {
		col := quotedIdent(c.Name) + " " + columnTypeSQL(c)
		if !c.IsNullable {
			col += " NOT NULL"
		}
		cols = append(cols, col)
	}
	if len(t.PrimaryKey) > 0 {
		pkCols := make([]string, len(t.PrimaryKey))
		for i, c := range t.PrimaryKey {
			pkCols[i] = quotedIdent(c)
		}
		cols = append(cols, "PRIMARY KEY ("+strings.Join(pkCols, ", ")+")")
	}
	return fmt.Sprintf("CREATE TABLE %s (\n\t%s\n)", quotedTable(t.Schema, t.Name), strings.Join(cols, ",\n\t"))
}

func addForeignKeySQL(fk catalog.ForeignKey) string {
	fromCols := make([]string, len(fk.FromColumns))
	for i, c := range fk.FromColumns {
		fromCols[i] = quotedIdent(c)
	}
	toCols := make([]string, len(fk.ToColumns))
	for i, c := range fk.ToColumns {
		toCols[i] = quotedIdent(c)
	}
	return fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
		quotedTableKey(fk.FromTable), quotedIdent(fk.ConstraintName),
		strings.Join(fromCols, ", "), quotedTableKey(fk.ToTable), strings.Join(toCols, ", "))
}

// columnTypeSQL renders a column's DDL type, reattaching length/precision/
// scale where Postgres's information_schema.columns.data_type strips it
// (e.g. "character varying" alone, without the (n)) — see internal/catalog
// Column's doc comment. A USER-DEFINED column renders as its underlying
// type name (ensureCustomTypesExist has already confirmed or created it on
// target by the time this is called).
func columnTypeSQL(c catalog.Column) string {
	switch c.Type {
	case "character varying", "character", "varbit", "bit":
		if c.CharMaxLength != nil {
			return fmt.Sprintf("%s(%d)", c.Type, *c.CharMaxLength)
		}
		return c.Type
	case "numeric", "decimal":
		if c.NumericPrecision != nil && c.NumericScale != nil {
			return fmt.Sprintf("numeric(%d,%d)", *c.NumericPrecision, *c.NumericScale)
		}
		return c.Type
	case "USER-DEFINED":
		return quotedIdent(c.UDTName)
	default:
		return c.Type
	}
}

func quotedTable(schema, name string) string {
	return pgx.Identifier{schema, name}.Sanitize()
}

// quotedTableKey quotes a "schema.table" string (catalog.ForeignKey's
// FromTable/ToTable format) as a schema-qualified identifier.
func quotedTableKey(key string) string {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		return pgx.Identifier{key}.Sanitize()
	}
	return pgx.Identifier{parts[0], parts[1]}.Sanitize()
}

func quotedIdent(name string) string {
	return pgx.Identifier{name}.Sanitize()
}
