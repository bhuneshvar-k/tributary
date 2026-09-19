package userconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// configFileName is the name of the user config file.
const configFileName = "config.yaml"

// UserConfig holds the persisted user-level settings. Every field is
// optional — a missing or empty value means "fall through to the next
// precedence level" (env var, then CLI flag).
type UserConfig struct {
	// Version is the config file format version. Currently 1.
	Version int `yaml:"version,omitempty"`

	// DSN is the default Postgres connection string for single-DSN
	// commands (inspect, plan). Corresponds to --dsn / TRIBUTARY_DSN.
	DSN string `yaml:"dsn,omitempty"`

	// SourceDSN is the default source Postgres connection string for
	// sync run. Corresponds to --source-dsn / TRIBUTARY_SOURCE_DSN.
	SourceDSN string `yaml:"source_dsn,omitempty"`

	// TargetDSN is the default target Postgres connection string for
	// sync run. Corresponds to --target-dsn / TRIBUTARY_TARGET_DSN.
	TargetDSN string `yaml:"target_dsn,omitempty"`

	// SeedTable is the default seed table name. Corresponds to
	// --seed-table.
	SeedTable string `yaml:"seed_table,omitempty"`

	// SeedPredicate is the default seed predicate (raw SQL WHERE
	// fragment). Corresponds to --seed-predicate.
	SeedPredicate string `yaml:"seed_predicate,omitempty"`

	// SchemaFile is the default path to a tributary.schema.yaml file.
	// Corresponds to --schema-file.
	SchemaFile string `yaml:"schema_file,omitempty"`
}

// Dir returns the tributary config directory (~/.tributary), creating
// it if necessary. The directory is created with 0o700 permissions.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory: %w", err)
	}
	dir := filepath.Join(home, ".tributary")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config directory %s: %w", dir, err)
	}
	return dir, nil
}

// Path returns the full path to the user config file (~/.tributary/config.yaml).
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

// Load reads and parses the user config file. Returns an empty config
// (not an error) if the file does not exist — config is optional.
func Load() (*UserConfig, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &UserConfig{}, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg UserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	return &cfg, nil
}

// Save writes the config to disk. It creates the config directory if
// needed and writes the file with 0o600 permissions (DSN strings may
// contain passwords).
func Save(cfg *UserConfig) error {
	path, err := Path()
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}

	return nil
}

// Get retrieves a config value by dot-separated key path (e.g. "dsn",
// "seed_table"). Returns an empty string if the key is not set.
func Get(cfg *UserConfig, key string) string {
	switch strings.ToLower(key) {
	case "dsn":
		return cfg.DSN
	case "source_dsn":
		return cfg.SourceDSN
	case "target_dsn":
		return cfg.TargetDSN
	case "seed_table":
		return cfg.SeedTable
	case "seed_predicate":
		return cfg.SeedPredicate
	case "schema_file":
		return cfg.SchemaFile
	default:
		return ""
	}
}

// Set sets a config value by key name. Returns an error for unknown keys.
func Set(cfg *UserConfig, key, value string) error {
	switch strings.ToLower(key) {
	case "dsn":
		cfg.DSN = value
	case "source_dsn":
		cfg.SourceDSN = value
	case "target_dsn":
		cfg.TargetDSN = value
	case "seed_table":
		cfg.SeedTable = value
	case "seed_predicate":
		cfg.SeedPredicate = value
	case "schema_file":
		cfg.SchemaFile = value
	default:
		return fmt.Errorf("unknown config key %q (valid keys: dsn, source_dsn, target_dsn, seed_table, seed_predicate, schema_file)", key)
	}
	return nil
}

// Unset removes a config value by key name. Returns an error for unknown keys.
func Unset(cfg *UserConfig, key string) error {
	return Set(cfg, key, "")
}

// Keys returns all valid config key names.
func Keys() []string {
	return []string{
		"dsn",
		"source_dsn",
		"target_dsn",
		"seed_table",
		"seed_predicate",
		"schema_file",
	}
}

// KeyDescription returns a human-readable description for each config key.
func KeyDescription(key string) string {
	switch strings.ToLower(key) {
	case "dsn":
		return "Default Postgres connection string (inspect, plan)"
	case "source_dsn":
		return "Default source Postgres connection string (sync run)"
	case "target_dsn":
		return "Default target Postgres connection string (sync run)"
	case "seed_table":
		return "Default seed table name"
	case "seed_predicate":
		return "Default seed predicate (raw SQL WHERE fragment)"
	case "schema_file":
		return "Default path to tributary.schema.yaml"
	default:
		return ""
	}
}
