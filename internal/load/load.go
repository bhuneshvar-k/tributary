package load

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
	"github.com/bhuneshvar-k/tributary/internal/graph"
	"github.com/bhuneshvar-k/tributary/internal/state"
)

// LoadOptions configures one Load call. Fresh and (RunID, Store) are
// independent concerns: Fresh controls whether pre-existing target rows
// for this subset are deleted before reload; RunID/Store control per-table
// checkpointing/resume. The caller (cmd/tributary) is expected to have
// already resolved RunID via state.Store.GetOrCreateRun before calling
// Load, and to call MarkRunCompleted/MarkRunFailed afterward — Load itself
// only reads/writes per-table checkpoints, it doesn't own run lifecycle.
type LoadOptions struct {
	Fresh        bool
	CreateSchema bool         // false (--no-create-schema) fails preflight on a missing target table instead of creating it
	RunID        string       // "" disables checkpointing even if Store is set
	Store        *state.Store // nil disables checkpointing/resume entirely
	BatchSize    int          // backfill/delete batch size; <= 0 defaults to 500
}

// TableResult reports what happened for one table during a Load call.
type TableResult struct {
	Table          graph.NodeID
	RowsCopied     int64
	RowsBackfilled int64
	RowsLeftNull   int64 // self-ref rows whose true parent was outside the subset
	SkippedResumed bool  // true if skipped because Store reported it already done
}

// LoadResult is the summary of one Load call.
type LoadResult struct {
	Tables    []TableResult
	TotalRows int64
}

// Load bulk-loads closure into target's database, in the order given by
// order (expected to be subset.TableOrder(g)'s output): first EnsureSchema
// stands up any missing target tables/constraints, then each table with
// rows is loaded in its own transaction (COPY, with any self-referencing
// FK column nulled and backfilled — see loadOneTable).
func Load(ctx context.Context, target *pgx.Conn, sourceSchema *catalog.Schema, g *graph.Graph, closure *graph.Closure, order []graph.NodeID, opts LoadOptions) (*LoadResult, error) {
	if _, err := EnsureSchema(ctx, target, sourceSchema, closure, opts.CreateSchema); err != nil {
		return nil, fmt.Errorf("ensure target schema: %w", err)
	}

	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 500
	}

	if opts.Fresh {
		if err := cleanupFresh(ctx, target, g, closure, order, batchSize); err != nil {
			return nil, fmt.Errorf("--fresh cleanup: %w", err)
		}
	}

	result := &LoadResult{}
	checkpointing := opts.Store != nil && opts.RunID != ""

	for _, table := range order {
		rows := closure.Rows[table]
		if len(rows) == 0 {
			continue
		}

		if checkpointing {
			done, err := opts.Store.IsTableDone(ctx, opts.RunID, string(table))
			if err != nil {
				return nil, fmt.Errorf("check checkpoint for %s: %w", table, err)
			}
			if done {
				result.Tables = append(result.Tables, TableResult{Table: table, SkippedResumed: true})
				continue
			}
		}

		tr, err := loadOneTable(ctx, target, g, table, rows, batchSize)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", table, err)
		}

		if checkpointing {
			if err := opts.Store.MarkTableDone(ctx, opts.RunID, string(table), tr.RowsCopied, tr.RowsBackfilled); err != nil {
				return nil, fmt.Errorf("checkpoint %s: %w", table, err)
			}
		}

		result.Tables = append(result.Tables, tr)
		result.TotalRows += tr.RowsCopied
	}

	return result, nil
}

// loadOneTable upserts every row in rows into table inside its own
// transaction. COPY has no conflict handling, so the true values are
// COPY'd into an unconstrained TEMP staging table first (fast, and never
// hits a constraint since staging has none), then merged into the real
// table with one INSERT ... ON CONFLICT (primary key) DO UPDATE — a row
// that already exists gets updated to match source; a new row gets
// inserted.
//
// Any self-referencing FK column — detected structurally via
// catalog.Table.ForeignKeys, independent of whether graph.Closure ever
// needed to break that edge during traversal (see the package doc
// comment for why) — is forced NULL in the merge (both on insert and on
// conflict-update) and restored via a batched UPDATE afterward, scoped to
// only rows whose true parent is also present in this table's own row set
// (self-reference means the parent lives in the very same table). A row
// whose true parent was excluded from the subset is left permanently
// NULL: a real, documented behavior at the subset boundary, not a bug.
func loadOneTable(ctx context.Context, target *pgx.Conn, g *graph.Graph, table graph.NodeID, rows map[string]graph.RowRef, batchSize int) (TableResult, error) {
	targetTable, ok := g.Nodes[table]
	if !ok {
		return TableResult{}, fmt.Errorf("table %s not found in graph", table)
	}

	selfRefFKs := selfReferencingFKs(targetTable)
	if err := checkNotNullSelfRef(targetTable, selfRefFKs); err != nil {
		return TableResult{}, err
	}

	tx, err := target.Begin(ctx)
	if err != nil {
		return TableResult{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	keys := sortedRowKeys(rows)
	columns := resolveColumnOrder(targetTable, rows, keys)

	values := make([][]any, len(keys))
	for i, k := range keys {
		row := rows[k]
		v := make([]any, len(columns))
		for j, col := range columns {
			v[j] = row.Data[col] // true values: staging has no constraints to violate
		}
		values[i] = v
	}

	stagingTable := stagingTableName(table)
	createStaging := fmt.Sprintf("CREATE TEMP TABLE %s (LIKE %s) ON COMMIT DROP",
		quotedIdent(stagingTable), quotedTable(targetTable.Schema, targetTable.Name))
	if _, err := tx.Exec(ctx, createStaging); err != nil {
		return TableResult{}, fmt.Errorf("create staging table: %w", err)
	}

	if len(values) > 0 {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{stagingTable}, columns, pgx.CopyFromRows(values)); err != nil {
			return TableResult{}, fmt.Errorf("copy into staging table: %w", err)
		}
	}

	nulledColumns := make(map[string]bool)
	for _, fk := range selfRefFKs {
		for _, c := range fk.FromColumns {
			nulledColumns[c] = true
		}
	}

	mergeTag, err := tx.Exec(ctx, mergeSQL(targetTable, columns, nulledColumns, stagingTable))
	if err != nil {
		return TableResult{}, translatePgError(err, table)
	}
	upserted := mergeTag.RowsAffected()

	var backfilled, leftNull int64
	if len(selfRefFKs) > 0 {
		backfilled, leftNull, err = backfillSelfRef(ctx, tx, targetTable, selfRefFKs, rows, keys, batchSize)
		if err != nil {
			return TableResult{}, translatePgError(err, table)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return TableResult{}, fmt.Errorf("commit: %w", err)
	}

	return TableResult{Table: table, RowsCopied: upserted, RowsBackfilled: backfilled, RowsLeftNull: leftNull}, nil
}

func stagingTableName(table graph.NodeID) string {
	return "tributary_staging_" + strings.NewReplacer(".", "_").Replace(string(table))
}

// mergeSQL builds the INSERT ... ON CONFLICT (primary key) DO UPDATE that
// moves rows from stagingTable into t's real table. Primary key columns
// are never included in the UPDATE SET list (updating a row to its own
// conflict key is a no-op); a self-referencing column is always set to
// NULL rather than the staged value, on both the insert and the
// conflict-update path, so it's uniformly ready for backfillSelfRef
// afterward regardless of whether this particular row was new or already
// existed on target.
func mergeSQL(t catalog.Table, columns []string, selfRefColumns map[string]bool, stagingTable string) string {
	pkSet := make(map[string]bool, len(t.PrimaryKey))
	for _, c := range t.PrimaryKey {
		pkSet[c] = true
	}

	destCols := make([]string, len(columns))
	selectCols := make([]string, len(columns))
	for i, c := range columns {
		destCols[i] = quotedIdent(c)
		if selfRefColumns[c] {
			selectCols[i] = "NULL"
		} else {
			selectCols[i] = quotedIdent(c)
		}
	}

	var setClauses []string
	for _, c := range columns {
		if pkSet[c] {
			continue
		}
		if selfRefColumns[c] {
			setClauses = append(setClauses, quotedIdent(c)+" = NULL")
		} else {
			setClauses = append(setClauses, fmt.Sprintf("%s = EXCLUDED.%s", quotedIdent(c), quotedIdent(c)))
		}
	}

	pkCols := make([]string, len(t.PrimaryKey))
	for i, c := range t.PrimaryKey {
		pkCols[i] = quotedIdent(c)
	}

	conflictAction := "DO NOTHING"
	if len(setClauses) > 0 {
		conflictAction = "DO UPDATE SET " + strings.Join(setClauses, ", ")
	}

	return fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s ON CONFLICT (%s) %s",
		quotedTable(t.Schema, t.Name), strings.Join(destCols, ", "), strings.Join(selectCols, ", "),
		quotedIdent(stagingTable), strings.Join(pkCols, ", "), conflictAction)
}

func selfReferencingFKs(t catalog.Table) []catalog.ForeignKey {
	myKey := t.Schema + "." + t.Name
	var fks []catalog.ForeignKey
	for _, fk := range t.ForeignKeys {
		if fk.FromTable == myKey && fk.ToTable == myKey {
			fks = append(fks, fk)
		}
	}
	return fks
}

// checkNotNullSelfRef is the preflight decided on for phase 2: a NOT NULL
// self-referencing column can't be COPY'd NULL, which is the only
// mechanism this loader has for satisfying a same-table FK during bulk
// insert — so it's a hard, named error rather than a silent failure deep
// inside CopyFrom.
func checkNotNullSelfRef(t catalog.Table, fks []catalog.ForeignKey) error {
	colsByName := make(map[string]catalog.Column, len(t.Columns))
	for _, c := range t.Columns {
		colsByName[c.Name] = c
	}
	for _, fk := range fks {
		for _, c := range fk.FromColumns {
			if col, ok := colsByName[c]; ok && !col.IsNullable {
				return fmt.Errorf(
					"table %s.%s has a NOT NULL self-referencing column %q (constraint %s); Tributary cannot bulk-load cyclic self-references into a NOT NULL column",
					t.Schema, t.Name, c, fk.ConstraintName)
			}
		}
	}
	return nil
}

func sortedRowKeys(rows map[string]graph.RowRef) []string {
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolveColumnOrder picks CopyFrom's column order once, from the target
// table's ordinal column order, intersected with the columns actually
// present in the row data — so every row's value slice lines up with the
// same column list regardless of Go map iteration order.
func resolveColumnOrder(t catalog.Table, rows map[string]graph.RowRef, keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	sample := rows[keys[0]].Data
	cols := make([]string, 0, len(t.Columns))
	for _, c := range t.Columns {
		if _, ok := sample[c.Name]; ok {
			cols = append(cols, c.Name)
		}
	}
	return cols
}

// backfillSelfRef restores each row's true self-referencing FK value(s),
// but only where the true parent is present in rows (this table's own
// subset) — a row whose true parent was excluded stays NULL, counted in
// leftNull rather than backfilled.
func backfillSelfRef(ctx context.Context, tx pgx.Tx, t catalog.Table, fks []catalog.ForeignKey, rows map[string]graph.RowRef, keys []string, batchSize int) (backfilled, leftNull int64, err error) {
	presentByFK := make(map[string]map[string]bool, len(fks))
	for _, fk := range fks {
		set := make(map[string]bool, len(keys))
		for _, k := range keys {
			set[valueKey(rows[k].Data, fk.ToColumns)] = true
		}
		presentByFK[fk.ConstraintName] = set
	}

	type pendingUpdate struct {
		setCols []string
		setVals []any
		pkVals  []any
	}
	var updates []pendingUpdate

	for _, k := range keys {
		row := rows[k]
		for _, fk := range fks {
			vals := make([]any, len(fk.FromColumns))
			hasValue := true
			for i, c := range fk.FromColumns {
				v := row.Data[c]
				if v == nil {
					hasValue = false
					break
				}
				vals[i] = v
			}
			if !hasValue {
				continue // was already null on source; nothing to restore
			}
			if !presentByFK[fk.ConstraintName][valueKey(row.Data, fk.FromColumns)] {
				leftNull++
				continue
			}
			pkVals := make([]any, len(t.PrimaryKey))
			for i, c := range t.PrimaryKey {
				pkVals[i] = row.Data[c]
			}
			updates = append(updates, pendingUpdate{setCols: fk.FromColumns, setVals: vals, pkVals: pkVals})
		}
	}

	if len(updates) == 0 {
		return 0, leftNull, nil
	}

	tableSQL := quotedTable(t.Schema, t.Name)
	pkCols := make([]string, len(t.PrimaryKey))
	for i, c := range t.PrimaryKey {
		pkCols[i] = quotedIdent(c)
	}

	for start := 0; start < len(updates); start += batchSize {
		end := min(start+batchSize, len(updates))
		batch := &pgx.Batch{}
		for _, u := range updates[start:end] {
			args := make([]any, 0, len(u.setCols)+len(u.pkVals))
			setClauses := make([]string, len(u.setCols))
			for i, c := range u.setCols {
				setClauses[i] = fmt.Sprintf("%s = $%d", quotedIdent(c), i+1)
				args = append(args, u.setVals[i])
			}
			whereClauses := make([]string, len(pkCols))
			for i, c := range pkCols {
				whereClauses[i] = fmt.Sprintf("%s = $%d", c, len(u.setCols)+i+1)
				args = append(args, u.pkVals[i])
			}
			batch.Queue(fmt.Sprintf("UPDATE %s SET %s WHERE %s", tableSQL, strings.Join(setClauses, ", "), strings.Join(whereClauses, " AND ")), args...)
		}

		results := tx.SendBatch(ctx, batch)
		for range updates[start:end] {
			if _, execErr := results.Exec(); execErr != nil {
				results.Close() //nolint:errcheck
				return backfilled, leftNull, execErr
			}
			backfilled++
		}
		if closeErr := results.Close(); closeErr != nil {
			return backfilled, leftNull, closeErr
		}
	}

	return backfilled, leftNull, nil
}

func valueKey(data map[string]any, cols []string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = fmt.Sprintf("%v", data[c])
	}
	return strings.Join(parts, "\x1f")
}

// cleanupFresh deletes, from target, exactly the rows this subset is about
// to (re)load — by primary key, in reverse table order so children are
// removed before parents — inside one transaction. It never touches rows
// outside this subset (no TRUNCATE).
func cleanupFresh(ctx context.Context, target *pgx.Conn, g *graph.Graph, closure *graph.Closure, order []graph.NodeID, batchSize int) error {
	tx, err := target.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	for i := len(order) - 1; i >= 0; i-- {
		table := order[i]
		rows := closure.Rows[table]
		if len(rows) == 0 {
			continue
		}
		t, ok := g.Nodes[table]
		if !ok || len(t.PrimaryKey) == 0 {
			continue
		}

		keys := sortedRowKeys(rows)
		pkCols := make([]string, len(t.PrimaryKey))
		for i, c := range t.PrimaryKey {
			pkCols[i] = quotedIdent(c)
		}
		tableSQL := quotedTable(t.Schema, t.Name)

		for start := 0; start < len(keys); start += batchSize {
			end := min(start+batchSize, len(keys))
			batch := &pgx.Batch{}
			for _, k := range keys[start:end] {
				row := rows[k]
				args := make([]any, len(t.PrimaryKey))
				where := make([]string, len(t.PrimaryKey))
				for i, c := range t.PrimaryKey {
					where[i] = fmt.Sprintf("%s = $%d", pkCols[i], i+1)
					args[i] = row.Key[c]
				}
				batch.Queue(fmt.Sprintf("DELETE FROM %s WHERE %s", tableSQL, strings.Join(where, " AND ")), args...)
			}

			results := tx.SendBatch(ctx, batch)
			for range keys[start:end] {
				if _, err := results.Exec(); err != nil {
					results.Close() //nolint:errcheck
					return fmt.Errorf("delete existing rows from %s: %w", table, err)
				}
			}
			if err := results.Close(); err != nil {
				return fmt.Errorf("delete existing rows from %s: %w", table, err)
			}
		}
	}

	return tx.Commit(ctx)
}

// translatePgError turns an opaque Postgres error from a bulk load into a
// specific, actionable one naming the constraint and the likely cause,
// rather than surfacing pgx's raw error text.
func translatePgError(err error, table graph.NodeID) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "23505": // unique_violation
		return fmt.Errorf(
			"row conflict in %s: a row with the same primary key already exists on target with different data (constraint %s) — pass --fresh to reload, or point at a different target: %w",
			table, pgErr.ConstraintName, err)
	case "23503": // foreign_key_violation
		return fmt.Errorf(
			"foreign key violation loading %s (constraint %s): the referenced row was not loaded in this subset — likely a pre-existing dangling reference in source data, not a Tributary bug: %w",
			table, pgErr.ConstraintName, err)
	default:
		return err
	}
}
