package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/quirkserver"
)

// newServeQuirkserverCommand is the hidden development verb that runs the
// quirk server as a real HTTP process — the stand-in live API when the whole
// pipeline is exercised without credentials. Hidden on purpose: it is
// toolkit-development machinery, not part of the CLI contract, so it appears
// in no help output and no docs, and its shape may change without a semver
// conversation.
func newServeQuirkserverCommand() *cobra.Command {
	var addr, specPath string
	cmd := &cobra.Command{
		Use:    "__serve-quirkserver",
		Short:  "run the quirk server as a real HTTP server (toolkit development only)",
		Hidden: true,
		Args:   exactArgs("tfpfgen __serve-quirkserver [--addr 127.0.0.1:0] [--spec PATH]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return serveQuirkserver(ctx, addr, specPath, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:0", "address the quirk server listens on")
	cmd.Flags().StringVar(&specPath, "spec", "",
		"also write the OpenAPI document the server implements to this path")
	return cmd
}

// serveQuirkserver boots the standalone quirk server with its rehearsal
// profile, optionally writes the document it implements, announces the base
// URL, and serves until ctx is done.
func serveQuirkserver(ctx context.Context, addr, specPath string, out io.Writer) error {
	s, err := quirkserver.Standalone(addr, quirkserver.StandaloneQuirks())
	if err != nil {
		return err
	}
	defer s.Close()

	if specPath != "" {
		if err := os.WriteFile(specPath, quirkserver.Spec(), 0o600); err != nil {
			return fmt.Errorf("writing the quirkserver document: %w", err)
		}
		fmt.Fprintf(out, "wrote the OpenAPI document to %s\n", specPath)
	}
	fmt.Fprintf(out, "quirkserver serving at %s — stop with SIGINT or SIGTERM\n", s.BaseURL())

	<-ctx.Done()
	return nil
}
