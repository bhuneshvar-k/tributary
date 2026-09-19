package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParsedCommand is the structured result of AI interpretation of a user prompt.
type ParsedCommand struct {
	// Command is the Tributary subcommand: "inspect", "plan", or "sync run".
	Command string `json:"command"`

	// Args holds the flags for the command.
	Args CommandArgs `json:"args"`

	// Explanation is a human-readable description of what this command does.
	Explanation string `json:"explanation"`

	// Warnings flags risks or assumptions.
	Warnings []string `json:"warnings"`

	// RequiresConfirmation should be true for write operations.
	RequiresConfirmation bool `json:"requires_confirmation"`
}

// CommandArgs holds all possible flags for a Tributary command.
type CommandArgs struct {
	SeedTable       string `json:"seed_table,omitempty"`
	SeedPredicate   string `json:"seed_predicate,omitempty"`
	SchemaFile      string `json:"schema_file,omitempty"`
	StrictCycles    bool   `json:"strict_cycles,omitempty"`
	IncludeUpstream bool   `json:"include_upstream,omitempty"`
	Fresh           bool   `json:"fresh,omitempty"`
	NoCreateSchema  bool   `json:"no_create_schema,omitempty"`
	Format          string `json:"format,omitempty"`
}

// ParseResponse extracts a ParsedCommand from the AI's raw text response.
// It handles markdown code fences, extra text around JSON, and minor
// formatting issues.
func ParseResponse(content string) (*ParsedCommand, error) {
	jsonStr := extractJSON(content)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON found in AI response:\n%s", content)
	}

	var cmd ParsedCommand
	if err := json.Unmarshal([]byte(jsonStr), &cmd); err != nil {
		return nil, fmt.Errorf("parse AI response JSON: %w\nraw JSON:\n%s", err, jsonStr)
	}

	// Normalize command.
	cmd.Command = normalizeCommand(cmd.Command)

	// Validate.
	if err := cmd.Validate(); err != nil {
		return nil, fmt.Errorf("invalid AI response: %w", err)
	}

	return &cmd, nil
}

// Validate checks that the parsed command has all required fields.
func (c *ParsedCommand) Validate() error {
	switch c.Command {
	case "inspect":
		// No required args.
	case "plan", "sync run":
		if c.Args.SeedTable == "" {
			return fmt.Errorf("missing required field: seed_table")
		}
		if c.Args.SeedPredicate == "" {
			return fmt.Errorf("missing required field: seed_predicate")
		}
	default:
		return fmt.Errorf("unknown command %q (valid: inspect, plan, sync run)", c.Command)
	}

	// Normalize format.
	if c.Args.Format == "" {
		c.Args.Format = "text"
	}
	if c.Args.Format != "text" && c.Args.Format != "json" {
		return fmt.Errorf("invalid format %q (valid: text, json)", c.Args.Format)
	}

	return nil
}

// ToArgs converts a ParsedCommand into a CLI args slice (e.g. for exec or display).
func (c *ParsedCommand) ToArgs() []string {
	var args []string

	// Command parts.
	for _, part := range strings.Split(c.Command, " ") {
		args = append(args, part)
	}

	// Flags.
	if c.Args.SeedTable != "" {
		args = append(args, "--seed-table", c.Args.SeedTable)
	}
	if c.Args.SeedPredicate != "" {
		args = append(args, "--seed-predicate", c.Args.SeedPredicate)
	}
	if c.Args.SchemaFile != "" {
		args = append(args, "--schema-file", c.Args.SchemaFile)
	}
	if c.Args.StrictCycles {
		args = append(args, "--strict-cycles")
	}
	if c.Args.IncludeUpstream {
		args = append(args, "--include-upstream")
	}
	if c.Args.Fresh {
		args = append(args, "--fresh")
	}
	if c.Args.NoCreateSchema {
		args = append(args, "--no-create-schema")
	}
	if c.Args.Format != "" && c.Args.Format != "text" {
		args = append(args, "--format", c.Args.Format)
	}

	return args
}

// extractJSON tries to find a JSON object in the response text. It handles:
// - Raw JSON
// - JSON wrapped in ```json ... ``` code fences
// - JSON with surrounding explanatory text
func extractJSON(s string) string {
	// Try direct parse first.
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		// Find matching closing brace.
		depth := 0
		for i, ch := range s {
			if ch == '{' {
				depth++
			} else if ch == '}' {
				depth--
				if depth == 0 {
					return s[:i+1]
				}
			}
		}
	}

	// Try extracting from markdown code fence.
	if idx := strings.Index(s, "```json"); idx != -1 {
		start := idx + len("```json")
		if endIdx := strings.Index(s[start:], "```"); endIdx != -1 {
			return strings.TrimSpace(s[start : start+endIdx])
		}
	}
	if idx := strings.Index(s, "```"); idx != -1 {
		start := idx + len("```")
		if endIdx := strings.Index(s[start:], "```"); endIdx != -1 {
			return strings.TrimSpace(s[start : start+endIdx])
		}
	}

	// Try to find first { and last }.
	first := strings.Index(s, "{")
	last := strings.LastIndex(s, "}")
	if first != -1 && last > first {
		return s[first : last+1]
	}

	return ""
}

// normalizeCommand maps common AI variations to canonical command names.
func normalizeCommand(cmd string) string {
	cmd = strings.TrimSpace(strings.ToLower(cmd))
	switch {
	case cmd == "inspect" || cmd == "schema" || cmd == "show schema" || cmd == "show tables":
		return "inspect"
	case cmd == "plan" || cmd == "subset" || cmd == "preview":
		return "plan"
	case cmd == "sync" || cmd == "sync run" || cmd == "run" || cmd == "copy" || cmd == "migrate":
		return "sync run"
	default:
		return cmd
	}
}
