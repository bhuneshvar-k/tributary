package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bhuneshvar-k/tributary/internal/ai"
	"github.com/bhuneshvar-k/tributary/internal/catalog"
	"github.com/bhuneshvar-k/tributary/pkg/userconfig"
)

func newAICmd() *cobra.Command {
	var (
		dryRun      bool
		autoConfirm bool
		provider    string
		model       string
		maxTokens   int
		format      string
	)

	cmd := &cobra.Command{
		Use:   `ai "<prompt>"`,
		Short: "Interact with Tributary using natural language",
		Long: `Interact with Tributary using natural language.

Examples:
  tributary ai "sync user admin@example.com"
  tributary ai "plan a subset for all active users"
  tributary ai "show me the schema for the orders table"
  tributary ai --dry-run "sync all orders from last 7 days"
  tributary ai --provider openai "subset users where team = 'marketing'"

The AI understands your database schema (auto-loaded from the configured DSN)
and translates your instruction into a Tributary command.

Configuration:
  tributary config set ai.provider claude
  tributary config set ai.api_key "your-api-key"
  tributary config set ai.model "claude-sonnet-4-20250514"`,
		Args: cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := args[0]

			// Load user config.
			cfg, err := userconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// Resolve AI provider.
			aiCfg := cfg.AI
			if provider != "" {
				aiCfg.Provider = provider
			}
			if model != "" {
				aiCfg.Model = model
			}

			if aiCfg.Provider == "" {
				return fmt.Errorf("AI provider not configured.\n\nSet it with:\n  tributary config set ai.provider claude\n\nValid providers: claude, openai, gemini, openrouter, opencode")
			}
			if aiCfg.APIKey == "" && aiCfg.Provider != "opencode" {
				return fmt.Errorf("AI API key not configured for provider %q.\n\nSet it with:\n  tributary config set ai.api_key \"your-api-key\"", aiCfg.Provider)
			}

			// Create provider.
			prov, err := ai.NewProvider(aiCfg.Provider, aiCfg.APIKey, aiCfg.Model, aiCfg.BaseURL)
			if err != nil {
				return err
			}

			// Build schema context if a DSN is configured.
			var schema *catalog.Schema
			dsn := resolveDSN(cfg)
			if dsn != "" {
				fmt.Fprintf(os.Stderr, "📊 Loading database schema from configured DSN...\n")
				schema, err = catalog.Inspect(context.Background(), dsn)
				if err != nil {
					fmt.Fprintf(os.Stderr, "⚠️  Could not load schema: %v\n", err)
					fmt.Fprintf(os.Stderr, "   Continuing without schema context.\n\n")
				} else {
					fmt.Fprintf(os.Stderr, "   Found %d tables\n\n", len(schema.Tables))
				}
			} else {
				fmt.Fprintf(os.Stderr, "ℹ️  No DSN configured — AI will operate without schema context.\n")
				fmt.Fprintf(os.Stderr, "   Set one with: tributary config set dsn <dsn>\n\n")
			}

			// Build system prompt.
			systemPrompt := ai.BuildSystemPrompt(schema)

			// Call the AI.
			fmt.Fprintf(os.Stderr, "🤖 Thinking...\n\n")
			resp, err := prov.Complete(context.Background(), &ai.Request{
				SystemPrompt: systemPrompt,
				UserPrompt:   prompt,
				Model:        aiCfg.Model,
				MaxTokens:    maxTokens,
				Temperature:  0,
			})
			if err != nil {
				return fmt.Errorf("AI request failed: %w", err)
			}

			// Parse the response.
			parsed, err := ai.ParseResponse(resp.Content)
			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ Failed to parse AI response: %v\n\n", err)
				fmt.Fprintf(os.Stderr, "Raw AI response:\n%s\n", resp.Content)
				return err
			}

			// Display metadata.
			fmt.Fprintf(os.Stderr, "⚡ Provider: %s | Model: %s | Tokens: %d | Latency: %s\n\n",
				prov.Name(), resp.Model, resp.Tokens.TotalTokens, resp.Latency.Round(1))

			// Output format handling.
			switch format {
			case "json":
				return printAIResultJSON(parsed)
			default:
				return printAIResultText(parsed)
			}
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show the generated command without executing it")
	cmd.Flags().BoolVar(&autoConfirm, "auto", false, "Skip confirmation prompt and execute immediately")
	cmd.Flags().StringVar(&provider, "provider", "", "Override AI provider (claude, openai, gemini, openrouter, opencode)")
	cmd.Flags().StringVar(&model, "model", "", "Override the AI model")
	cmd.Flags().IntVar(&maxTokens, "max-tokens", 0, "Override max response tokens")
	cmd.Flags().StringVar(&format, "format", "text", `Output format: "text" or "json"`)

	return cmd
}

// resolveDSN picks the DSN from config. Prefers source_dsn, then dsn.
func resolveDSN(cfg *userconfig.UserConfig) string {
	if cfg.SourceDSN != "" {
		return cfg.SourceDSN
	}
	return cfg.DSN
}

func printAIResultText(cmd *ai.ParsedCommand) error {
	fmt.Println("═══════════════════════════════════════════════")
	fmt.Println("  Tributary AI — Generated Command")
	fmt.Println("═══════════════════════════════════════════════")
	fmt.Println()

	fmt.Printf("  Command:   tributary %s\n", strings.Join(cmd.ToArgs(), " "))
	fmt.Printf("  Purpose:   %s\n", cmd.Explanation)

	if len(cmd.Warnings) > 0 {
		fmt.Println()
		fmt.Println("  ⚠️  Warnings:")
		for _, w := range cmd.Warnings {
			fmt.Printf("     - %s\n", w)
		}
	}

	fmt.Println()

	// Run via executor.
	executor := ai.NewExecutor(false, false)
	return executor.Run(context.Background(), cmd)
}

func printAIResultJSON(cmd *ai.ParsedCommand) error {
	type aiResult struct {
		Command              string            `json:"command"`
		Args                 []string          `json:"args"`
		Explanation          string            `json:"explanation"`
		Warnings             []string          `json:"warnings"`
		RequiresConfirmation bool              `json:"requires_confirmation"`
	}

	result := aiResult{
		Command:              cmd.Command,
		Args:                 cmd.ToArgs(),
		Explanation:          cmd.Explanation,
		Warnings:             cmd.Warnings,
		RequiresConfirmation: cmd.RequiresConfirmation,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
