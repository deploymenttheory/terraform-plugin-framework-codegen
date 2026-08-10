package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/store"
)

func newSpecCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "spec",
		Short: "work with the pinned OpenAPI document",
	}
	group.AddCommand(newSpecImportCommand(), newSpecVerifyCommand())
	return group
}

// newSpecImportCommand pins the upstream document by hash: the bytes go to
// <dir>/upstream.yaml exactly as published, the provenance to
// <dir>/upstream.lock.json. Importing identical content changes nothing;
// different content moves the pin and says what it replaced.
func newSpecImportCommand() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "import <url|file>",
		Short: "pin the upstream OpenAPI document by hash",
		Args:  oneArg("tfpfgen spec import <url|file> [--dir spec]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := args[0]

			doc, err := store.Retrieve(source)
			if err != nil {
				return err
			}

			res, err := store.Import(dir, doc, source)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			switch res.Outcome {
			case store.Unchanged:
				fmt.Fprintf(out, "%s already pins this document (sha256 %s, %s); nothing changed\n",
					res.DocumentPath, res.Lock.ShortSHA(), describeVersions(res.Lock))
			case store.Repinned:
				fmt.Fprintf(out, "re-imported %s into %s:\n  was: sha256 %s, %s\n  now: sha256 %s, %s\n",
					source, res.DocumentPath,
					res.Previous.ShortSHA(), describeVersions(*res.Previous),
					res.Lock.ShortSHA(), describeVersions(res.Lock))
			default:
				fmt.Fprintf(out, "pinned %s as %s (sha256 %s, %s, %s)\n",
					source, res.DocumentPath, res.Lock.ShortSHA(), describeVersions(res.Lock), res.Lock.Format)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "spec", "directory holding the pinned document and its lock")
	return cmd
}

// newSpecVerifyCommand is the drift gate on the pin: the stored document
// must still be the bytes the lock records.
func newSpecVerifyCommand() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "check the pinned document against its lock",
		Args:  exactArgs("tfpfgen spec verify [--dir spec]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			lock, err := store.Verify(dir)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s/%s matches its lock (sha256 %s, %s)\n",
				dir, store.DocumentName, lock.ShortSHA(), describeVersions(lock))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "spec", "directory holding the pinned document and its lock")
	return cmd
}

// describeVersions renders a lock's versions for a message: always the
// OpenAPI dialect, plus the vendor's own version when the document declares
// one.
func describeVersions(l store.Lock) string {
	if l.DocumentVersion == "" {
		return fmt.Sprintf("openapi %s", l.OpenAPI)
	}
	return fmt.Sprintf("openapi %s, version %s", l.OpenAPI, l.DocumentVersion)
}

// oneArg requires exactly one positional argument, answering anything else
// with the verb's own usage line.
func oneArg(usage string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return usagef("usage: %s", usage)
		}
		return nil
	}
}
