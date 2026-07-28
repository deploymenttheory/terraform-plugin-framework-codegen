package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/version"
)

func runVersion(args []string) error {
	fs, _ := newFlagSet("version", "version")

	short := fs.Bool("short", false, "print only the version string")

	if err := parse(fs, args); err != nil {
		return err
	}

	if *short {
		fmt.Fprintln(os.Stdout, version.Version)
		return nil
	}

	fmt.Fprintf(os.Stdout, "tfpluginframeworkgen %s (%s %s/%s)\n",
		version.Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)

	return nil
}
