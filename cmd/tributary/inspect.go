package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
	"github.com/bhuneshvar-k/tributary/pkg/userconfig"
)

func newInspectCmd() *cobra.Command {
	var dsn string

	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Print the schema graph (tables, columns, foreign keys) as JSON",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dsn == "" {
				dsn = os.Getenv("TRIBUTARY_DSN")
			}
			if dsn == "" {
				if userCfg, err := userconfig.Load(); err == nil {
					dsn = userCfg.DSN
				}
			}
			if dsn == "" {
				return fmt.Errorf("--dsn is required (or set TRIBUTARY_DSN, or use 'tributary config set dsn <dsn>')")
			}

			schema, err := catalog.Inspect(context.Background(), dsn)
			if err != nil {
				return err
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(schema)
		},
	}

	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres connection string (or set TRIBUTARY_DSN)")
	return cmd
}
