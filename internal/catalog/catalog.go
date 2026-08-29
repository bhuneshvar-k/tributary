// Package catalog inspects a Postgres database's schema: tables, columns,
// and foreign key constraints. It is the data source for the FK Graph
// Resolver (internal/graph) — see phase 0 and phase 1 of the project plan.
package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Column describes a single column of a table.
type Column struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	IsNullable bool   `json:"is_nullable"`
}

// ForeignKey describes a foreign key constraint discovered in pg_catalog.
// It always has at least one column pair; composite keys have more than one,
// ordered to match.
type ForeignKey struct {
	ConstraintName string   `json:"constraint_name"`
	FromTable      string   `json:"from_table"`
	FromColumns    []string `json:"from_columns"`
	ToTable        string   `json:"to_table"`
	ToColumns      []string `json:"to_columns"`
}

// Table describes a single table: its columns and any outgoing foreign keys.
type Table struct {
	Schema      string       `json:"schema"`
	Name        string       `json:"name"`
	Columns     []Column     `json:"columns"`
	ForeignKeys []ForeignKey `json:"foreign_keys"`
}

// Schema is the full introspected shape of a database: every table, its
// columns, and every FK constraint pg_catalog actually knows about.
//
// This is deliberately only half the picture. See internal/graph for how
// this gets merged with a user-declared relations file to cover
// application-level relationships (Rails/ActiveRecord-style FKs, polymorphic
// associations) that pg_catalog can't see at all — that merge is phase 1.
type Schema struct {
	Tables []Table `json:"tables"`
}

// Inspect connects to the given Postgres database and walks pg_catalog to
// build a full picture of its tables, columns, and foreign keys.
func Inspect(ctx context.Context, connString string) (*Schema, error) {
	conn, err := pgx.Connect(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	tables, err := fetchTables(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("fetch tables: %w", err)
	}

	fks, err := fetchForeignKeys(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("fetch foreign keys: %w", err)
	}

	byTable := make(map[string]*Table, len(tables))
	for i := range tables {
		byTable[key(tables[i].Schema, tables[i].Name)] = &tables[i]
	}
	for _, fk := range fks {
		if t, ok := byTable[fk.FromTable]; ok {
			t.ForeignKeys = append(t.ForeignKeys, fk)
		}
	}

	return &Schema{Tables: tables}, nil
}

func key(schema, name string) string {
	return schema + "." + name
}

// fetchTables reads every table and column from information_schema,
// skipping the system schemas.
func fetchTables(ctx context.Context, conn *pgx.Conn) ([]Table, error) {
	const q = `
		select table_schema, table_name, column_name, data_type, is_nullable
		from information_schema.columns
		where table_schema not in ('pg_catalog', 'information_schema')
		order by table_schema, table_name, ordinal_position;
	`
	rows, err := conn.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byTable := make(map[string]*Table)
	var order []string

	for rows.Next() {
		var schema, table, column, dataType, nullable string
		if err := rows.Scan(&schema, &table, &column, &dataType, &nullable); err != nil {
			return nil, err
		}
		k := key(schema, table)
		t, ok := byTable[k]
		if !ok {
			t = &Table{Schema: schema, Name: table}
			byTable[k] = t
			order = append(order, k)
		}
		t.Columns = append(t.Columns, Column{
			Name:       column,
			Type:       dataType,
			IsNullable: nullable == "YES",
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	tables := make([]Table, 0, len(order))
	for _, k := range order {
		tables = append(tables, *byTable[k])
	}
	return tables, nil
}

// fetchForeignKeys returns every FK constraint pg_catalog knows about,
// including composite keys — grouped by constraint name and ordered by
// column position via unnest(conkey, confkey) WITH ORDINALITY, which is the
// part that's easy to get wrong (a naive join loses the pairing between
// from-columns and to-columns on multi-column keys).
func fetchForeignKeys(ctx context.Context, conn *pgx.Conn) ([]ForeignKey, error) {
	const q = `
		select
			con.conname,
			nsp.nspname || '.' || rel.relname as from_table,
			att.attname as from_column,
			fnsp.nspname || '.' || frel.relname as to_table,
			fatt.attname as to_column,
			ord.ordinality
		from pg_constraint con
		join pg_class rel on rel.oid = con.conrelid
		join pg_namespace nsp on nsp.oid = rel.relnamespace
		join pg_class frel on frel.oid = con.confrelid
		join pg_namespace fnsp on fnsp.oid = frel.relnamespace
		join lateral unnest(con.conkey, con.confkey) with ordinality as ord(from_attnum, to_attnum, ordinality) on true
		join pg_attribute att on att.attrelid = con.conrelid and att.attnum = ord.from_attnum
		join pg_attribute fatt on fatt.attrelid = con.confrelid and fatt.attnum = ord.to_attnum
		where con.contype = 'f'
		order by con.conname, ord.ordinality;
	`
	rows, err := conn.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byName := make(map[string]*ForeignKey)
	var order []string

	for rows.Next() {
		var name, fromTable, fromCol, toTable, toCol string
		var ordinality int
		if err := rows.Scan(&name, &fromTable, &fromCol, &toTable, &toCol, &ordinality); err != nil {
			return nil, err
		}
		fk, ok := byName[name]
		if !ok {
			fk = &ForeignKey{ConstraintName: name, FromTable: fromTable, ToTable: toTable}
			byName[name] = fk
			order = append(order, name)
		}
		fk.FromColumns = append(fk.FromColumns, fromCol)
		fk.ToColumns = append(fk.ToColumns, toCol)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	fks := make([]ForeignKey, 0, len(order))
	for _, n := range order {
		fks = append(fks, *byName[n])
	}
	return fks, nil
}
