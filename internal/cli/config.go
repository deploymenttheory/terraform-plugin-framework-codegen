package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
)

// runConfigValidate is the offline preflight: the config file must be
// well-formed and, with -secrets, the auth role's secrets must be present in
// the environment. It touches no network, so a broken run dies in seconds
// rather than after a long credentialed stage.
func runConfigValidate(ctx *Context, args []string) int {
	fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
	fs.SetOutput(ctx.Stderr)
	file := fs.String("file", "tfpfgen.yaml", "config file to validate")
	secrets := fs.Bool("secrets", false, "also require the auth method's secrets in the environment")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(ctx.Stderr, "usage: tfpfgen config validate [-file tfpfgen.yaml] [-secrets]")
		return ExitUsage
	}

	cfg, err := config.Load(*file)
	if err != nil {
		fmt.Fprintln(ctx.Stderr, err)
		return ExitFailure
	}

	if *secrets {
		missing := config.MissingSecrets(cfg.Auth.Method, os.LookupEnv)
		if len(missing) > 0 {
			fmt.Fprintf(ctx.Stderr, "auth.method %s needs repository secrets that are not set:", cfg.Auth.Method)
			for _, name := range missing {
				fmt.Fprintf(ctx.Stderr, " %s", name)
			}
			fmt.Fprintln(ctx.Stderr)
			return ExitFailure
		}
	}

	fmt.Fprintf(ctx.Stdout, "%s is valid: provider %s, backend %s@%s, auth %s\n",
		*file, cfg.Provider.Name, cfg.SDK.Backend, cfg.SDK.BackendVersion, cfg.Auth.Method)
	return ExitOK
}
