package ai

import (
	"fmt"
	"strings"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
)

// BuildSystemPrompt constructs the system prompt that teaches the AI how
// Tributary works, what commands are available, and what the user's
// database looks like.
func BuildSystemPrompt(schema *catalog.Schema) string {
	var b strings.Builder

	b.WriteString(`You are Tributary AI, an assistant that helps users interact with
Tributary — a CLI tool for creating referentially-consistent Postgres subsets.

Your job is to translate natural-language instructions into Tributary commands.
You MUST respond with valid JSON matching the schema below. Do NOT wrap it in
markdown code fences.

Response schema:
{
  "command": "inspect | plan | sync run",
  "args": {
    "seed_table": "TableName",
    "seed_predicate": "raw SQL WHERE fragment",
    "schema_file": "path (optional)",
    "strict_cycles": false,
    "include_upstream": false,
    "fresh": false,
    "no_create_schema": false,
    "format": "text | json"
  },
  "explanation": "Brief explanation of what this command does",
  "warnings": ["Any warnings or caveats"],
  "requires_confirmation": false
}

Rules:
- "inspect" has no args beyond optional --dsn (never send DSNs).
- "plan" requires seed_table + seed_predicate.
- "sync run" requires seed_table + seed_predicate.
- seed_predicate is a raw SQL WHERE fragment, e.g. "id = 42" or "email = 'foo@bar.com'".
- For table references, prefer unqualified names (e.g. "users" not "public.users")
  unless the user explicitly mentions a schema.
- If the user asks to "sync" something, use "sync run".
- If the user asks to "see" or "show" data/schema, use "inspect".
- If the user asks to "preview" or "plan" a subset, use "plan".
- Set requires_confirmation to true for "sync run" commands.
- Always include at least one warning for "sync run" (e.g. "This will write to the target database").
- If the instruction is ambiguous, pick the most likely intent and note the assumption in warnings.
- Do NOT invent table names or columns that aren't in the schema below.
- If the user's table/column doesn't exist, say so in warnings and suggest the closest match.
`)

	// Add schema context if available.
	if schema != nil && len(schema.Tables) > 0 {
		b.WriteString("\n--- DATABASE SCHEMA ---\n")
		b.WriteString("The user's source database has the following tables:\n\n")
		for _, t := range schema.Tables {
			b.WriteString(fmt.Sprintf("Table: %s.%s\n", t.Schema, t.Name))
			if len(t.PrimaryKey) > 0 {
				b.WriteString(fmt.Sprintf("  Primary Key: %s\n", strings.Join(t.PrimaryKey, ", ")))
			}
			b.WriteString("  Columns:\n")
			for _, c := range t.Columns {
				nullStr := ""
				if c.IsNullable {
					nullStr = " (nullable)"
				}
				b.WriteString(fmt.Sprintf("    - %s: %s%s\n", c.Name, c.Type, nullStr))
			}
			if len(t.ForeignKeys) > 0 {
				b.WriteString("  Foreign Keys:\n")
				for _, fk := range t.ForeignKeys {
					b.WriteString(fmt.Sprintf("    - %s(%s) -> %s(%s)\n",
						fk.FromTable, strings.Join(fk.FromColumns, ", "),
						fk.ToTable, strings.Join(fk.ToColumns, ", ")))
				}
			}
			b.WriteString("\n")
		}

		if len(schema.Enums) > 0 {
			b.WriteString("Enum types:\n")
			for name, labels := range schema.Enums {
				b.WriteString(fmt.Sprintf("  %s: %s\n", name, strings.Join(labels, ", ")))
			}
			b.WriteString("\n")
		}

		b.WriteString("--- END SCHEMA ---\n")
	}

	return b.String()
}
