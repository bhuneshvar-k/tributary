package ai

import (
	"strings"
	"testing"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
)

func TestBuildSystemPrompt_NoSchema(t *testing.T) {
	prompt := BuildSystemPrompt(nil)

	if !strings.Contains(prompt, "Tributary AI") {
		t.Error("prompt should mention Tributary AI")
	}
	if !strings.Contains(prompt, "inspect") {
		t.Error("prompt should mention inspect command")
	}
	if !strings.Contains(prompt, "plan") {
		t.Error("prompt should mention plan command")
	}
	if !strings.Contains(prompt, "sync run") {
		t.Error("prompt should mention sync run command")
	}
	if strings.Contains(prompt, "DATABASE SCHEMA") {
		t.Error("prompt should not contain schema section when schema is nil")
	}
}

func TestBuildSystemPrompt_WithSchema(t *testing.T) {
	schema := &catalog.Schema{
		Tables: []catalog.Table{
			{
				Schema:     "public",
				Name:       "users",
				PrimaryKey: []string{"id"},
				Columns: []catalog.Column{
					{Name: "id", Type: "integer", IsNullable: false},
					{Name: "email", Type: "text", IsNullable: false},
					{Name: "name", Type: "text", IsNullable: true},
				},
				ForeignKeys: []catalog.ForeignKey{
					{
						ConstraintName: "fk_company",
						FromTable:      "public.users",
						FromColumns:    []string{"company_id"},
						ToTable:        "public.companies",
						ToColumns:      []string{"id"},
					},
				},
			},
			{
				Schema:     "public",
				Name:       "orders",
				PrimaryKey: []string{"id"},
				Columns: []catalog.Column{
					{Name: "id", Type: "integer", IsNullable: false},
					{Name: "user_id", Type: "integer", IsNullable: false},
					{Name: "total", Type: "numeric(10,2)", IsNullable: false},
				},
			},
		},
		Enums: map[string][]string{
			"status": {"active", "inactive", "pending"},
		},
	}

	prompt := BuildSystemPrompt(schema)

	if !strings.Contains(prompt, "DATABASE SCHEMA") {
		t.Error("prompt should contain DATABASE SCHEMA section")
	}
	if !strings.Contains(prompt, "users") {
		t.Error("prompt should mention users table")
	}
	if !strings.Contains(prompt, "orders") {
		t.Error("prompt should mention orders table")
	}
	if !strings.Contains(prompt, "Primary Key: id") {
		t.Error("prompt should mention primary key")
	}
	if !strings.Contains(prompt, "public.users") && !strings.Contains(prompt, "users(company_id)") {
		t.Error("prompt should mention foreign key relationship")
	}
	if !strings.Contains(prompt, "active, inactive, pending") {
		t.Error("prompt should mention enum values")
	}
	if !strings.Contains(prompt, "email") {
		t.Error("prompt should mention columns")
	}
}

func TestBuildSystemPrompt_EmptySchema(t *testing.T) {
	schema := &catalog.Schema{Tables: []catalog.Table{}}
	prompt := BuildSystemPrompt(schema)

	// Empty schema should not add the schema section.
	if strings.Contains(prompt, "DATABASE SCHEMA") {
		t.Error("prompt should not contain schema section for empty tables")
	}
}

func TestBuildSystemPrompt_JSONResponseSchema(t *testing.T) {
	prompt := BuildSystemPrompt(nil)

	// System prompt should include the JSON response format.
	if !strings.Contains(prompt, `"command"`) {
		t.Error("prompt should include command field in response schema")
	}
	if !strings.Contains(prompt, `"args"`) {
		t.Error("prompt should include args field in response schema")
	}
	if !strings.Contains(prompt, `"explanation"`) {
		t.Error("prompt should include explanation field in response schema")
	}
	if !strings.Contains(prompt, `"warnings"`) {
		t.Error("prompt should include warnings field in response schema")
	}
	if !strings.Contains(prompt, `"requires_confirmation"`) {
		t.Error("prompt should include requires_confirmation field in response schema")
	}
}
