// tfpfgen turns an OpenAPI document into a terraform-plugin-framework
// provider. All behavior lives in internal packages; this file only hands
// the process arguments to the dispatcher and exits with its verdict.
package main

import (
	"os"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
