package sdkgen

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/emit"
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

func (kiotaBackend) RequiredVersion(cfg *config.Config) string { return cfg.SDK.BackendVersion }

func (b kiotaBackend) CheckTool(ctx context.Context, cfg *config.Config) error {
	if _, err := exec.LookPath("kiota"); err != nil {
		return fmt.Errorf("kiota is not on PATH; install the pinned %s (e.g. `brew install kiota`, or the "+
			"release archive from github.com/microsoft/kiota/releases) — the toolkit never downloads tools: %w",
			b.RequiredVersion(cfg), err)
	}
	have, err := toolVersion(ctx, "kiota", kiotaEnv, "--version")
	if err != nil {
		return err
	}
	return gate("kiota", have, b.RequiredVersion(cfg))
}

func (kiotaBackend) Generate(ctx context.Context, revisedSpecPath string, cfg *config.Config, outDir string) error {
	// The root namespace must be the import path the provider module gives
	// the SDK tree, or kiota's generated cross-package imports ("<ns>/things",
	// "<ns>/models") resolve nowhere. emit owns that spelling — module path
	// plus internal/sdk — and deriving it here keeps one definition site.
	// Without the flag kiota defaults to "ApiSdk", which type-checks in no
	// provider repo.
	pc, err := emit.FromConfig(cfg, "")
	if err != nil {
		return err
	}

	args := []string{
		"generate",
		"--language", "go",
		"--openapi", revisedSpecPath,
		"--output", outDir,
		"--class-name", cfg.SDK.ClientTypeName,
		"--namespace-name", pc.SDKImport,
		// The deprecated string indexers double the surface for nothing a
		// generated provider would ever call.
		"--exclude-backward-compatible",
		// outDir is always a fresh staging directory, but --clean-output makes
		// that a property of the invocation rather than of the caller.
		"--clean-output",
	}
	for _, g := range cfg.SDK.IncludePaths {
		args = append(args, "--include-path", g)
	}
	for _, g := range cfg.SDK.ExcludePaths {
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
	return nil
}

// Normalize scrubs kiota-lock.json. The lock's kiotaVersion and
// descriptionHash stay — they are the evidence that makes a regeneration
// provable — but its descriptionLocation names the temp pre-normalized copy
// kiota actually read, a path that differs every run and exists on nobody
// else's machine, so it is rewritten to the durable revised document.
// Timestamp-valued fields go entirely, and generated file headers lose any
// dated comment lines.
func (kiotaBackend) Normalize(outDir, recordedSpecPath string) error {
	if err := scrubKiotaLock(filepath.Join(outDir, KiotaLockName), recordedSpecPath); err != nil {
		return err
	}
	return scrubDatedHeaders(outDir)
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
