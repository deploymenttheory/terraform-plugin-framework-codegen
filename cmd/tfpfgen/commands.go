package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

// command is one subcommand or verb. run receives the arguments after the name
// and owns its own flag parsing, so adding a flag to one subcommand cannot
// affect another.
type command struct {
	name    string
	summary string
	// usage is the one-line argument sketch shown above the flag list.
	usage string
	run   func(args []string) error
}

// commands is ordered for the help output: it reads as the pipeline stages in
// the order an author walks them, not alphabetically.
//
// The grammar is noun-group throughout: the first word names the artefact, the
// second what to do to it. Every group dispatches through runVerbs, so the verb
// tables all behave identically.
var commands = []command{
	{
		name:    "openapi",
		summary: "fetch and pin upstream OpenAPI documents",
		usage:   "openapi <fetch> [flags]",
		run:     runOpenAPI,
	},
	{
		name:    "blueprint",
		summary: "draft, merge, validate, diff or list blueprints",
		usage:   "blueprint <draft|merge|validate|diff|list> [flags]",
		run:     runBlueprint,
	},
	{
		name:    "probe",
		summary: "exercise a resource's lifecycle; record or replay cassettes",
		usage:   "probe <record|replay|verify|sweep|list> [flags]",
		run:     runProbe,
	},
	{
		name:    "provider",
		summary: "generate a terraform-plugin-framework provider from blueprints",
		usage:   "provider <generate|scaffold> [flags]",
		run:     runProvider,
	},
	{
		name:    "bindings",
		summary: "check blueprint SDK bindings against the pinned SDK",
		usage:   "bindings <check|facts> [flags]",
		run:     runBindingsGroup,
	},
	{
		name:    "spec",
		summary: "export or import Provider Code Specification v0.1 JSON",
		usage:   "spec <export|import> [flags]",
		run:     runSpecGroup,
	},
	{
		name:    "version",
		summary: "print the toolkit version and exit",
		usage:   "version",
		run:     runVersion,
	},
}

func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// runVerbs dispatches one noun group's verb table.
//
// defaultVerb, when non-empty, is what a bare `tfpfgen <group> -flags` means --
// probe uses it so the safe verb (replay) is what you get by typing less. A
// group without a default demands its verb, because guessing one would make
// `blueprint -blueprint DIR` silently pick an action nobody named.
func runVerbs(group string, verbs []command, defaultVerb string, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "-help", "--help", "help":
			// A request for the verb list is output, not a diagnostic.
			printVerbs(os.Stdout, group, verbs)
			return nil
		}
	}

	verb, rest := defaultVerb, args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, rest = args[0], args[1:]
	}

	if verb == "" {
		printVerbs(os.Stderr, group, verbs)
		return usagef("%s needs a verb", group)
	}

	for _, v := range verbs {
		if v.name == verb {
			return v.run(rest)
		}
	}

	printVerbs(os.Stderr, group, verbs)
	return usagef("%s: unknown verb %q", group, verb)
}

func printVerbs(w io.Writer, group string, verbs []command) {
	fmt.Fprintf(w, "Usage: tfpfgen %s <verb> [flags]\n\nVerbs:\n", group)
	for _, v := range verbs {
		fmt.Fprintf(w, "  %-10s %s\n", v.name, v.summary)
	}
	fmt.Fprintln(w)
}

// notImplemented returns a run function for a verb that is registered but
// unbuilt. It still parses the global flags, so `-h` prints something useful.
func notImplemented(name string) func([]string) error {
	return func(args []string) error {
		fs, _ := newFlagSet(name, "")
		if err := parse(fs, args); err != nil {
			return err
		}
		return fmt.Errorf("%s: %w", name, errNotImplemented)
	}
}

// globals are the flags every subcommand accepts. They are registered per
// FlagSet rather than parsed before dispatch, so `tfpfgen provider generate -q`
// reads naturally and there is no "flags must come before the command" rule to
// learn.
type globals struct {
	quiet bool
	chdir string
}

func newFlagSet(name, usage string) (*flag.FlagSet, *globals) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)

	// flag writes both its parse errors and its usage text to one destination,
	// which conflates two different things: requested help belongs on stdout so
	// it can be piped, and an error belongs on stderr. Discarding flag's own
	// output and printing from parse() is what keeps those separate.
	fs.SetOutput(io.Discard)

	g := &globals{}
	fs.BoolVar(&g.quiet, "q", false, "suppress progress output")
	fs.StringVar(&g.chdir, "chdir", "", "change to this directory before running")
	fs.StringVar(&g.chdir, "C", "", "shorthand for -chdir")

	if usage == "" {
		usage = name + " [flags]"
	}
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintf(out, "Usage: tfpfgen %s\n\nFlags:\n", usage)
		fs.PrintDefaults()
	}

	return fs, g
}

// parse parses args and applies the global flags. Every subcommand calls it
// rather than fs.Parse directly, so -chdir and -q cannot be honoured by some
// subcommands and silently ignored by others, and so help and errors reach the
// right stream with the right exit code.
func parse(fs *flag.FlagSet, args []string) error {
	err := fs.Parse(args)

	switch {
	case err == nil:
		return applyGlobals(fs)

	case errors.Is(err, flag.ErrHelp):
		// Help was asked for, so it is output, not a diagnostic.
		fs.SetOutput(os.Stdout)
		fs.Usage()
		return flag.ErrHelp

	default:
		// A malformed flag is a caller mistake: usageError carries the
		// invalid-input exit code so it cannot be confused with a real failure.
		fs.SetOutput(os.Stderr)
		fs.Usage()
		return usagef("%v", err)
	}
}

func applyGlobals(fs *flag.FlagSet) error {
	var (
		chdir string
		quiet bool
	)
	// -chdir and -C share one destination; whichever was set last on the
	// command line wins, and Lookup on either name sees the same value.
	if f := fs.Lookup("chdir"); f != nil {
		chdir = f.Value.String()
	}
	if f := fs.Lookup("q"); f != nil {
		quiet = f.Value.String() == "true"
	}

	if chdir != "" {
		if err := os.Chdir(chdir); err != nil {
			return fmt.Errorf("changing directory to %s: %w", chdir, err)
		}
	}

	// Progress goes to the log package, as in the sibling generators, so -q is
	// one assignment rather than a flag threaded through every call site.
	log.SetFlags(0)
	if quiet {
		log.SetOutput(io.Discard)
	}

	return nil
}
