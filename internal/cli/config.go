package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
)

func newConfigCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "config",
		Short: "work with tfpfgen.yaml",
	}
	group.AddCommand(newConfigValidateCommand())
	return group
}

// newConfigValidateCommand is the offline preflight: the config file must be
// well-formed and, with --secrets, the auth role's secrets must be present
// in the environment. It touches no network, so a broken run dies in
// seconds rather than after a long credentialed stage.
func newConfigValidateCommand() *cobra.Command {
	var (
		file    string
		secrets bool
	)
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "check tfpfgen.yaml and, with --secrets, the environment",
		Args:  exactArgs("tfpfgen config validate [--file tfpfgen.yaml] [--secrets]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(file)
			if err != nil {
				return err
			}

			if secrets {
				missing := config.MissingSecrets(cfg.Auth.Method, os.LookupEnv)
				if len(missing) > 0 {
					return fmt.Errorf("auth.method %s needs repository secrets that are not set: %v",
						cfg.Auth.Method, missing)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%s is valid: provider %s, backend %s@%s, auth %s\n",
				file, cfg.Provider.Name, cfg.SDK.Backend, cfg.SDK.BackendVersion, cfg.Auth.Method)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "tfpfgen.yaml", "config file to validate")
	cmd.Flags().BoolVar(&secrets, "secrets", false, "also require the auth method's secrets in the environment")
	return cmd
}
