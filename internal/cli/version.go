package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/version"
)

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "report the toolkit version",
		Args:  exactArgs("tfpfgen version"),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), version.Version())
			return nil
		},
	}
}
