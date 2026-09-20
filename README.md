# Tributary

[![CI](https://github.com/bhuneshvar-k/tributary/actions/workflows/ci.yml/badge.svg)](https://github.com/bhuneshvar-k/tributary/actions/workflows/ci.yml)
[![Release](https://github.com/bhuneshvar-k/tributary/actions/workflows/release.yml/badge.svg)](https://github.com/bhuneshvar-k/tributary/actions/workflows/release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/bhuneshvar-k/tributary)](https://goreportcard.com/report/github.com/bhuneshvar-k/tributary)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A fast, reliable CLI for creating and syncing **referentially consistent Postgres subsets**.

Tributary walks real foreign keys plus user-declared relationships, computes the closure of rows that must travel together, and loads that subset into a target Postgres database.

```
┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
│  Source Postgres │ ───► │    Tributary    │ ───► │ Target Postgres │
│   (Production)  │      │  (CLI Tool)     │      │  (Dev/Staging)  │
└─────────────────┘      └─────────────────┘      └─────────────────┘
```

## ✨ Features

- **Referential Integrity** - Automatically follows foreign keys to maintain data relationships
- **AI-Powered** - Use natural language to plan and sync subsets (`tributary ai "sync user admin@example.com"`)
- **Cross-Platform** - Works on macOS, Linux, and Windows (amd64 & arm64)
- **Auto-Updates** - Built-in version checking with easy upgrade path
- **Resumable Sync** - Checkpoint system allows interrupted syncs to resume
- **Schema Auto-Create** - Automatically creates missing target tables
- **Upsert Mode** - Safe to re-run; updates existing rows, inserts new ones

## 📦 Installation

### Homebrew (macOS/Linux) - Recommended

```sh
brew tap bhuneshvar-k/tap
brew trust bhuneshvar-k/tap
brew install tributary
```

> **Note:** `brew trust` is required once per machine for third-party taps. After that, `brew upgrade tributary` works automatically.

### curl Installer (macOS/Linux)

```sh
curl -sSL https://get.tributary.dev | bash
```

With options:

```sh
# Install specific version
curl -sSL https://get.tributary.dev | bash -s -- --version v1.0.0

# Install to custom directory
curl -sSL https://get.tributary.dev | bash -s -- --to ~/.local/bin
```

### Go Install

```sh
go install github.com/bhuneshvar-k/tributary/cmd/tributary@latest
```

### GitHub Releases

Download the latest binary for your platform from [Releases](https://github.com/bhuneshvar-k/tributary/releases).

### Build from Source

```sh
git clone https://github.com/bhuneshvar-k/tributary.git
cd tributary
make build
./bin/tributary --version
```

## 🚀 Quick Start

### 1. Inspect Your Schema

```sh
export TRIBUTARY_DSN="postgres://user:pass@localhost:5432/mydb?sslmode=disable"

tributary inspect
```

This shows your database schema (tables, columns, keys, relationships) as JSON.

### 2. Plan a Subset

```sh
tributary plan \
  --seed-table users \
  --seed-predicate "id = 42"
```

This computes which rows are needed and shows per-table counts.

### 3. Sync the Subset

```sh
tributary sync run \
  --source-dsn "$SOURCE_DSN" \
  --target-dsn "$TARGET_DSN" \
  --seed-table users \
  --seed-predicate "id = 42"
```

This copies the referentially consistent subset to your target database.

## 📖 Command Reference

### `tributary inspect`

Prints source schema metadata as JSON.

```sh
tributary inspect --dsn "$TRIBUTARY_DSN"
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--dsn` | Database connection string (or env `TRIBUTARY_DSN`) |

**Output includes:**
- Tables
- Columns with nullability/type metadata
- Primary keys
- Foreign keys
- Enum definitions

---

### `tributary plan`

Computes a referentially consistent subset from a seed table + predicate.

```sh
tributary plan \
  --dsn "$TRIBUTARY_DSN" \
  --seed-table users \
  --seed-predicate "id = 42" \
  --schema-file tributary.schema.yaml
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--dsn` | Database connection string (or env `TRIBUTARY_DSN`) |
| `--seed-table` | Starting table for subset (required) |
| `--seed-predicate` | SQL WHERE fragment to select seed rows (required) |
| `--schema-file` | Path to schema relations file (optional) |
| `--strict-cycles` | Fail instead of auto-breaking cycles |
| `--include-upstream` | Enable full bidirectional fan-out |
| `--format` | Output format: `text` or `json` (default: `text`) |

**Behavior:**
- Default mode is **downstream-only**: required parent rows are included but don't fan out to all siblings
- `--include-upstream` enables full fan-out from any discovered row
- `--seed-predicate` is a raw SQL `WHERE` fragment

---

### `tributary sync run`

Executes one-shot subset copy from source to target.

```sh
tributary sync run \
  --source-dsn "$SOURCE_DSN" \
  --target-dsn "$TARGET_DSN" \
  --seed-table users \
  --seed-predicate "id = 42" \
  --schema-file tributary.schema.yaml
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--source-dsn` | Source database connection (or env `TRIBUTARY_SOURCE_DSN`) |
| `--target-dsn` | Target database connection (or env `TRIBUTARY_TARGET_DSN`) |
| `--seed-table` | Starting table for subset |
| `--seed-predicate` | SQL WHERE fragment to select seed rows |
| `--schema-file` | Path to schema relations file |
| `--strict-cycles` | Fail instead of auto-breaking cycles |
| `--include-upstream` | Enable full bidirectional fan-out |
| `--fresh` | Force cleanup before reload |
| `--no-create-schema` | Skip auto-creating target tables |
| `--state-db` | Path to checkpoint database (default: `.tributary/state.db`) |
| `--no-resume` | Don't resume from checkpoints |
| `--format` | Output format: `text` or `json` (default: `text`) |

**Behavior:**
- Default write mode is **upsert** (`INSERT ... ON CONFLICT DO UPDATE`)
- Re-running with same scope is safe and expected
- `--fresh` performs subset-scoped cleanup before reload
- Missing target tables are auto-created by default
- Checkpointing in local SQLite for crash resume

---

### `tributary ai`

Natural language interface — describe what you want in plain English and Tributary generates and runs the command.

```sh
tributary ai "sync user admin@example.com"
```

**Setup (one-time):**

```sh
# Pick a provider (claude, openai, gemini, openrouter, opencode)
tributary config set ai.provider claude

# Set your API key
tributary config set ai.api_key "sk-ant-your-key-here"

# (Optional) Set your DSN so the AI knows your schema
tributary config set dsn "postgres://user:pass@localhost:5432/mydb?sslmode=disable"
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--dry-run` | Show the generated command without executing it |
| `--auto` | Skip confirmation prompt and execute immediately |
| `--provider` | Override AI provider for this invocation |
| `--model` | Override the AI model for this invocation |
| `--max-tokens` | Override max response tokens |
| `--format` | Output format: `text` or `json` (default: `text`) |

**Examples:**

```sh
# Basic usage — AI translates your words into a tributary command
tributary ai "sync user admin@example.com"
tributary ai "plan a subset for all active users"
tributary ai "show me the schema for the orders table"

# Preview what the AI would do (no execution)
tributary ai --dry-run "sync all orders from last 7 days"

# Skip confirmation prompt
tributary ai --auto "subset users where team = 'marketing'"

# Override provider/model per invocation
tributary ai --provider openai "sync user 42"
tributary ai --provider gemini --model gemini-2.0-flash "plan orders"

# JSON output (for scripting)
tributary ai --format json "sync user admin@example.com"
```

**How it works:**

1. If a DSN is configured, Tributary inspects your database schema (tables, columns, foreign keys) and sends that metadata as context to the AI
2. The AI translates your natural language instruction into a structured Tributary command
3. The command, explanation, and any warnings are displayed
4. Unless `--dry-run` is set, the command executes after confirmation (or immediately with `--auto`)

**Supported AI providers:**

| Provider | Default Model | API Key Config |
|----------|--------------|----------------|
| `claude` | `claude-sonnet-4-20250514` | `ai.api_key` |
| `openai` | `gpt-4o` | `ai.api_key` |
| `gemini` | `gemini-2.0-flash` | `ai.api_key` |
| `openrouter` | `anthropic/claude-sonnet-4-20250514` | `ai.api_key` |
| `opencode` | `claude-sonnet-4-20250514` | (local, no key needed) |

**Security:**

- API keys are stored with `0600` file permissions (same as DSN passwords)
- Only schema metadata is sent to AI providers — never row data or connection strings
- Write operations (`sync run`) require confirmation by default

---

### `tributary update`

Updates tributary to the latest version.

```sh
tributary update
```

**Features:**
- Automatically downloads and installs the latest version
- Falls back to manual instructions if self-update fails
- Shows current and available versions

---

### `tributary --version`

Shows version information.

```sh
tributary --version
# tributary version v1.0.0
```

## 📋 Schema Relations File

Use `tributary.schema.yaml` when real DB constraints don't represent all relationships.

### Supported Relation Types

**Soft Foreign Key:**
```yaml
relations:
  - from: orders.user_id
    to: users.id
```

**Composite Key:**
```yaml
relations:
  - from: [line_items.order_id, line_items.tenant_id]
    to: [orders.id, orders.tenant_id]
```

**Polymorphic Association:**
```yaml
relations:
  - from: comments.commentable_id
    polymorphic_type: comments.commentable_type
    targets:
      Post: posts.id
      Photo: photos.id
```

**Ignore FK Edge:**
```yaml
relations:
  - ignore: audit_logs.actor_id
```

**Dependency Breaks (Cycle Handling):**
```yaml
dependency_breaks:
  - table: employees
    column: manager_id
```

See [`tributary.schema.example.yaml`](tributary.schema.example.yaml) for a complete example.

## 🔄 Typical Workflow

```
1. Inspect Schema
   └─► tributary inspect --dsn "$SOURCE_DSN"

2. (Optional) Define Relations
   └─► Create tributary.schema.yaml

3. Plan Subset
   └─► tributary plan --seed-table users --seed-predicate "id = 42"

4. Sync to Target
   └─► tributary sync run --source-dsn ... --target-dsn ...

5. Refresh (Optional)
   └─► Re-run sync run to update target data
```

## 🛠️ Development

### Prerequisites

- Go 1.25+
- Docker (for integration tests)

### Build

```sh
make build
```

### Run Tests

```sh
make test
```

### Development Build

```sh
make dev
```

### Project Structure

```
tributary/
├── cmd/tributary/        # CLI entry point
├── internal/
│   ├── ai/               # AI provider abstraction + command generation
│   ├── catalog/          # Schema introspection
│   ├── graph/            # Graph build + closure walk
│   ├── subset/           # Dependency ordering
│   ├── load/             # Schema ensure + copy/upsert
│   ├── state/            # SQLite checkpointing
│   └── version/          # Version info + update check
├── pkg/
│   ├── config/           # Schema relations config
│   └── userconfig/       # User-level config (~/.tributary/config.yaml)
├── docs/                 # Documentation
└── .goreleaser.yaml      # Release configuration
```

## ⚙️ Configuration

Tributary stores user-level configuration in `~/.tributary/config.yaml`. This file is optional — all commands work without it via CLI flags and environment variables.

### Precedence

**CLI flags > Environment variables > Config file**

### Config Commands

```sh
# Set a value
tributary config set dsn "postgres://user:pass@localhost:5432/mydb?sslmode=disable"

# Get a value
tributary config get dsn

# List all values
tributary config list

# Remove a value
tributary config unset dsn

# Show config file path
tributary config path
```

### Available Config Keys

| Key | Description |
|-----|-------------|
| `dsn` | Default Postgres connection string (inspect, plan) |
| `source_dsn` | Default source Postgres connection string (sync run) |
| `target_dsn` | Default target Postgres connection string (sync run) |
| `seed_table` | Default seed table name |
| `seed_predicate` | Default seed predicate (raw SQL WHERE fragment) |
| `schema_file` | Default path to tributary.schema.yaml |
| `ai.provider` | AI provider: `claude`, `openai`, `gemini`, `openrouter`, or `opencode` |
| `ai.api_key` | API key for the AI provider (stored with `0600` permissions) |
| `ai.model` | AI model override (empty = provider default) |
| `ai.base_url` | Custom AI API endpoint (empty = provider default) |

### Example Config File

```yaml
# ~/.tributary/config.yaml
dsn: "postgres://user:pass@localhost:5432/mydb?sslmode=disable"
source_dsn: "postgres://user:pass@localhost:5432/prod?sslmode=disable"
target_dsn: "postgres://user:pass@localhost:5432/dev?sslmode=disable"
seed_table: users
seed_predicate: "id = 42"
schema_file: "./tributary.schema.yaml"
ai:
  provider: claude
  api_key: "sk-ant-your-key-here"
  model: "claude-sonnet-4-20250514"
```

### Security

- Config file is written with `0600` permissions (owner read/write only)
- DSN strings may contain passwords — the file is not world-readable
- API keys are stored in the same file with the same protections
- Config directory is created with `0700` permissions

### Environment Variables

| Variable | Description |
|----------|-------------|
| `TRIBUTARY_DSN` | Default database connection string |
| `TRIBUTARY_SOURCE_DSN` | Source database for sync |
| `TRIBUTARY_TARGET_DSN` | Target database for sync |
| `INSTALL_DIR` | Custom install directory for installer script |

## 📊 Schema Creation Behavior

When auto-create is enabled (default), Tributary creates missing target tables with:

- ✅ Columns + data types
- ✅ `NOT NULL` constraints
- ✅ `PRIMARY KEY` constraints
- ✅ Real foreign keys
- ✅ Enum type definitions

**Not covered:**
- ❌ Defaults
- ❌ Sequences/identity
- ❌ Check constraints
- ❌ Indexes beyond PK
- ❌ Triggers
- ❌ Views
- ❌ Non-enum custom types

## 🗺️ Roadmap

- [x] AI-powered natural language interface (`tributary ai`)
- [ ] Masking/transform pipeline
- [ ] Incremental sync via logical replication (`sync watch`)
- [ ] Observability & hardening
- [ ] Lightweight branching workflow

See [`docs/PLAN.md`](docs/PLAN.md) for detailed roadmap.

## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🔒 Privacy & Data Security

### Your Data Stays on Your Machines

**Tributary is a local CLI tool. It never sees, stores, or transmits your data.**

Tributary runs entirely on your machine and connects directly between your source and target Postgres databases. No data passes through any third-party server, cloud service, or analytics endpoint — not even temporarily.

```
┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
│  Source Postgres │ ◄──► │    Tributary    │ ◄──► │ Target Postgres │
│   (Your Server) │      │  (Your Machine) │      │  (Your Server)  │
└─────────────────┘      └─────────────────┘      └─────────────────┘
        ▲                                                  ▲
        │              No external connections              │
        └──────────────────────────────────────────────────┘
```

### What Tributary Does NOT Do

| Action | Status |
|--------|--------|
| Send data to external servers | ❌ Never (except AI provider when `tributary ai` is used) |
| Phone home or report analytics | ❌ Never |
| Log queries or row data | ❌ Never |
| Store credentials in plaintext | ❌ Never |
| Access the internet during sync | ❌ Never |
| Upload schema information | ❌ Never (except to AI provider for `tributary ai` context) |
| Share usage statistics | ❌ Never |

### What Tributary Accesses

- **Schema metadata (read-only)** — Table names, columns, primary keys, foreign keys, enum types
- **Row data (during sync only)** — Streamed directly between databases, never cached or stored by Tributary
- **AI provider (opt-in, `tributary ai` only)** — When you use `tributary ai`, schema metadata (table/column names, foreign keys) is sent to the configured AI provider for command generation. Row data and connection strings are never sent.

### Checkpoint File

Tributary creates a local SQLite file (`.tributary/state.db`) to track sync progress. This file contains only:

- Run identifiers (hashes)
- Table names and sync status
- Row counts

**It does NOT contain row data, column values, or connection strings.**

### Open Source & Auditable

Tributary is fully open source under the MIT License. You can read the code, build from source, and verify no external connections are made.

---

## 🔗 Links

- [GitHub Repository](https://github.com/bhuneshvar-k/tributary)
- [Releases](https://github.com/bhuneshvar-k/tributary/releases)
- [Issue Tracker](https://github.com/bhuneshvar-k/tributary/issues)
- [Documentation](docs/)
