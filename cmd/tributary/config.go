package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bhuneshvar-k/tributary/pkg/userconfig"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage user-level configuration (~/.tributary/config.yaml)",
		Long: `View and modify persistent user configuration. Config values are
used as fallbacks when CLI flags and environment variables are not set.

Precedence: CLI flags > environment variables > config file.

The config file is optional — tributary works without it.`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newConfigSetCmd())
	cmd.AddCommand(newConfigGetCmd())
	cmd.AddCommand(newConfigListCmd())
	cmd.AddCommand(newConfigUnsetCmd())
	cmd.AddCommand(newConfigPathCmd())
	return cmd
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a config value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]

			cfg, err := userconfig.Load()
			if err != nil {
				return err
			}

			if err := userconfig.Set(cfg, key, value); err != nil {
				return err
			}

			if err := userconfig.Save(cfg); err != nil {
				return err
			}

			path, _ := userconfig.Path()
			fmt.Fprintf(os.Stderr, "Set %s in %s\n", key, path)
			return nil
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a config value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := userconfig.Load()
			if err != nil {
				return err
			}

			val := userconfig.Get(cfg, args[0])
			if val == "" {
				return fmt.Errorf("%q is not set", args[0])
			}
			fmt.Println(val)
			return nil
		},
	}
}

func newConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all config values",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := userconfig.Load()
			if err != nil {
				return err
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "key\tvalue\tdescription")
			fmt.Fprintln(tw, "---\t-----\t-----------")
			for _, key := range userconfig.Keys() {
				val := userconfig.Get(cfg, key)
				desc := userconfig.KeyDescription(key)
				if val == "" {
					val = "(not set)"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", key, val, desc)
			}
			tw.Flush()

			path, _ := userconfig.Path()
			fmt.Fprintf(os.Stderr, "\nconfig file: %s\n", path)
			return nil
		},
	}
}

func newConfigUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove a config value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := userconfig.Load()
			if err != nil {
				return err
			}

			if err := userconfig.Unset(cfg, args[0]); err != nil {
				return err
			}

			if err := userconfig.Save(cfg); err != nil {
				return err
			}

			path, _ := userconfig.Path()
			fmt.Fprintf(os.Stderr, "Unset %s in %s\n", args[0], path)
			return nil
		},
	}
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the config file path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := userconfig.Path()
			if err != nil {
				return err
			}
			fmt.Println(path)
			return nil
		},
	}
}
