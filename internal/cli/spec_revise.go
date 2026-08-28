package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/revise"
)

// newSpecReviseCommand runs both halves of revision. First Propose: the
// committed audit observations compile into proposed corrections — or, for
// kinds the config auto-accepts, straight into accepted ones. Then
// Materialize: the pinned upstream document plus every accepted correction
// becomes <dir>/revised.yaml, what both generators read. Materialize refuses
// while <dir>/corrections/proposed/ holds any correction awaiting a human
// decision — no flag exists to look away — so one invocation with fresh
// observations shows the operator exactly what needs deciding.
func newSpecReviseCommand() *cobra.Command {
	var dir, cfgFile string
	var proposeOnly bool
	cmd := &cobra.Command{
		Use:   "revise",
		Short: "compile observations into proposed corrections, then materialize the revised spec",
		Args:  exactArgs("tfpfgen spec revise [--dir spec] [--config tfpfgen.yaml] [--propose-only]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			autoAccept, err := autoAcceptKinds(cfgFile, cmd.Flags().Changed("config"))
			if err != nil {
				return err
			}

			properties, err := revise.ProposeWith(dir, revise.Options{AutoAccept: autoAccept})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if properties.Observations > 0 || proposeOnly {
				printProposals(out, properties)
			}
			if proposeOnly {
				return nil
			}

			res, err := revise.Materialize(dir)
			if err != nil {
				return err
			}

			if len(res.Applied) == 0 {
				fmt.Fprintf(out, "wrote %s: no corrections; the revised spec is the upstream document (sha256 %s)\n",
					res.OutputPath, res.Lock.ShortSHA())
				return nil
			}

			noun := "corrections"
			if len(res.Applied) == 1 {
				noun = "correction"
			}
			fmt.Fprintf(out, "wrote %s: %d %s applied to the upstream document (sha256 %s):\n",
				res.OutputPath, len(res.Applied), noun, res.Lock.ShortSHA())
			for _, f := range res.Applied {
				fmt.Fprintf(out, "  %s\n", filepath.Base(f))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "spec", "directory holding the pinned document, its corrections, and the revised output")
	cmd.Flags().StringVar(&cfgFile, "config", "tfpfgen.yaml", "config file naming the audit.auto_accept kinds")
	cmd.Flags().BoolVar(&proposeOnly, "propose-only", false, "compile observations into proposals and stop before materializing")
	return cmd
}

// autoAcceptKinds reads audit.auto_accept from the config file. An absent
// file at the default location simply means no kinds are auto-accepted —
// revision must work in a tree that has no tfpfgen.yaml — but a path the
// operator named explicitly must exist.
func autoAcceptKinds(cfgFile string, explicit bool) ([]string, error) {
	if _, err := os.Stat(cfgFile); errors.Is(err, os.ErrNotExist) {
		if explicit {
			return nil, fmt.Errorf("--config %s: the file does not exist", cfgFile)
		}
		return nil, nil
	}
	configuration, err := config.Load(cfgFile)
	if err != nil {
		return nil, err
	}
	return configuration.Audit.AutoAccept, nil
}

// printProposals renders one Propose run's report: the summary counts, then
// a line per finding a human should see.
func printProposals(out io.Writer, p revise.Proposals) {
	fmt.Fprintf(out, "compiled %d observations: %d proposed, %d auto-accepted, %d suppressed by rejection markers, %d stale\n",
		p.Observations, len(p.Proposed), len(p.AutoAccepted), len(p.Suppressed), len(p.Stale))
	for _, w := range p.Proposed {
		fmt.Fprintf(out, "  proposed %s — %s\n", filepath.Base(w.Path), describeWritten(w))
	}
	for _, w := range p.AutoAccepted {
		fmt.Fprintf(out, "  auto-accepted %s — %s\n", filepath.Base(w.Path), describeWritten(w))
	}
	for _, n := range p.Suppressed {
		fmt.Fprintf(out, "  suppressed %s: %s\n", describeNote(n), n.Reason)
	}
	for _, n := range p.NoForm {
		fmt.Fprintf(out, "  no correction form exists yet: %s\n", describeNote(n))
	}
	for _, n := range p.Vetoed {
		fmt.Fprintf(out, "  vetoed %s: %s\n", describeNote(n), n.Reason)
	}
	for _, n := range p.Unplaceable {
		fmt.Fprintf(out, "  unplaceable %s: %s\n", describeNote(n), n.Reason)
	}
	for _, n := range p.Stale {
		fmt.Fprintf(out, "  stale %s: observed against a superseded document\n", describeNote(n))
	}
}

func describeWritten(w revise.Written) string {
	return describeNote(revise.Note{Entity: w.Entity, Attribute: w.Attribute, Kind: w.Kind})
}

func describeNote(n revise.Note) string {
	at := n.Entity
	if n.Attribute != "" {
		at += "." + n.Attribute
	}
	return fmt.Sprintf("%s (%s)", at, n.Kind)
}
