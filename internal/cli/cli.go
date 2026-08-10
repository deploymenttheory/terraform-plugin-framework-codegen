// Package cli dispatches the tfpfgen command line. The grammar is
// noun-verb — `tfpfgen <noun> <verb> [flags]` — with `version` as the one
// bare verb. Verbs register themselves in the table below; this package owns
// only resolution, usage rendering, and the exit-code contract.
package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Exit codes are a documented contract (docs/contract.md). New codes are
// appended, never renumbered.
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
)

// Context carries the streams a command writes to, so tests can capture
// output without touching the process's own stdout and stderr.
type Context struct {
	Stdout io.Writer
	Stderr io.Writer
}

// Command is one resolvable entry in the grammar. Name is the full spelling
// the operator types: "config validate", or "version" for the bare verb.
type Command struct {
	Name    string
	Summary string
	Run     func(ctx *Context, args []string) int
}

// commands returns the registry in display order. It is a function, not a
// package variable, so each Run starts from a clean table.
func commands() []Command {
	return []Command{
		{Name: "config validate", Summary: "check tfpfgen.yaml and, with -secrets, the environment", Run: runConfigValidate},
		{Name: "version", Summary: "report the toolkit version", Run: runVersion},
	}
}

// Run resolves args against the registry and executes the matched command.
func Run(args []string, stdout, stderr io.Writer) int {
	ctx := &Context{Stdout: stdout, Stderr: stderr}
	if len(args) == 0 {
		usage(ctx.Stderr)
		return ExitUsage
	}

	table := commands()
	// Longest spelling first: a two-word match beats a one-word match.
	if len(args) >= 2 {
		name := args[0] + " " + args[1]
		for _, c := range table {
			if c.Name == name {
				return c.Run(ctx, args[2:])
			}
		}
	}
	for _, c := range table {
		if c.Name == args[0] {
			return c.Run(ctx, args[1:])
		}
	}

	fmt.Fprintf(ctx.Stderr, "tfpfgen: unknown command %q\n\n", strings.Join(args, " "))
	usage(ctx.Stderr)
	return ExitUsage
}

// usage renders the registry grouped by noun.
func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: tfpfgen <noun> <verb> [flags]")
	fmt.Fprintln(w)
	names := commands()
	sort.SliceStable(names, func(i, j int) bool { return names[i].Name < names[j].Name })
	for _, c := range names {
		fmt.Fprintf(w, "  %-24s %s\n", c.Name, c.Summary)
	}
}
