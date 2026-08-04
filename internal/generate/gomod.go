package generate

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// kiotaRuntimeModule is the one module every kiota-generated tree imports; its
// presence is the cheapest offline proof the provider's go.mod has been set up
// for an embedded kiota SDK. go build remains the ultimate arbiter.
const kiotaRuntimeModule = "github.com/microsoft/kiota-abstractions-go"

// AssertGoMod holds the provider's hand-maintained go.mod to what the
// blueprint declares about its SDK.
//
// The file stays the operator's -- generating it outright would mean owning
// indirect blocks and toolchain directives only `go mod tidy` can compute --
// but the requirements the blueprint states are now enforced facts rather than
// hopeful prose: an external SDK must be required at the pinned version, and
// an embedded kiota SDK needs its runtime module. Failure names the exact
// `go get` to run, because "go.mod is wrong" without the fix is a puzzle,
// not a diagnostic.
func AssertGoMod(root string, bp blueprint.Blueprint) error {
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path) //nolint:gosec // fixed name under the operator-supplied root
	if os.IsNotExist(err) {
		// A scratch tree has no module; the postcheck battery skips it too.
		return nil
	}
	if err != nil {
		return err
	}

	f, err := modfile.Parse(path, data, nil)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	requires := map[string]string{}
	for _, r := range f.Require {
		requires[r.Mod.Path] = r.Mod.Version
	}

	sdk := bp.Provider.SDK

	if sdk.Mode == blueprint.SDKModeEmbed {
		if sdk.Generator == "kiota" {
			if _, ok := requires[kiotaRuntimeModule]; !ok {
				return fmt.Errorf("%s does not require %s, which every kiota-generated SDK needs; "+
					"run: cd %s && go get %s", path, kiotaRuntimeModule, root, kiotaRuntimeModule)
			}
		}
		return nil
	}

	// External: the SDK lives in its own module and the provider must require it.
	version, ok := requires[sdk.ModulePath]
	if !ok {
		return fmt.Errorf("%s does not require the SDK module %s the blueprint binds to; "+
			"run: cd %s && go get %s", path, sdk.ModulePath, root, sdk.ModulePath)
	}
	if pinned := bp.Source.SDKVersion; pinned != "" && version != pinned {
		return fmt.Errorf("%s requires %s %s but the blueprint's bindings were resolved against %s; "+
			"bump source.sdkVersion after re-running bindings check, or run: cd %s && go get %s@%s",
			path, sdk.ModulePath, version, pinned, root, sdk.ModulePath, pinned)
	}

	return nil
}
