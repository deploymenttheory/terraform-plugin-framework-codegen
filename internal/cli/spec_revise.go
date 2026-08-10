package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/revise"
)

// newSpecReviseCommand materializes the revised spec: the pinned upstream
// document with every accepted correction applied, written to
// <dir>/revised.yaml — what both generators read. It refuses while
// <dir>/corrections/proposed/ holds any correction awaiting a human
// decision; no flag exists to look away.
func newSpecReviseCommand() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "revise",
		Short: "materialize the revised spec from the pin plus accepted corrections",
		Args:  exactArgs("tfpfgen spec revise [--dir spec]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := revise.Materialize(dir)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
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
	return cmd
}
