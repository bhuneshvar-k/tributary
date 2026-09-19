package main

import (
	"context"
	"fmt"
	"os"

	"github.com/bhuneshvar-k/tributary/internal/version"
	"github.com/creativeprojects/go-selfupdate"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update tributary to the latest version",
		Long: `Update tributary to the latest version.

This command will attempt to download and install the latest version.
If the self-update fails (e.g., due to permissions), it will display
manual installation instructions.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Current version: %s\n", version.Short())
			fmt.Println("Checking for updates...")

			// Try self-update first
			if err := selfUpdate(); err != nil {
				fmt.Fprintf(os.Stderr, "\n⚠️  Self-update failed: %v\n", err)
				fmt.Println("\nTo update tributary manually, run:")
				fmt.Println("  curl -sSL https://get.tributary.dev | bash")
				fmt.Println("\nOr with Homebrew:")
				fmt.Println("  brew upgrade tributary")
				return nil
			}

			return nil
		},
	}
}

// selfUpdate attempts to download and install the latest version.
func selfUpdate() error {
	// Get current executable path
	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Detect current version
	currentVersion := version.Version()
	if currentVersion == "dev" {
		return fmt.Errorf("cannot update development builds")
	}

	// Create a GitHub source
	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		return fmt.Errorf("failed to create GitHub source: %w", err)
	}

	// Create updater with source
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source: source,
	})
	if err != nil {
		return fmt.Errorf("failed to create updater: %w", err)
	}

	// Find the latest release
	ctx := context.Background()
	release, found, err := updater.DetectLatest(ctx, selfupdate.NewRepositorySlug("bhuneshvar-k", "tributary"))
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	if !found {
		return fmt.Errorf("no releases found")
	}

	latestVersion := release.Version()
	fmt.Printf("Latest version: %s\n", latestVersion)

	// Compare versions
	if release.LessOrEqual(currentVersion) {
		fmt.Println("✓ You're already on the latest version!")
		return nil
	}

	// Download and update
	fmt.Printf("Downloading %s...\n", release.AssetName)
	if err := updater.UpdateTo(ctx, release, exe); err != nil {
		return fmt.Errorf("failed to update: %w", err)
	}

	fmt.Printf("✓ Successfully updated to %s\n", latestVersion)
	return nil
}
