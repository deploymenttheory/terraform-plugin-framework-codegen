package main

import (
	"fmt"
	"io"
	"text/tabwriter"
)

const usageHeader = `tfpluginframeworkgen generates terraform-plugin-framework providers from an API
specification plus recorded API behavior.

Usage:

    tfpluginframeworkgen <command> [flags]

Commands:
`

const usageFooter = `
Global flags, accepted by every command:

    -v            verbose output
    -q            suppress progress output
    -C <dir>      change to this directory before running
    -config <p>   provider generation config (default ` + defaultConfigPath + `)

The pipeline, in the order an author walks it:

    specs -> ingest -> [probe -> merge] -> emit -> verify

Every stage writes a committed, reviewable artefact, and CI regenerates each one
and fails on drift. Run "tfpluginframeworkgen <command> -h" for a command's flags.
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
