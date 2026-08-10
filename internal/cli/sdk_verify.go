package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/sdkgen"
)

// newSDKVerifyCommand is the SDK drift gate: regenerate into a temporary
// tree with the exact pipeline `sdk generate` runs, byte-compare it against
// the committed tree at --out, and check the committed files against the
// digests the manifest recorded. Drift lists every path with its kind
// (changed | missing | extra | hand-edited) and fails; an identical tree
// confirms in one line.
func newSDKVerifyCommand() *cobra.Command {
	var (
		dir     string
		out     string
		cfgFile string
	)
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "regenerate into a temporary tree and fail if the committed SDK differs",
		Args:  exactArgs("tfpfgen sdk verify [--dir spec] [--out internal/sdk] [--config tfpfgen.yaml]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return err
			}

			rep, err := sdkgen.Verify(cmd.Context(), sdkgen.Options{
				Config:  cfg,
				SpecDir: dir,
				Out:     out,
				Root:    ".",
			})
			if err != nil {
				return err
			}

			if rep.Clean() {
				noun := "files"
				if rep.Files == 1 {
					noun = "file"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s matches regeneration with %s %s: %d %s, no drift\n",
					out, rep.Backend, rep.Version, rep.Files, noun)
				return nil
			}

			for _, d := range rep.Drifts {
				fmt.Fprintln(cmd.OutOrStdout(), d)
			}
			noun := "paths"
			if len(rep.Drifts) == 1 {
				noun = "path"
			}
			return fmt.Errorf("%s has drifted from regeneration: %d %s; run `tfpfgen sdk generate` and commit the result",
				out, len(rep.Drifts), noun)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "spec", "directory holding the revised document")
	cmd.Flags().StringVar(&out, "out", "internal/sdk", "committed SDK tree to verify")
	cmd.Flags().StringVar(&cfgFile, "config", "tfpfgen.yaml", "config file naming the backend and its version pin")
	return cmd
}
