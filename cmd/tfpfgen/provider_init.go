package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/openapi"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/snapshot"
)

const usageProviderInit = "provider init [-module DIR] [-name NAME] [-openapi-dir DIR] " +
	"[-out DIR] [-display-name NAME] [-api-endpoint URL] [-force]"

// runProviderInit derives the provider block from what the repository already
// states.
//
// The block used to be the one hand-authored file that gated the whole
// pipeline: nothing in it is a choice. The module path is go.mod's, the client
// type is the generated SDK's own lock, the layout is the fixed convention
// every emitted tree uses, and the source block restates the pinned snapshot.
// Deriving it makes the first pipeline run self-sufficient.
func runProviderInit(args []string) error {
	fs, _ := newFlagSet("provider init", usageProviderInit)

	var (
		module = fs.String("module", ".", "provider module root holding go.mod and the embedded SDK")
		name   = fs.String("name", "",
			"provider registry name (default: the module basename minus terraform-provider-)")
		openapiDir = fs.String("openapi-dir", "",
			"directory holding pinned OpenAPI snapshots (default openapi/<name>)")
		out = fs.String("out", "",
			"blueprint directory to write the provider block into (default blueprints/<name>)")
		force = fs.Bool("force", false, "overwrite an existing provider block")
		// Two settings a document cannot supply. The endpoint is derivable only
		// when the document names an absolute server, and house capitalisation
		// ("ThousandEyes", "Jamf Pro") is not derivable at all -- a generator
		// that guesses it is wrong for every vendor whose name is not one
		// lowercase word. Stating them here is what keeps the correction out of
		// the generated blueprint, which nobody may hand-edit.
		displayNameFlag = fs.String("display-name", "",
			"provider display name for prose, e.g. ThousandEyes or Jamf Pro (default: the name, capitalised)")
		apiEndpointFlag = fs.String("api-endpoint", "",
			"API base URL (default: the document's server, when it names an absolute one)")
	)

	if err := parse(fs, args); err != nil {
		return err
	}

	goModule, err := goModulePath(filepath.Join(*module, "go.mod"))
	if err != nil {
		return err
	}

	if *name == "" {
		*name = strings.TrimPrefix(filepath.Base(goModule), "terraform-provider-")
	}
	if *openapiDir == "" {
		*openapiDir = filepath.Join("openapi", *name)
	}
	if *out == "" {
		*out = filepath.Join("blueprints", *name)
	}

	// The client type is the generated SDK's own statement, not a guess: kiota
	// records the class name it emitted in its lock. Requiring the lock also
	// enforces the pipeline order -- the SDK exists before anything binds to it.
	clientClass, err := kiotaClientClass(filepath.Join(*module, "internal", "sdk", "kiota-lock.json"))
	if err != nil {
		return err
	}

	snap, err := snapshot.Find(*openapiDir, "")
	if err != nil {
		return err
	}
	if err := snap.Verify(); err != nil {
		return err
	}
	meta, err := snap.LoadMetadata()
	if err != nil {
		return err
	}

	// What the document says about proving who you are. A specification that
	// declares its security schemes has already answered this, and asking a
	// person to restate it invites them to state it differently.
	auth := blueprint.Auth{Method: blueprint.AuthBearerToken}
	apiEndpoint := ""
	if doc, dErr := openapi.Load(snap.SpecPath()); dErr == nil {
		// The document's own server, when it names a host. A relative server
		// means the host is per-installation, so there is nothing to default to.
		apiEndpoint = doc.ServerURL()
		if apiEndpoint == "" {
			log.Printf("endpoint: the document names no absolute server; the provider will require one")
		} else {
			log.Printf("endpoint: %s, from the document's server", apiEndpoint)
		}
		if derived, ok := doc.Auth(); ok {
			auth = derived
			log.Printf("auth: %s, derived from the document's security schemes", auth.Resolved())
		} else {
			log.Printf("auth: the document declares no usable security scheme; defaulting to %s",
				auth.Resolved())
		}
	}

	// A stated endpoint wins over the document's: a self-hosted API's server is
	// per-customer, so the document either names none or names the vendor's own.
	if *apiEndpointFlag != "" {
		if apiEndpoint != "" && apiEndpoint != *apiEndpointFlag {
			log.Printf("endpoint: %s, stated; the document said %s", *apiEndpointFlag, apiEndpoint)
		} else {
			log.Printf("endpoint: %s, stated", *apiEndpointFlag)
		}
		apiEndpoint = *apiEndpointFlag
	}

	resolvedDisplayName := *displayNameFlag
	if resolvedDisplayName == "" {
		resolvedDisplayName = displayName(*name)
		log.Printf("display name: %s, from the provider name; state -display-name if the vendor "+
			"capitalises it differently", resolvedDisplayName)
	}

	bp := blueprint.Blueprint{
		FormatVersion: blueprint.FormatVersion,
		Provider: blueprint.Provider{
			Name:        *name,
			GoModule:    goModule,
			TypePrefix:  *name,
			DisplayName: resolvedDisplayName,
			APIEndpoint: apiEndpoint,
			Auth:        auth,
			SDK: blueprint.SDKModule{
				Dialect:    blueprint.DialectKiotaFluent,
				Mode:       blueprint.SDKModeEmbed,
				Generator:  "kiota",
				ModulePath: goModule,
				ClientType: "*sdk." + clientClass,
				ClientImport: blueprint.Import{
					Path:  goModule + "/internal/sdk",
					Alias: "sdk",
				},
			},
			Conventions: blueprint.Conventions{
				ResourceRoot:   "internal/services/resources",
				DataSourceRoot: "internal/services/datasources",
				ProviderPkgDir: "internal/provider",
				DefaultTimeouts: blueprint.Timeouts{
					CreateSeconds: 180, ReadSeconds: 180, UpdateSeconds: 180, DeleteSeconds: 180,
				},
			},
			Support: blueprint.SupportPkgs{
				Convert:      blueprint.Import{Path: goModule + "/internal/services/common/convert"},
				CRUD:         blueprint.Import{Path: goModule + "/internal/services/common/crud"},
				CommonSchema: blueprint.Import{Path: goModule + "/internal/services/common/schema", Alias: "commonschema"},
				Errors:       blueprint.Import{Path: goModule + "/internal/services/common/errors"},
				Client:       blueprint.Import{Path: goModule + "/internal/client"},
			},
		},
		Source: blueprint.SourceInfo{
			SpecFile:    snapshot.SpecFileName,
			SpecVersion: meta.Version,
			SpecSHA256:  meta.SHA256,
			SnapshotDir: snap.Name,
		},
	}

	path := filepath.Join(*out, "provider"+blueprint.Ext)
	if _, err := os.Stat(path); err == nil && !*force {
		log.Printf("kept      %s (already exists; -force overwrites)", path)
		return nil
	}

	if err := blueprint.Save(path, bp); err != nil {
		return err
	}

	log.Printf("wrote     %s (module %s, client %s, snapshot %s)", path, goModule, clientClass, snap.Name)

	return nil
}

// goModulePath reads the module path go.mod declares.
func goModulePath(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path by design
	if err != nil {
		return "", fmt.Errorf("reading the provider module: %w", err)
	}
	mp := modfile.ModulePath(data)
	if mp == "" {
		return "", fmt.Errorf("%s declares no module path", path)
	}
	return mp, nil
}

// kiotaClientClass reads the root client type name off the SDK's own lock.
func kiotaClientClass(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path by design
	if err != nil {
		return "", fmt.Errorf("reading the SDK lock (run sdk generate first): %w", err)
	}
	var lock struct {
		ClientClassName string `json:"clientClassName"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if lock.ClientClassName == "" {
		return "", fmt.Errorf("%s names no clientClassName", path)
	}
	return lock.ClientClassName, nil
}

// displayName renders a provider name the way prose wants it: thousandeyes
// becomes Thousandeyes, which a person then corrects to ThousandEyes in the
// blueprint if the capitalisation matters to them. Guessing the house
// capitalisation of an arbitrary vendor is not something a generator can do.
func displayName(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}
