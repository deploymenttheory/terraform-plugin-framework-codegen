package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

func modRoot(t *testing.T, gomod string) string {
	t.Helper()
	root := t.TempDir()
	if gomod != "" {
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(gomod), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func sdkBlueprint(mode, generator, modulePath, sdkVersion string) blueprint.Blueprint {
	var bp blueprint.Blueprint
	bp.Provider.GoModule = "example.com/prov"
	bp.Provider.SDK = blueprint.SDKModule{
		Mode:       mode,
		Generator:  generator,
		ModulePath: modulePath,
	}
	bp.Source.SDKVersion = sdkVersion
	return bp
}

func TestUnit_Generate_GoModAssertion(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		gomod   string
		bp      blueprint.Blueprint
		wantErr string
	}{
		"no module root is skipped": {
			gomod: "",
			bp:    sdkBlueprint("", "", "example.com/sdk", ""),
		},
		"external sdk present at the pinned version": {
			gomod: "module example.com/prov\n\ngo 1.25\n\nrequire example.com/sdk v0.2.0\n",
			bp:    sdkBlueprint("", "", "example.com/sdk", "v0.2.0"),
		},
		"external sdk missing names the go get": {
			gomod:   "module example.com/prov\n\ngo 1.25\n",
			bp:      sdkBlueprint("", "", "example.com/sdk", ""),
			wantErr: "go get example.com/sdk",
		},
		"external sdk at the wrong version names both": {
			gomod:   "module example.com/prov\n\ngo 1.25\n\nrequire example.com/sdk v0.1.0\n",
			bp:      sdkBlueprint("", "", "example.com/sdk", "v0.2.0"),
			wantErr: "v0.1.0",
		},
		"embedded kiota sdk needs the runtime module": {
			gomod:   "module example.com/prov\n\ngo 1.25\n",
			bp:      sdkBlueprint(blueprint.SDKModeEmbed, "kiota", "example.com/prov", ""),
			wantErr: kiotaRuntimeModule,
		},
		"embedded kiota sdk with the runtime module passes": {
			gomod: "module example.com/prov\n\ngo 1.25\n\nrequire " + kiotaRuntimeModule + " v1.9.4\n",
			bp:    sdkBlueprint(blueprint.SDKModeEmbed, "kiota", "example.com/prov", ""),
		},
		"embedded hand-written sdk needs nothing": {
			gomod: "module example.com/prov\n\ngo 1.25\n",
			bp:    sdkBlueprint(blueprint.SDKModeEmbed, "", "example.com/prov", ""),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := AssertGoMod(modRoot(t, tc.gomod), tc.bp)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("AssertGoMod: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("AssertGoMod error = %v, want it to name %q", err, tc.wantErr)
			}
		})
	}
}
