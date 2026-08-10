package cli

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/providergen"
)

func newProviderCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "provider",
		Short: "work with the generated provider tree",
	}
	group.AddCommand(newProviderGenerateCommand())
	group.AddCommand(newProviderVerifyCommand())
	return group
}

// providerFlags are the inputs `provider generate` and `provider verify`
// share: where the revised spec, the SDK, the provider root and the config
// live.
type providerFlags struct {
	dir     string
	sdk     string
	out     string
	cfgFile string
}

// register binds the shared flags onto one verb.
func (f *providerFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.dir, "dir", "spec", "directory holding the revised document")
	cmd.Flags().StringVar(&f.sdk, "sdk", "internal/sdk", "committed SDK tree the bindings resolve against")
	cmd.Flags().StringVar(&f.out, "out", ".", "provider repo root the generated tree lands in")
	cmd.Flags().StringVar(&f.cfgFile, "config", "tfpfgen.yaml", "config file naming the provider, backend and auth method")
}

// options loads the config and folds the flags into providergen options.
func (f *providerFlags) options() (providergen.Options, error) {
	cfg, err := config.Load(f.cfgFile)
	if err != nil {
		return providergen.Options{}, err
	}
	return providergen.Options{
		Config:  cfg,
		SpecDir: f.dir,
		SDKDir:  f.sdk,
		Root:    f.out,
	}, nil
}

// newProviderGenerateCommand generates the complete provider tree from the
// revised spec and the generated SDK: derive the intermediate
// representation, bind and prune against the real SDK, render the provider
// core and every entity, splice the registrations, land everything through
// a staging directory, and record every file in the manifest. Prunings are
// reported one line each, and — toolchain permitting — the installed tree
// is held to `go mod tidy`, `go build` and `go vet`.
func newProviderGenerateCommand() *cobra.Command {
	var (
		flags     providerFlags
		printIR   bool
		postcheck bool
	)
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "generate the provider tree from the revised spec and the generated SDK",
		Args:  exactArgs("tfpfgen provider generate [--dir spec] [--sdk internal/sdk] [--out .] [--config tfpfgen.yaml] [--print-ir] [--postcheck]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := flags.options()
			if err != nil {
				return err
			}
			opts.Postcheck = postcheck

			out := cmd.OutOrStdout()
			if printIR {
				rendered, err := providergen.IR(opts)
				if err != nil {
					return err
				}
				fmt.Fprint(out, string(rendered))
				return nil
			}

			res, err := providergen.Run(cmd.Context(), opts)
			if err != nil {
				return err
			}

			for _, r := range res.Removals {
				fmt.Fprintf(out, "pruned %s\n", r)
			}
			noun := "files"
			if res.Files == 1 {
				noun = "file"
			}
			fmt.Fprintf(out, "generated %d %s into %s from %s: %d resources, %d datasources, %d list resources, %d actions\n",
				res.Files, noun, res.Root, res.RevisedPath,
				res.Resources, res.Datasources, res.ListResources, res.Actions)
			switch {
			case res.Postcheck.Ran:
				fmt.Fprintln(out, "postcheck passed: go mod tidy, go build, go vet")
			default:
				fmt.Fprintf(out, "postcheck skipped: %s\n", res.Postcheck.SkippedReason)
			}
			return nil
		},
	}
	flags.register(cmd)
	cmd.Flags().BoolVar(&printIR, "print-ir", false, "print the derived intermediate representation as JSON and generate nothing")
	cmd.Flags().BoolVar(&postcheck, "postcheck", goOnPath(), "run go mod tidy, go build and go vet in the generated tree")
	return cmd
}

// newProviderVerifyCommand is the provider drift gate: regenerate into a
// temporary tree with the exact pipeline `provider generate` runs,
// byte-compare it against the committed files, and check the committed
// files against the digests the manifest recorded. Drift lists every path
// with its kind (changed | missing | extra | hand-edited) and fails; an
// identical tree confirms in one line.
func newProviderVerifyCommand() *cobra.Command {
	var flags providerFlags
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "regenerate into a temporary tree and fail if the committed provider differs",
		Args:  exactArgs("tfpfgen provider verify [--dir spec] [--sdk internal/sdk] [--out .] [--config tfpfgen.yaml]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := flags.options()
			if err != nil {
				return err
			}

			rep, err := providergen.Verify(cmd.Context(), opts)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if rep.Clean() {
				noun := "files"
				if rep.Files == 1 {
					noun = "file"
				}
				fmt.Fprintf(out, "%s matches regeneration from %s: %d %s, no drift\n",
					rep.Root, rep.RevisedPath, rep.Files, noun)
				return nil
			}

			for _, d := range rep.Drifts {
				fmt.Fprintln(out, d)
			}
			noun := "paths"
			if len(rep.Drifts) == 1 {
				noun = "path"
			}
			return fmt.Errorf("%s has drifted from regeneration: %d %s; run `tfpfgen provider generate` and commit the result",
				rep.Root, len(rep.Drifts), noun)
		},
	}
	flags.register(cmd)
	return cmd
}

// goOnPath is the postcheck default: on exactly when a go toolchain is
// present to run it.
func goOnPath() bool {
	_, err := exec.LookPath("go")
	return err == nil
}
