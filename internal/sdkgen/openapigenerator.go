package sdkgen

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
)

// openAPIGeneratorTools are the binary names the tool installs under,
// preferred first: the official npm wrapper and Docker images ship
// `openapi-generator-cli`; Homebrew ships `openapi-generator`.
var openAPIGeneratorTools = []string{"openapi-generator-cli", "openapi-generator"}

// ignoreFileName is the tool's own suppression list, read from the output
// directory before generation starts.
const ignoreFileName = ".openapi-generator-ignore"

// ignoreFileContent suppresses the repository scaffolding the go generator
// writes beside the code: CI config for a repo that does not exist, a push
// script, per-model markdown, and a copy of the input document under api/.
// The generated SDK tree holds compilable code and its provenance, nothing
// else. Test generation and docs rendering are switched off by
// --global-property as well; the ignore file is the belt to those braces.
const ignoreFileContent = `# Written by tfpfgen sdk generate: the SDK tree carries compilable code and
# its provenance, not repository scaffolding for a repo that does not exist.
docs/
api/
.travis.yml
.gitlab-ci.yml
git_push.sh
*.mustache
README.md
.gitignore
`

// openAPIGeneratorBackend drives openapi-generator's go client generator.
type openAPIGeneratorBackend struct{}

func (openAPIGeneratorBackend) Name() string { return config.BackendOpenAPIGenerator }

func (openAPIGeneratorBackend) RequiredVersion(cfg *config.Config) string {
	return cfg.SDK.BackendVersion
}

// tool reports which of the accepted binary names is on PATH.
func (openAPIGeneratorBackend) tool() (string, error) {
	for _, name := range openAPIGeneratorTools {
		if _, err := exec.LookPath(name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("neither %s is on PATH; install the pinned version (e.g. `npm install -g @openapitools/openapi-generator-cli`, "+
		"or `brew install openapi-generator`) — the toolkit never downloads tools",
		strings.Join(openAPIGeneratorTools, " nor "))
}

func (b openAPIGeneratorBackend) CheckTool(ctx context.Context, cfg *config.Config) error {
	name, err := b.tool()
	if err != nil {
		return err
	}
	have, err := toolVersion(ctx, name, nil, "version")
	if err != nil {
		return err
	}
	return gate(name, have, b.RequiredVersion(cfg))
}

func (b openAPIGeneratorBackend) Generate(ctx context.Context, revisedSpecPath string, cfg *config.Config, outDir string) error {
	name, err := b.tool()
	if err != nil {
		return err
	}

	// The CLI has no path-glob flag, so sdk.include_paths/exclude_paths are
	// honoured by filtering the document itself — the same globs, the same
	// meaning kiota's flags give them.
	specPath := revisedSpecPath
	if len(cfg.SDK.IncludePaths)+len(cfg.SDK.ExcludePaths) > 0 {
		doc, err := os.ReadFile(revisedSpecPath) //nolint:gosec // the pre-normalized copy this run wrote
		if err != nil {
			return err
		}
		filtered, err := FilterPaths(doc, cfg.SDK.IncludePaths, cfg.SDK.ExcludePaths)
		if err != nil {
			return err
		}
		specPath = strings.TrimSuffix(revisedSpecPath, filepath.Ext(revisedSpecPath)) + ".filtered.yaml"
		if err := os.WriteFile(specPath, filtered, 0o600); err != nil {
			return err
		}
	}

	// The ignore file must exist before the tool runs — it is read from the
	// output directory at startup.
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, ignoreFileName), []byte(ignoreFileContent), 0o600); err != nil {
		return err
	}

	args := []string{
		"generate",
		"-g", "go",
		"-i", specPath,
		"-o", outDir,
		// hideGenerationTimestamp keeps file headers reproducible;
		// packageName=sdk names the generated package after its place in the
		// provider tree rather than the tool's "openapi" default;
		// withGoMod=false keeps the SDK a package of the provider module, not
		// a module of its own.
		"--additional-properties", "hideGenerationTimestamp=true,packageName=sdk,withGoMod=false",
		// Docs and tests are rendering choices, not code: the provider repo's
		// own CI tests the provider, and per-model markdown is churn.
		"--global-property", "apis,models,supportingFiles,apiDocs=false,modelDocs=false,apiTests=false,modelTests=false",
	}

	_, err = runTool(ctx, name, nil, args...)
	return err
}

// Normalize makes the output a pure function of its inputs: the FILES
// inventory is sorted (the tool appends in generation order, which is not
// guaranteed stable), scaffolding that slipped past the ignore file is
// deleted, and generated file headers lose any dated comment lines. The
// .openapi-generator/VERSION file stays — like kiota's lock, it is the
// evidence of which tool version produced the tree.
func (openAPIGeneratorBackend) Normalize(outDir, _ string) error {
	if err := sortLinesInFile(filepath.Join(outDir, ".openapi-generator", "FILES")); err != nil {
		return err
	}
	for _, name := range []string{"git_push.sh", ".travis.yml", ".gitlab-ci.yml"} {
		if err := os.Remove(filepath.Join(outDir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return scrubDatedHeaders(outDir)
}

// sortLinesInFile rewrites a text file with its lines sorted and
// deduplicated. A missing file is fine — nothing to normalize.
func sortLinesInFile(path string) error {
	data, err := os.ReadFile(path) //nolint:gosec // the fixed name under the staging dir this run wrote
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	sort.Strings(lines)
	kept := lines[:0]
	for i, line := range lines {
		if i == 0 || line != lines[i-1] {
			kept = append(kept, line)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o600)
}
