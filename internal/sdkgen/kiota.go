package sdkgen

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/emit"
)

// KiotaLockName is kiota's own record of a generation, written into the
// output directory. Kept deliberately: its kiotaVersion and descriptionHash
// are what make a regeneration provable.
const KiotaLockName = "kiota-lock.json"

// kiotaEnv keeps kiota deterministic and quiet: no update check, no
// telemetry, no colour codes in captured output.
var kiotaEnv = []string{
	"KIOTA_OFFLINE_ENABLED=true",
	"KIOTA_CLI_TELEMETRY_OPTOUT=true",
	"KIOTA_TUTORIAL_ENABLED=false",
	"KIOTA_CONSOLE_COLORS_ENABLED=false",
}

// kiotaBackend drives Microsoft Kiota.
type kiotaBackend struct{}

func (kiotaBackend) Name() string { return config.BackendKiota }

func (kiotaBackend) RequiredVersion(configuration *config.Config) string {
	return configuration.SDK.BackendVersion
}

func (b kiotaBackend) CheckTool(ctx context.Context, configuration *config.Config) error {
	if _, err := exec.LookPath("kiota"); err != nil {
		return fmt.Errorf("kiota is not on PATH; install the pinned %s (e.g. `brew install kiota`, or the "+
			"release archive from github.com/microsoft/kiota/releases) — the toolkit never downloads tools: %w",
			b.RequiredVersion(configuration), err)
	}
	have, err := toolVersion(ctx, "kiota", kiotaEnv, "--version")
	if err != nil {
		return err
	}
	return refuseVersionMismatch("kiota", have, b.RequiredVersion(configuration))
}

func (kiotaBackend) Generate(ctx context.Context, revisedSpecPath string, configuration *config.Config, outDir string) error {
	// The root namespace must be the import path the provider module gives
	// the SDK tree, or kiota's generated cross-package imports ("<ns>/things",
	// "<ns>/models") resolve nowhere. emit owns that spelling — module path
	// plus internal/sdk — and deriving it here keeps one definition site.
	// Without the flag kiota defaults to "ApiSdk", which type-checks in no
	// provider repo.
	pc, err := emit.FromConfig(configuration, "")
	if err != nil {
		return err
	}

	args := []string{
		"generate",
		"--language", "go",
		"--openapi", revisedSpecPath,
		"--output", outDir,
		"--class-name", configuration.SDK.ClientTypeName,
		"--namespace-name", pc.SDKImport,
		// The deprecated string indexers double the surface for nothing a
		// generated provider would ever call.
		"--exclude-backward-compatible",
		// outDir is always a fresh staging directory, but --clean-output makes
		// that a property of the invocation rather than of the caller.
		"--clean-output",
	}
	for _, g := range configuration.SDK.IncludePaths {
		args = append(args, "--include-path", g)
	}
	for _, g := range configuration.SDK.ExcludePaths {
		args = append(args, "--exclude-path", g)
	}

	if _, err := runTool(ctx, "kiota", kiotaEnv, args...); err != nil {
		return err
	}

	// kiota drops a timestamped log file into the output; a timestamp is the
	// one thing a reproducible tree must not carry.
	if err := os.Remove(filepath.Join(outDir, ".kiota.log")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return refuseUnreferencedRequestBodyEnums(outDir)
}

// Normalize scrubs kiota-lock.json. The lock's kiotaVersion and
// descriptionHash stay — they are the evidence that makes a regeneration
// provable — but its descriptionLocation names the temp pre-normalised copy
// kiota actually read, a path that differs every run and exists on nobody
// else's machine, so it is rewritten to the durable revised document.
// Timestamp-valued fields go entirely, and generated file headers lose any
// dated comment lines.
func (kiotaBackend) Normalize(outDir, recordedSpecPath string) error {
	if err := scrubKiotaLock(filepath.Join(outDir, KiotaLockName), recordedSpecPath); err != nil {
		return err
	}
	if err := dropUnreferencedImports(outDir); err != nil {
		return err
	}
	return scrubDatedHeaders(outDir)
}

// dropUnreferencedImports deletes an aliased import a generated file never
// mentions. Go refuses to compile such a file, and kiota emits them: a model
// that inherits every one of its time-typed properties from an embedded
// parent still gets time imported, and the SDK then fails to type-check as a
// whole — which stops generation before a single provider file is written,
// however sound the rest of the tree is.
//
// The edit is deliberately textual and minimal. Only the offending import
// line goes; every other byte of the file is left exactly as the generator
// wrote it, because the alternative — running the file through a formatter —
// rewrites indentation across the whole SDK and buries a one-line repair in a
// diff nobody can read. Only aliased imports are considered, which is what
// kiota emits and what lets usage be decided by looking for the alias alone.
func dropUnreferencedImports(outDir string) error {
	return filepath.WalkDir(outDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		source, err := os.ReadFile(path) //nolint:gosec // a .go file under the staging dir this run wrote
		if err != nil {
			return err
		}
		trimmed, dropped := withoutUnreferencedImports(source)
		if !dropped {
			return nil
		}
		return os.WriteFile(path, trimmed, 0o644) //nolint:gosec // generated source, world-readable like the rest
	})
}

// withoutUnreferencedImports answers one file's source with every aliased
// import it never references removed, and whether anything was removed. A
// file that does not parse is returned untouched: repairing imports is not
// the place to discover a generator emitted something worse.
func withoutUnreferencedImports(source []byte) ([]byte, bool) {
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "generated.go", source, parser.SkipObjectResolution)
	if err != nil {
		// An unparseable file is not this pass's business; whatever is wrong
		// with it, the compiler will say so far more usefully.
		return source, false
	}

	referenced := map[string]bool{}
	ast.Inspect(parsed, func(n ast.Node) bool {
		selector, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := selector.X.(*ast.Ident); ok {
			referenced[ident.Name] = true
		}
		return true
	})

	unused := map[int]bool{}
	for _, spec := range parsed.Imports {
		if spec.Name == nil {
			continue
		}
		switch alias := spec.Name.Name; alias {
		case "_", ".", "":
			continue
		default:
			if !referenced[alias] {
				unused[fileSet.Position(spec.Pos()).Line] = true
			}
		}
	}
	if len(unused) == 0 {
		return source, false
	}

	lines := strings.Split(string(source), "\n")
	kept := make([]string, 0, len(lines))
	for i, line := range lines {
		if unused[i+1] {
			continue
		}
		kept = append(kept, line)
	}
	return []byte(strings.Join(kept, "\n")), true
}

// scrubKiotaLock rewrites the lock deterministically: descriptionLocation
// points at the durable document, timestamp values are dropped, and the JSON
// is re-encoded in sorted-key form so the bytes are a pure function of the
// remaining content.
func scrubKiotaLock(path, recordedSpecPath string) error {
	data, err := os.ReadFile(path) //nolint:gosec // the fixed name under the staging dir this run wrote
	if err != nil {
		return fmt.Errorf("reading %s (did kiota generate anything?): %w", path, err)
	}

	var lock map[string]any
	if err := json.Unmarshal(data, &lock); err != nil {
		return fmt.Errorf("%s is not usable JSON: %w", path, err)
	}

	lock["descriptionLocation"] = strings.ReplaceAll(recordedSpecPath, `\`, `/`)
	for key, value := range lock {
		if s, ok := value.(string); ok && timestampValue.MatchString(s) {
			delete(lock, key)
		}
	}

	out, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}
