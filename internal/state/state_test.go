package state

import (
	"context"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testKey() RunKey {
	return RunKey{
		SourceFingerprint: "source:5432/db",
		TargetFingerprint: "target:5432/db",
		SeedTable:         "public.users",
		SeedPredicate:     "id = 1",
		SchemaFileHash:    "abc123",
	}
}

func TestRunKeyIDStableAndDistinct(t *testing.T) {
	a := testKey()
	b := testKey()
	if a.ID() != b.ID() {
		t.Fatal("identical RunKeys produced different IDs")
	}

	c := testKey()
	c.SeedPredicate = "id = 2"
	if a.ID() == c.ID() {
		t.Fatal("different RunKeys produced the same ID")
	}
}

func TestGetOrCreateRun_NewRun(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	run, err := s.GetOrCreateRun(ctx, testKey(), false)
	if err != nil {
		t.Fatalf("GetOrCreateRun: %v", err)
	}
	if run.Status != "in_progress" {
		t.Fatalf("Status = %q, want in_progress", run.Status)
	}
	if run.ID != testKey().ID() {
		t.Fatalf("Run.ID = %q, want %q", run.ID, testKey().ID())
	}
}

func TestGetOrCreateRun_ReturnsExistingInProgress(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	key := testKey()

	first, err := s.GetOrCreateRun(ctx, key, false)
	if err != nil {
		t.Fatalf("GetOrCreateRun (1st): %v", err)
	}

	if err := s.MarkTableDone(ctx, first.ID, "public.users", 10, 0); err != nil {
		t.Fatalf("MarkTableDone: %v", err)
	}

	second, err := s.GetOrCreateRun(ctx, key, false)
	if err != nil {
		t.Fatalf("GetOrCreateRun (2nd): %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("resumed run got a different ID: %q vs %q", second.ID, first.ID)
	}

	done, err := s.IsTableDone(ctx, second.ID, "public.users")
	if err != nil {
		t.Fatalf("IsTableDone: %v", err)
	}
	if !done {
		t.Fatal("expected public.users to still be checkpointed done across GetOrCreateRun calls")
	}
}

// sync run upserts by default, so re-fetching a completed run must reset
// it for reprocessing rather than staying "completed" and blocking the
// caller — there's no separate --fresh-only reset path anymore for a
// run that isn't actively in progress.
func TestGetOrCreateRun_CompletedRun_AutomaticallyResetsForReprocessing(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	key := testKey()

	run, err := s.GetOrCreateRun(ctx, key, false)
	if err != nil {
		t.Fatalf("GetOrCreateRun: %v", err)
	}
	if err := s.MarkTableDone(ctx, run.ID, "public.users", 5, 0); err != nil {
		t.Fatalf("MarkTableDone: %v", err)
	}
	if err := s.MarkRunCompleted(ctx, run.ID); err != nil {
		t.Fatalf("MarkRunCompleted: %v", err)
	}

	again, err := s.GetOrCreateRun(ctx, key, false)
	if err != nil {
		t.Fatalf("GetOrCreateRun (after completed): %v", err)
	}
	if again.ID != run.ID {
		t.Fatalf("reprocessed run got a different ID: %q, want same identity %q", again.ID, run.ID)
	}
	if again.Status != "in_progress" {
		t.Fatalf("Status = %q, want in_progress (a completed run resets automatically for reprocessing)", again.Status)
	}

	done, err := s.IsTableDone(ctx, again.ID, "public.users")
	if err != nil {
		t.Fatalf("IsTableDone: %v", err)
	}
	if done {
		t.Fatal("expected public.users checkpoint to be wiped when a completed run resets, but it's still marked done")
	}
}

// An in-progress run (a genuine crash-resume candidate) is the one case
// GetOrCreateRun preserves checkpoints for — unless forceReset (--fresh)
// says to abandon it anyway.
func TestGetOrCreateRun_InProgressRun_ResumesUnlessForceReset(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	key := testKey()

	run, err := s.GetOrCreateRun(ctx, key, false)
	if err != nil {
		t.Fatalf("GetOrCreateRun: %v", err)
	}
	if err := s.MarkTableDone(ctx, run.ID, "public.users", 5, 0); err != nil {
		t.Fatalf("MarkTableDone: %v", err)
	}
	// Deliberately not marked completed or failed: simulates a crash.

	resumed, err := s.GetOrCreateRun(ctx, key, false)
	if err != nil {
		t.Fatalf("GetOrCreateRun (resume): %v", err)
	}
	done, err := s.IsTableDone(ctx, resumed.ID, "public.users")
	if err != nil {
		t.Fatalf("IsTableDone: %v", err)
	}
	if !done {
		t.Fatal("expected an in-progress run's checkpoints to survive a resume when forceReset is false")
	}

	fresh, err := s.GetOrCreateRun(ctx, key, true)
	if err != nil {
		t.Fatalf("GetOrCreateRun (fresh): %v", err)
	}
	if fresh.ID != run.ID {
		t.Fatalf("fresh run got a different ID: %q, want same identity %q", fresh.ID, run.ID)
	}
	if fresh.Status != "in_progress" {
		t.Fatalf("Status after fresh = %q, want in_progress", fresh.Status)
	}
	if fresh.CompletedAt != nil {
		t.Fatal("expected CompletedAt to be cleared after a fresh reset")
	}

	done, err = s.IsTableDone(ctx, fresh.ID, "public.users")
	if err != nil {
		t.Fatalf("IsTableDone: %v", err)
	}
	if done {
		t.Fatal("expected public.users checkpoint to be wiped by --fresh, but it's still marked done")
	}
}

func TestIsTableDone_UnknownTableIsNotDone(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	run, err := s.GetOrCreateRun(ctx, testKey(), false)
	if err != nil {
		t.Fatalf("GetOrCreateRun: %v", err)
	}

	done, err := s.IsTableDone(ctx, run.ID, "public.never_touched")
	if err != nil {
		t.Fatalf("IsTableDone: %v", err)
	}
	if done {
		t.Fatal("expected an unrecorded table to be reported as not done")
	}
}

func TestMarkRunFailed(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	run, err := s.GetOrCreateRun(ctx, testKey(), false)
	if err != nil {
		t.Fatalf("GetOrCreateRun: %v", err)
	}

	if err := s.MarkRunFailed(ctx, run.ID, "foreign key violation on public.orders"); err != nil {
		t.Fatalf("MarkRunFailed: %v", err)
	}

	// Check the persisted status directly, not through GetOrCreateRun —
	// GetOrCreateRun auto-resets a non-in_progress run for reprocessing
	// (see TestGetOrCreateRun_CompletedRun_AutomaticallyResetsForReprocessing),
	// which would mutate the very status this test is verifying.
	persisted, err := s.getRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("getRun (after failed): %v", err)
	}
	if persisted.Status != "failed" {
		t.Fatalf("Status = %q, want failed", persisted.Status)
	}

	// A subsequent GetOrCreateRun call, as sync run would make on retry,
	// resets it to in_progress so the retry reprocesses everything.
	again, err := s.GetOrCreateRun(ctx, testKey(), false)
	if err != nil {
		t.Fatalf("GetOrCreateRun (after failed): %v", err)
	}
	if again.Status != "in_progress" {
		t.Fatalf("Status = %q, want in_progress (a failed run resets automatically on retry)", again.Status)
	}
}
