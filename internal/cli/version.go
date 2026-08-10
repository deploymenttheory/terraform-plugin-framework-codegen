package cli

import (
	"fmt"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/version"
)

func runVersion(ctx *Context, args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(ctx.Stderr, "usage: tfpfgen version")
		return ExitUsage
	}
	fmt.Fprintln(ctx.Stdout, version.Version())
	return ExitOK
}
