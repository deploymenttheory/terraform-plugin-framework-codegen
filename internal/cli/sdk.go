package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/sdkgen"
)

func newSDKCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "sdk",
		Short: "work with the generated SDK",
	}
	group.AddCommand(newSDKGenerateCommand())
	return group
}

// newSDKGenerateCommand generates the SDK tree from the revised spec with
// the configured backend at its exact version pin: gate the tool on PATH,
// pre-normalize a copy of the revised document, generate into a staging
// directory, scrub nondeterminism, swap the tree into --out, and record
// every file in the manifest under the sdk origin.
func newSDKGenerateCommand() *cobra.Command {
	var (
		dir     string
		out     string
		cfgFile string
	)
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "generate the SDK from the revised spec with the pinned backend",
		Args:  exactArgs("tfpfgen sdk generate [--dir spec] [--out internal/sdk] [--config tfpfgen.yaml]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return err
			}

			res, err := sdkgen.Run(cmd.Context(), sdkgen.Options{
				Config:  cfg,
				SpecDir: dir,
				Out:     out,
				Root:    ".",
			})
			if err != nil {
				return err
			}

			noun := "files"
			if res.Files == 1 {
				noun = "file"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "generated %d %s into %s with %s %s from %s\n",
				res.Files, noun, out, res.Backend, res.Version, res.RevisedPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "spec", "directory holding the revised document")
	cmd.Flags().StringVar(&out, "out", "internal/sdk", "directory the generated SDK tree replaces")
	cmd.Flags().StringVar(&cfgFile, "config", "tfpfgen.yaml", "config file naming the backend and its version pin")
	return cmd
}
