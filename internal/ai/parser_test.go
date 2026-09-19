package ai

import (
	"testing"
)

func TestParseResponse_ValidJSON(t *testing.T) {
	input := `{
		"command": "sync run",
		"args": {
			"seed_table": "users",
			"seed_predicate": "email = 'admin@example.com'"
		},
		"explanation": "Syncing user with admin@example.com and related data.",
		"warnings": ["This will write to the target database."],
		"requires_confirmation": true
	}`

	cmd, err := ParseResponse(input)
	if err != nil {
		t.Fatalf("ParseResponse() error: %v", err)
	}

	if cmd.Command != "sync run" {
		t.Errorf("Command: got %q, want %q", cmd.Command, "sync run")
	}
	if cmd.Args.SeedTable != "users" {
		t.Errorf("SeedTable: got %q, want %q", cmd.Args.SeedTable, "users")
	}
	if cmd.Args.SeedPredicate != "email = 'admin@example.com'" {
		t.Errorf("SeedPredicate: got %q, want %q", cmd.Args.SeedPredicate, "email = 'admin@example.com'")
	}
	if !cmd.RequiresConfirmation {
		t.Error("RequiresConfirmation should be true")
	}
	if len(cmd.Warnings) != 1 {
		t.Errorf("Warnings: got %d, want 1", len(cmd.Warnings))
	}
}

func TestParseResponse_WithMarkdownFence(t *testing.T) {
	input := "Here's the command:\n```json\n" + `{
		"command": "plan",
		"args": {
			"seed_table": "orders",
			"seed_predicate": "created_at > '2024-01-01'"
		},
		"explanation": "Planning subset for recent orders.",
		"warnings": [],
		"requires_confirmation": false
	}` + "\n```"

	cmd, err := ParseResponse(input)
	if err != nil {
		t.Fatalf("ParseResponse() error: %v", err)
	}

	if cmd.Command != "plan" {
		t.Errorf("Command: got %q, want %q", cmd.Command, "plan")
	}
	if cmd.Args.SeedTable != "orders" {
		t.Errorf("SeedTable: got %q, want %q", cmd.Args.SeedTable, "orders")
	}
}

func TestParseResponse_WithSurroundingText(t *testing.T) {
	input := `I'll help you with that. Here's the command:

` + `{"command": "inspect", "args": {}, "explanation": "Showing schema.", "warnings": [], "requires_confirmation": false}` + `

Let me know if you need anything else!`

	cmd, err := ParseResponse(input)
	if err != nil {
		t.Fatalf("ParseResponse() error: %v", err)
	}

	if cmd.Command != "inspect" {
		t.Errorf("Command: got %q, want %q", cmd.Command, "inspect")
	}
}

func TestParseResponse_NoJSON(t *testing.T) {
	input := "I don't understand what you mean. Could you rephrase?"

	_, err := ParseResponse(input)
	if err == nil {
		t.Error("ParseResponse() should return error for non-JSON input")
	}
}

func TestParseResponse_InvalidJSON(t *testing.T) {
	input := `{"command": "sync run", "args": {"seed_table": }}`

	_, err := ParseResponse(input)
	if err == nil {
		t.Error("ParseResponse() should return error for invalid JSON")
	}
}

func TestParseResponse_MissingSeedTable(t *testing.T) {
	input := `{
		"command": "sync run",
		"args": {
			"seed_predicate": "id = 1"
		},
		"explanation": "test",
		"warnings": [],
		"requires_confirmation": true
	}`

	_, err := ParseResponse(input)
	if err == nil {
		t.Error("ParseResponse() should return error for missing seed_table")
	}
}

func TestParseResponse_MissingSeedPredicate(t *testing.T) {
	input := `{
		"command": "sync run",
		"args": {
			"seed_table": "users"
		},
		"explanation": "test",
		"warnings": [],
		"requires_confirmation": true
	}`

	_, err := ParseResponse(input)
	if err == nil {
		t.Error("ParseResponse() should return error for missing seed_predicate")
	}
}

func TestParseResponse_InspectNoArgs(t *testing.T) {
	input := `{
		"command": "inspect",
		"args": {},
		"explanation": "Showing schema.",
		"warnings": [],
		"requires_confirmation": false
	}`

	cmd, err := ParseResponse(input)
	if err != nil {
		t.Fatalf("ParseResponse() error: %v", err)
	}

	if cmd.Command != "inspect" {
		t.Errorf("Command: got %q, want %q", cmd.Command, "inspect")
	}
}

func TestParseResponse_UnknownCommand(t *testing.T) {
	input := `{
		"command": "deploy",
		"args": {},
		"explanation": "Deploying to production.",
		"warnings": [],
		"requires_confirmation": true
	}`

	_, err := ParseResponse(input)
	if err == nil {
		t.Error("ParseResponse() should return error for unknown command")
	}
}

func TestParseResponse_NormalizeCommands(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`{"command": "schema", "args": {}, "explanation": "x", "warnings": [], "requires_confirmation": false}`, "inspect"},
		{`{"command": "show tables", "args": {}, "explanation": "x", "warnings": [], "requires_confirmation": false}`, "inspect"},
		{`{"command": "subset", "args": {"seed_table": "t", "seed_predicate": "id = 1"}, "explanation": "x", "warnings": [], "requires_confirmation": false}`, "plan"},
		{`{"command": "preview", "args": {"seed_table": "t", "seed_predicate": "id = 1"}, "explanation": "x", "warnings": [], "requires_confirmation": false}`, "plan"},
		{`{"command": "copy", "args": {"seed_table": "t", "seed_predicate": "id = 1"}, "explanation": "x", "warnings": [], "requires_confirmation": true}`, "sync run"},
		{`{"command": "migrate", "args": {"seed_table": "t", "seed_predicate": "id = 1"}, "explanation": "x", "warnings": [], "requires_confirmation": true}`, "sync run"},
	}

	for _, tt := range tests {
		cmd, err := ParseResponse(tt.input)
		if err != nil {
			t.Errorf("ParseResponse(%q) error: %v", tt.input, err)
			continue
		}
		if cmd.Command != tt.expected {
			t.Errorf("ParseResponse(%q) Command: got %q, want %q", tt.input, cmd.Command, tt.expected)
		}
	}
}

func TestParseResponse_DefaultFormat(t *testing.T) {
	input := `{
		"command": "plan",
		"args": {
			"seed_table": "users",
			"seed_predicate": "id = 1"
		},
		"explanation": "test",
		"warnings": [],
		"requires_confirmation": false
	}`

	cmd, err := ParseResponse(input)
	if err != nil {
		t.Fatalf("ParseResponse() error: %v", err)
	}

	if cmd.Args.Format != "text" {
		t.Errorf("Format: got %q, want %q", cmd.Args.Format, "text")
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"raw JSON", `{"a": 1}`, `{"a": 1}`},
		{"fenced", "```json\n{\"a\": 1}\n```", `{"a": 1}`},
		{"fenced without lang", "```\n{\"a\": 1}\n```", `{"a": 1}`},
		{"surrounded", "text {\"a\": 1} text", `{"a": 1}`},
		{"nested", `{"a": {"b": 1}}`, `{"a": {"b": 1}}`},
		{"no JSON", "no json here", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSON(tt.input)
			if got != tt.want {
				t.Errorf("extractJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestToArgs_Inspect(t *testing.T) {
	cmd := &ParsedCommand{Command: "inspect"}
	args := cmd.ToArgs()
	if len(args) != 1 || args[0] != "inspect" {
		t.Errorf("ToArgs(): got %v, want [inspect]", args)
	}
}

func TestToArgs_SyncRun(t *testing.T) {
	cmd := &ParsedCommand{
		Command: "sync run",
		Args: CommandArgs{
			SeedTable:     "users",
			SeedPredicate: "id = 42",
			Fresh:         true,
			Format:        "json",
		},
	}
	args := cmd.ToArgs()

	expected := []string{"sync", "run", "--seed-table", "users", "--seed-predicate", "id = 42", "--fresh", "--format", "json"}
	if len(args) != len(expected) {
		t.Fatalf("ToArgs() len: got %d, want %d", len(args), len(expected))
	}
	for i, a := range args {
		if a != expected[i] {
			t.Errorf("ToArgs()[%d]: got %q, want %q", i, a, expected[i])
		}
	}
}
