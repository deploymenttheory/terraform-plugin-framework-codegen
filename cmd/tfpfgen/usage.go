package main

import (
	"fmt"
	"io"
	"text/tabwriter"
)

const usageHeader = `tfpfgen generates terraform-plugin-framework providers from an API
specification plus recorded API behaviour.

Usage:

    tfpfgen <command> <verb> [flags]

Commands:
`

const usageFooter = `
Global flags, accepted by every command:

    -q            suppress progress output
    -chdir <dir>  change to this directory before running (-C for short)

The pipeline, in the order an author walks it:

    openapi fetch -> blueprint draft -> [probe record -> blueprint merge] -> provider generate

Every stage writes a committed, reviewable artefact; provider generate -check
fails on drift, and CI regenerates each stage the same way. Run
"tfpfgen <command> -h" for a command's verbs and flags.
`

func printUsage(w io.Writer) {
	fmt.Fprint(w, usageHeader)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range commands {
		fmt.Fprintf(tw, "    %s\t%s\n", c.name, c.summary)
	}
	_ = tw.Flush()

	fmt.Fprint(w, usageFooter)
}
