package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// RunKey identifies one logical sync configuration: the same source,
// target, seed, and schema file always hash to the same ID, regardless of
// how many times sync run is invoked against them. Fresh is deliberately
// not part of the hash — a --fresh invocation reuses the same run
// identity (see Store.GetOrCreateRun) rather than minting an unrelated
// one, so run history stays traceable under one ID per configuration.
type RunKey struct {
	SourceFingerprint string
	TargetFingerprint string
	SeedTable         string
	SeedPredicate     string
	SchemaFileHash    string
}

// ID returns a stable identifier for this configuration.
func (k RunKey) ID() string {
	h := sha256.New()
	for _, s := range []string{
		k.SourceFingerprint, k.TargetFingerprint, k.SeedTable, k.SeedPredicate, k.SchemaFileHash,
	} {
		h.Write([]byte(s))
		h.Write([]byte{0}) // separator: avoids "ab"+"c" colliding with "a"+"bc"
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Run is one row of sync run's history for a given RunKey.
type Run struct {
	ID          string
	Status      string // "in_progress" | "completed" | "failed"
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// Store is a checkpoint store backed by a single SQLite file.
type Store struct {
	db *sql.DB
}

const schemaDDL = `
CREATE TABLE IF NOT EXISTS runs (
	run_id             TEXT PRIMARY KEY,
	created_at         TEXT NOT NULL,
	completed_at       TEXT,
	source_fingerprint TEXT NOT NULL,
	target_fingerprint TEXT NOT NULL,
	seed_table         TEXT NOT NULL,
	seed_predicate     TEXT NOT NULL,
	schema_file_hash   TEXT NOT NULL,
	status             TEXT NOT NULL,
	failure_reason     TEXT
);

CREATE TABLE IF NOT EXISTS run_tables (
	run_id          TEXT NOT NULL REFERENCES runs(run_id),
	table_name      TEXT NOT NULL,
	status          TEXT NOT NULL,
	rows_copied     INTEGER NOT NULL,
	rows_backfilled INTEGER NOT NULL,
	completed_at    TEXT NOT NULL,
	PRIMARY KEY (run_id, table_name)
);
`

// Open opens (creating if necessary) the SQLite checkpoint store at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}
	if _, err := db.Exec(schemaDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("init state db: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

const timeLayout = time.RFC3339Nano

// GetOrCreateRun looks up the run identified by key.ID(). A brand-new
// identity gets a fresh "in_progress" row.
//
// sync run upserts by default, so re-invoking it is meant to just refresh
// the target rather than be blocked — an existing run whose status is
// "completed" or "failed" therefore always has its table-level checkpoints
// wiped and its status reset to "in_progress", regardless of forceReset,
// so the caller's table loop reprocesses every table (safe: the loader
// upserts, it doesn't blindly re-insert).
//
// The one case checkpoints are preserved is an existing "in_progress" run
// — a genuine crash-resume candidate — which is returned as-is so the
// caller's table loop can skip tables already marked done. forceReset
// overrides even that (used by --fresh, which also wipes target rows
// separately, to abandon a stuck in-progress attempt and start clean).
func (s *Store) GetOrCreateRun(ctx context.Context, key RunKey, forceReset bool) (*Run, error) {
	id := key.ID()

	run, err := s.getRun(ctx, id)
	if err != nil {
		return nil, err
	}

	if run == nil {
		now := time.Now().UTC()
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO runs (run_id, created_at, source_fingerprint, target_fingerprint, seed_table, seed_predicate, schema_file_hash, status)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'in_progress')`,
			id, now.Format(timeLayout), key.SourceFingerprint, key.TargetFingerprint, key.SeedTable, key.SeedPredicate, key.SchemaFileHash,
		); err != nil {
			return nil, fmt.Errorf("create run: %w", err)
		}
		return &Run{ID: id, Status: "in_progress", CreatedAt: now}, nil
	}

	if run.Status == "in_progress" && !forceReset {
		return run, nil // resume: crash recovery
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM run_tables WHERE run_id = ?`, id); err != nil {
		return nil, fmt.Errorf("reset run_tables: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE runs SET status = 'in_progress', completed_at = NULL, failure_reason = NULL WHERE run_id = ?`, id); err != nil {
		return nil, fmt.Errorf("reset run status: %w", err)
	}
	run.Status = "in_progress"
	run.CompletedAt = nil

	return run, nil
}

func (s *Store) getRun(ctx context.Context, id string) (*Run, error) {
	row := s.db.QueryRowContext(ctx, `SELECT run_id, created_at, completed_at, status FROM runs WHERE run_id = ?`, id)

	var r Run
	var createdAt string
	var completedAt sql.NullString
	if err := row.Scan(&r.ID, &createdAt, &completedAt, &r.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query run: %w", err)
	}

	t, err := time.Parse(timeLayout, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	r.CreatedAt = t

	if completedAt.Valid {
		ct, err := time.Parse(timeLayout, completedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse completed_at: %w", err)
		}
		r.CompletedAt = &ct
	}

	return &r, nil
}

// IsTableDone reports whether table's load was already checkpointed
// complete for runID.
func (s *Store) IsTableDone(ctx context.Context, runID, table string) (bool, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM run_tables WHERE run_id = ? AND table_name = ?`, runID, table).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query table checkpoint: %w", err)
	}
	return status == "done", nil
}

// MarkTableDone records table's load as complete. Callers must only call
// this after table's transaction has committed — see the package doc
// comment for why an earlier "copied but not backfilled" checkpoint would
// be meaningless.
func (s *Store) MarkTableDone(ctx context.Context, runID, table string, rowsCopied, rowsBackfilled int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO run_tables (run_id, table_name, status, rows_copied, rows_backfilled, completed_at)
		VALUES (?, ?, 'done', ?, ?, ?)
		ON CONFLICT(run_id, table_name) DO UPDATE SET
			status = 'done', rows_copied = excluded.rows_copied,
			rows_backfilled = excluded.rows_backfilled, completed_at = excluded.completed_at`,
		runID, table, rowsCopied, rowsBackfilled, time.Now().UTC().Format(timeLayout),
	)
	if err != nil {
		return fmt.Errorf("mark table done: %w", err)
	}
	return nil
}

// MarkRunCompleted marks the whole run successful.
func (s *Store) MarkRunCompleted(ctx context.Context, runID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET status = 'completed', completed_at = ? WHERE run_id = ?`,
		time.Now().UTC().Format(timeLayout), runID)
	if err != nil {
		return fmt.Errorf("mark run completed: %w", err)
	}
	return nil
}

// MarkRunFailed marks the run failed, recording reason for a human
// resuming later to see what went wrong without re-reading logs.
func (s *Store) MarkRunFailed(ctx context.Context, runID, reason string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET status = 'failed', failure_reason = ? WHERE run_id = ?`,
		reason, runID)
	if err != nil {
		return fmt.Errorf("mark run failed: %w", err)
	}
	return nil
}
