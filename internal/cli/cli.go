// Package cli assembles the tfpfgen command tree. The grammar is noun-verb
// — `tfpfgen <noun> <verb> [flags]` — with `version` as the one bare verb.
// Cobra owns parsing and help; each verb is a thin adapter that calls into
// an internal package and maps its result onto the exit-code contract.
package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// Exit codes are a documented contract (docs/contract.md). New codes are
// appended, never renumbered.
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
)

// usageError marks an error as the operator's spelling rather than a failed
// operation, so Run can answer with the usage exit code.
type usageError struct{ err error }

func (u usageError) Error() string { return u.err.Error() }

// usagef builds a usageError the way fmt.Errorf builds an error.
func usagef(format string, args ...any) error {
	return usageError{fmt.Errorf(format, args...)}
}

// Run executes one invocation and returns its exit code. Streams are
// injected so tests capture output without touching the process's own.
func Run(args []string, stdout, stderr io.Writer) int {
	root := newRootCommand()
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)

	err := root.Execute()
	if err == nil {
		return ExitOK
	}

	fmt.Fprintf(stderr, "tfpfgen: %v\n", err)
	var u usageError
	// Cobra reports an unmatched subcommand with its own error before any
	// RunE can wrap it; that is a spelling problem all the same.
	if errors.As(err, &u) || strings.HasPrefix(err.Error(), "unknown command") {
		fmt.Fprintln(stderr)
		fmt.Fprint(stderr, root.UsageString())
		return ExitUsage
	}
	return ExitFailure
}

// newRootCommand assembles the full tree. It is rebuilt per Run so no state
// leaks between invocations.
func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "tfpfgen",
		Short: "turn an OpenAPI document into a terraform-plugin-framework provider",
		// Errors and usage are rendered once, by Run, with the exit-code
		// contract applied — not by cobra's own printer.
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("unknown command %q", strings.Join(args, " "))
			}
			return usagef("a command is required — usage: tfpfgen <noun> <verb> [flags]")
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageError{err}
	})

	root.AddCommand(
		newConfigCommand(),
		newSpecCommand(),
		newVersionCommand(),
	)
	return root
}

// exactArgs refuses trailing arguments with the verb's own usage line.
func exactArgs(usage string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != 0 {
			return usagef("usage: %s", usage)
		}
		return nil
	}
}
