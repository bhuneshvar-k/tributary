package userconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_NoFile(t *testing.T) {
	// Use a temp home dir with no config file.
	t.Setenv("HOME", t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with no file: %v", err)
	}
	if cfg.DSN != "" {
		t.Errorf("expected empty DSN, got %q", cfg.DSN)
	}
}

func TestSaveAndLoad(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	original := &UserConfig{
		Version:       1,
		DSN:           "postgres://localhost/test",
		SourceDSN:     "postgres://localhost/source",
		TargetDSN:     "postgres://localhost/target",
		SeedTable:     "users",
		SeedPredicate: "id = 1",
		SchemaFile:    "./tributary.schema.yaml",
	}

	if err := Save(original); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if loaded.DSN != original.DSN {
		t.Errorf("DSN: got %q, want %q", loaded.DSN, original.DSN)
	}
	if loaded.SourceDSN != original.SourceDSN {
		t.Errorf("SourceDSN: got %q, want %q", loaded.SourceDSN, original.SourceDSN)
	}
	if loaded.TargetDSN != original.TargetDSN {
		t.Errorf("TargetDSN: got %q, want %q", loaded.TargetDSN, original.TargetDSN)
	}
	if loaded.SeedTable != original.SeedTable {
		t.Errorf("SeedTable: got %q, want %q", loaded.SeedTable, original.SeedTable)
	}
	if loaded.SeedPredicate != original.SeedPredicate {
		t.Errorf("SeedPredicate: got %q, want %q", loaded.SeedPredicate, original.SeedPredicate)
	}
	if loaded.SchemaFile != original.SchemaFile {
		t.Errorf("SchemaFile: got %q, want %q", loaded.SchemaFile, original.SchemaFile)
	}
}

func TestSave_FilePermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &UserConfig{DSN: "postgres://localhost/test"}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	path, _ := Path()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(): %v", err)
	}

	// File should be 0600 (owner read/write only).
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("file permissions: got %o, want 0600", perm)
	}
}

func TestGet_Set(t *testing.T) {
	cfg := &UserConfig{}

	if err := Set(cfg, "dsn", "postgres://localhost/test"); err != nil {
		t.Fatalf("Set(dsn): %v", err)
	}
	if got := Get(cfg, "dsn"); got != "postgres://localhost/test" {
		t.Errorf("Get(dsn): got %q, want %q", got, "postgres://localhost/test")
	}

	if err := Set(cfg, "source_dsn", "postgres://localhost/src"); err != nil {
		t.Fatalf("Set(source_dsn): %v", err)
	}
	if got := Get(cfg, "source_dsn"); got != "postgres://localhost/src" {
		t.Errorf("Get(source_dsn): got %q, want %q", got, "postgres://localhost/src")
	}

	if err := Set(cfg, "target_dsn", "postgres://localhost/tgt"); err != nil {
		t.Fatalf("Set(target_dsn): %v", err)
	}
	if got := Get(cfg, "target_dsn"); got != "postgres://localhost/tgt" {
		t.Errorf("Get(target_dsn): got %q, want %q", got, "postgres://localhost/tgt")
	}

	if err := Set(cfg, "seed_table", "users"); err != nil {
		t.Fatalf("Set(seed_table): %v", err)
	}
	if got := Get(cfg, "seed_table"); got != "users" {
		t.Errorf("Get(seed_table): got %q, want %q", got, "users")
	}

	if err := Set(cfg, "seed_predicate", "id = 1"); err != nil {
		t.Fatalf("Set(seed_predicate): %v", err)
	}
	if got := Get(cfg, "seed_predicate"); got != "id = 1" {
		t.Errorf("Get(seed_predicate): got %q, want %q", got, "id = 1")
	}

	if err := Set(cfg, "schema_file", "./schema.yaml"); err != nil {
		t.Fatalf("Set(schema_file): %v", err)
	}
	if got := Get(cfg, "schema_file"); got != "./schema.yaml" {
		t.Errorf("Get(schema_file): got %q, want %q", got, "./schema.yaml")
	}
}

func TestSet_UnknownKey(t *testing.T) {
	cfg := &UserConfig{}
	if err := Set(cfg, "unknown_key", "value"); err == nil {
		t.Error("Set(unknown_key) should return error")
	}
}

func TestUnset(t *testing.T) {
	cfg := &UserConfig{DSN: "postgres://localhost/test"}
	if err := Unset(cfg, "dsn"); err != nil {
		t.Fatalf("Unset(dsn): %v", err)
	}
	if got := Get(cfg, "dsn"); got != "" {
		t.Errorf("Get(dsn) after Unset: got %q, want empty", got)
	}
}

func TestGet_UnknownKey(t *testing.T) {
	cfg := &UserConfig{}
	if got := Get(cfg, "unknown_key"); got != "" {
		t.Errorf("Get(unknown_key): got %q, want empty", got)
	}
}

func TestPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, err := Path()
	if err != nil {
		t.Fatalf("Path(): %v", err)
	}

	expected := filepath.Join(t.TempDir(), ".tributary", configFileName)
	// We need to re-evaluate since HOME is set to TempDir.
	expected = filepath.Join(os.Getenv("HOME"), ".tributary", configFileName)
	if path != expected {
		t.Errorf("Path(): got %q, want %q", path, expected)
	}
}

func TestKeys(t *testing.T) {
	keys := Keys()
	if len(keys) != 6 {
		t.Errorf("Keys(): got %d keys, want 6", len(keys))
	}
}

func TestKeyDescription(t *testing.T) {
	desc := KeyDescription("dsn")
	if desc == "" {
		t.Error("KeyDescription(dsn) returned empty string")
	}

	desc = KeyDescription("unknown")
	if desc != "" {
		t.Errorf("KeyDescription(unknown): got %q, want empty", desc)
	}
}
