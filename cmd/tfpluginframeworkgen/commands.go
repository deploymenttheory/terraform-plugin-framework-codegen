package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
)

// command is one subcommand. run receives the arguments after the subcommand
// name and owns its own flag parsing, so adding a flag to one subcommand cannot
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
var commands = []command{
	{
		name:    "specs",
		summary: "fetch and snapshot an upstream OpenAPI document",
		usage:   "specs [-output-dir DIR] [-dry-run]",
		run:     notImplemented("specs"),
	},
	{
		name:    "ingest",
		summary: "infer a provider blueprint from an OpenAPI snapshot",
		usage:   "ingest [-spec FILE] [-out DIR] [-only TAG] [-list]",
		run:     runIngest,
	},
	{
		name:    "blueprint",
		summary: "validate, diff or list blueprints",
		usage:   "blueprint <validate|diff|list> [flags]",
		run:     notImplemented("blueprint"),
	},
	{
		name:    "probe",
		summary: "exercise a resource's lifecycle; record or replay cassettes",
		usage:   "probe [-mode record|replay|verify] [-blueprint FILE] [--allow-mutations]",
		run:     notImplemented("probe"),
	},
	{
		name:    "merge",
		summary: "fold probe facts into a blueprint",
		usage:   "merge -blueprint FILE -facts FILE [-strategy annotate]",
		run:     notImplemented("merge"),
	},
	{
		name:    "emit",
		summary: "render a terraform-plugin-framework provider from blueprints",
		usage:   "emit -blueprint DIR -out DIR [-only NAME] [-dry-run]",
		run:     runEmit,
	},
	{
		name:    "verify",
		summary: "fail if the committed provider has drifted from its blueprints",
		usage:   "verify -blueprint DIR -out DIR",
		run:     runVerify,
	},
	{
		name:    "scaffold",
		summary: "write a blank resource from the archetype, registered and compiling",
		usage:   "scaffold -name NAME [-kind resource|datasource]",
		run:     notImplemented("scaffold"),
	},
	{
		name:    "bindings",
		summary: "check blueprint SDK bindings against the pinned SDK",
		usage:   "bindings -blueprint DIR -module DIR",
		run:     runBindings,
	},
	{
		name:    "interop",
		summary: "export or import terraform-plugin-codegen-spec v0.1 JSON",
		usage:   "interop <export|import> [flags]",
		run:     runInterop,
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

// notImplemented returns a run function for a subcommand that is registered but
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
// FlagSet rather than parsed before dispatch, so `tfpluginframeworkgen emit -v` reads
// naturally and there is no "flags must come before the command" rule to learn.
type globals struct {
	verbose bool
	quiet   bool
	chdir   string
	config  string
}

const defaultConfigPath = ".tfpluginframeworkgen/config.yaml"

func newFlagSet(name, usage string) (*flag.FlagSet, *globals) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)

	// flag writes both its parse errors and its usage text to one destination,
	// which conflates two different things: requested help belongs on stdout so
	// it can be piped, and an error belongs on stderr. Discarding flag's own
	// output and printing from parse() is what keeps those separate.
	fs.SetOutput(io.Discard)

	g := &globals{}
	fs.BoolVar(&g.verbose, "v", false, "verbose output")
	fs.BoolVar(&g.quiet, "q", false, "suppress progress output")
	fs.StringVar(&g.chdir, "C", "", "change to this directory before running")
	fs.StringVar(&g.config, "config", defaultConfigPath, "path to the provider generation config")

	if usage == "" {
		usage = name + " [flags]"
	}
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintf(out, "Usage: tfpluginframeworkgen %s\n\nFlags:\n", usage)
		fs.PrintDefaults()
	}

	return fs, g
}

// parse parses args and applies the global flags. Every subcommand calls it
// rather than fs.Parse directly, so -C and -q cannot be honoured by some
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
	if f := fs.Lookup("C"); f != nil {
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
