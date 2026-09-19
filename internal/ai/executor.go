package ai

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Executor runs parsed Tributary commands.
type Executor struct {
	// DryRun when true prints the command but does not execute it.
	DryRun bool

	// AutoConfirm skips the interactive confirmation prompt.
	AutoConfirm bool

	// TributaryBin is the path to the tributary binary. Empty = "tributary"
	// (resolved via PATH).
	TributaryBin string

	// Stdin is used for interactive prompts. Defaults to os.Stdin.
	Stdin *os.File

	// Stdout is used for command output. Defaults to os.Stdout.
	Stdout *os.File

	// Stderr is used for warnings and errors. Defaults to os.Stderr.
	Stderr *os.File
}

// NewExecutor creates an executor with sensible defaults.
func NewExecutor(dryRun, autoConfirm bool) *Executor {
	return &Executor{
		DryRun:      dryRun,
		AutoConfirm: autoConfirm,
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
	}
}

// Run executes a parsed command. For "inspect" and "plan" it runs directly;
// for "sync run" it asks for confirmation unless AutoConfirm is set or DryRun
// is true.
func (e *Executor) Run(ctx context.Context, cmd *ParsedCommand) error {
	args := cmd.ToArgs()

	// "inspect" and "plan" always run without confirmation.
	if cmd.Command != "sync run" {
		return e.execute(ctx, args)
	}

	// sync run: show what we're about to do.
	if e.DryRun {
		e.printPreview(cmd)
		return nil
	}

	// Print the command for the user.
	e.printPreview(cmd)

	// Confirm.
	if !e.AutoConfirm {
		if !e.confirm() {
			fmt.Fprintln(e.Stderr, "Aborted.")
			return nil
		}
	}

	return e.execute(ctx, args)
}

// printPreview shows the command and explanation before execution.
func (e *Executor) printPreview(cmd *ParsedCommand) {
	fmt.Fprintf(e.Stderr, "\n🔧 Tributary AI generated command:\n\n")
	fmt.Fprintf(e.Stderr, "  tributary %s\n\n", strings.Join(cmd.ToArgs(), " "))
	fmt.Fprintf(e.Stderr, "📝 %s\n", cmd.Explanation)
	if len(cmd.Warnings) > 0 {
		fmt.Fprintln(e.Stderr, "\n⚠️  Warnings:")
		for _, w := range cmd.Warnings {
			fmt.Fprintf(e.Stderr, "  - %s\n", w)
		}
	}
	fmt.Fprintln(e.Stderr)
}

// confirm asks the user y/N.
func (e *Executor) confirm() bool {
	fmt.Fprint(e.Stderr, "Execute this command? [y/N] ")
	scanner := bufio.NewScanner(e.Stdin)
	if scanner.Scan() {
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return answer == "y" || answer == "yes"
	}
	return false
}

// execute runs the tributary binary with the given args.
func (e *Executor) execute(ctx context.Context, args []string) error {
	bin := e.TributaryBin
	if bin == "" {
		bin = "tributary"
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = e.Stdout
	cmd.Stderr = e.Stderr
	cmd.Stdin = e.Stdin

	fmt.Fprintf(e.Stderr, "🚀 Running: tributary %s\n\n", strings.Join(args, " "))

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tributary %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
