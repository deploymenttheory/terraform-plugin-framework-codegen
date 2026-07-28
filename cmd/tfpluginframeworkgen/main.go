// Command tfpluginframeworkgen generates terraform-plugin-framework providers from an
// API specification plus recorded API behavior.
//
//	tfpluginframeworkgen ingest -only Tags
//	tfpluginframeworkgen probe  -blueprint blueprints/thousandeyes -mode replay
//	tfpluginframeworkgen emit   -blueprint blueprints/thousandeyes -out pilot/thousandeyes
//	tfpluginframeworkgen verify -blueprint blueprints/thousandeyes -out pilot/thousandeyes
//
// Subcommands follow the go tool's shape: each owns its own flag.FlagSet, so
// there is no global flag namespace for two subcommands to collide in, and
// argument parsing needs no third-party dependency.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// Exit codes. The probe subcommand publishes its codes as part of its contract,
// so they are declared centrally rather than locally: no other subcommand may
// reuse one of these numbers to mean something else.
const (
	exitOK    = 0
	exitError = 1
	// exitInvalidInput covers a usage error and an invalid plan or config file
	// alike. flag.ExitOnError already exits 2, so aligning with it avoids two
	// codes both meaning "you gave me something I could not use".
	exitInvalidInput    = 2
	exitGatingRefused   = 3
	exitBudgetExceeded  = 4
	exitOrphansLeft     = 5
	exitReplayMismatch  = 6
	exitRedactionFailed = 7
)

// errNotImplemented marks a subcommand that is registered but not yet built.
// Registering the full surface up front means the help output describes the
// intended pipeline from the first commit, and a user who reaches for a missing
// stage gets a straight answer instead of "unknown command".
var errNotImplemented = errors.New("not implemented yet")

// errNothingToDo reports a run whose inputs were valid but selected no work. It
// is an error rather than a silent success because it almost always means a
// mistyped filter, and reporting success would make that look like a working run.
var errNothingToDo = errors.New("nothing to do")

// exitCoder lets a subcommand choose a documented exit code without every
// caller in the chain threading one back up.
type exitCoder interface {
	error
	ExitCode() int
}

// usageError is a caller mistake rather than a failure: the message is printed
// without a stack of wrapping context, and the exit code says "invalid input".
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return exitInvalidInput }

func usagef(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		// A -h on any subcommand surfaces as flag.ErrHelp. The help text has
		// already been printed by then, and asking for help is not a failure.
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(exitOK)
		}

		fmt.Fprintf(os.Stderr, "tfpluginframeworkgen: %v\n", err)

		var coded exitCoder
		if errors.As(err, &coded) {
			os.Exit(coded.ExitCode())
		}
		os.Exit(exitError)
	}
}

// run dispatches to a subcommand. diag receives the usage text printed
// alongside a dispatch error; requested help still goes to stdout, because help
// asked for is output rather than a diagnostic.
func run(args []string, diag io.Writer) error {
	if len(args) == 0 {
		printUsage(diag)
		return usagef("no command given")
	}

	name := args[0]

	switch name {
	case "-h", "-help", "--help", "help":
		printUsage(os.Stdout)
		return nil
	}

	cmd, ok := lookup(name)
	if !ok {
		printUsage(diag)
		return usagef("unknown command %q", name)
	}

	return cmd.run(args[1:])
}
