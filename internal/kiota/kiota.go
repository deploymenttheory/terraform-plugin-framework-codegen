// Package kiota drives the Microsoft Kiota generator as a pinned, gated tool.
//
// The kiota binary is a PATH tool, like git and terraform: this package never
// downloads it. What protects determinism is not owning the download but
// refusing to run the wrong binary -- kiota's output is a pure function of
// (kiota version, description bytes, parameters), and a version bump rewrites
// the whole generated tree. The committed kiota-lock.json is the single source
// of truth for which version produced a tree; Gate compares it against the
// binary on PATH and refuses on mismatch, naming both.
//
// Generation itself is deterministic but not gofmt-formatted, so Generate
// always finishes with the same gofumpt pass the provider generator applies to
// its own output -- one canonical form, whichever tool wrote the file.
package kiota

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/tools/imports"
	"mvdan.cc/gofumpt/format"
)

// LockFileName is kiota's own record of a generation, written into the output
// directory. Committed deliberately: it is what makes a regeneration provable.
const LockFileName = "kiota-lock.json"

// Lock is the subset of kiota-lock.json this tool reads.
type Lock struct {
	DescriptionHash     string `json:"descriptionHash"`
	KiotaVersion        string `json:"kiotaVersion"`
	ClientClassName     string `json:"clientClassName"`
	ClientNamespaceName string `json:"clientNamespaceName"`
}

// ReadLock reads the lock file in dir, reporting whether one exists.
func ReadLock(dir string) (Lock, bool, error) {
	var l Lock

	data, err := os.ReadFile(filepath.Join(dir, LockFileName)) //nolint:gosec // fixed name under an operator-supplied dir
	if os.IsNotExist(err) {
		return l, false, nil
	}
	if err != nil {
		return l, false, err
	}

	if err := json.Unmarshal(data, &l); err != nil {
		return l, false, fmt.Errorf("%s is not a usable kiota lock file: %w", filepath.Join(dir, LockFileName), err)
	}
	return l, true, nil
}

// semver matches the leading version in `kiota --version` output, which
// carries build metadata after a plus (e.g. "1.34.1+heads/main...").
var semver = regexp.MustCompile(`\d+\.\d+\.\d+`)

// BinaryVersion reports the version of the kiota binary on PATH.
func BinaryVersion() (string, error) {
	if _, err := exec.LookPath("kiota"); err != nil {
		return "", fmt.Errorf("kiota is not on PATH; install it (e.g. `brew install kiota`, or the "+
			"release zip from github.com/microsoft/kiota/releases): %w", err)
	}

	out, err := run("", "--version")
	if err != nil {
		return "", err
	}

	v := semver.FindString(out)
	if v == "" {
		return "", fmt.Errorf("could not read a version from `kiota --version` output: %q", strings.TrimSpace(out))
	}
	return v, nil
}

// Gate refuses a generation whose binary does not match the lock the committed
// tree was generated with. A silent version drift would rewrite the whole tree
// and present as an inexplicable diff; the refusal names both versions.
func Gate(lock Lock) error {
	have, err := BinaryVersion()
	if err != nil {
		return err
	}

	if lock.KiotaVersion != "" && have != lock.KiotaVersion {
		return fmt.Errorf("the kiota on PATH is %s but %s was generated with %s; "+
			"install the matching version, or regenerate deliberately and commit the full diff",
			have, LockFileName, lock.KiotaVersion)
	}
	return nil
}

// GenerateOptions is one invocation of kiota generate.
type GenerateOptions struct {
	// Description is the path to the OpenAPI document, normally a pinned
	// snapshot's SpecPath. Never a URL: reproducibility starts with the pin.
	Description string
	// Out is the directory the generated tree is written into.
	Out string
	// Module is the Go import path of Out (kiota's -n). Changing it rewrites
	// every generated file, because import aliases hash the package paths.
	Module string
	// ClientName is the root client type name (kiota's -c).
	ClientName string
	// Include and Exclude are kiota path globs, e.g. "/tags/**".
	Include []string
	Exclude []string
	// Clean passes --clean-output, removing files a previous run produced.
	Clean bool
}

// Generate runs kiota and formats its output.
func Generate(opts GenerateOptions) error {
	args := []string{
		"generate",
		"--language", "go",
		"--openapi", opts.Description,
		"--output", opts.Out,
		"--namespace-name", opts.Module,
		"--class-name", opts.ClientName,
		// The deprecated string indexers double the surface for nothing this
		// generator would ever call.
		"--exclude-backward-compatible",
	}
	for _, g := range opts.Include {
		args = append(args, "--include-path", g)
	}
	for _, g := range opts.Exclude {
		args = append(args, "--exclude-path", g)
	}
	if opts.Clean {
		args = append(args, "--clean-output")
	}

	if _, err := run("", args...); err != nil {
		return err
	}

	// kiota drops a timestamped log file into the output; a timestamp is the
	// one thing a reproducible tree must not carry.
	if err := os.Remove(filepath.Join(opts.Out, ".kiota.log")); err != nil && !os.IsNotExist(err) {
		return err
	}

	return formatTree(opts.Out)
}

// run executes kiota with the environment that keeps it deterministic and
// quiet: no update check, no telemetry, no colour codes in captured output.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("kiota", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"KIOTA_OFFLINE_ENABLED=true",
		"KIOTA_CLI_TELEMETRY_OPTOUT=true",
		"KIOTA_TUTORIAL_ENABLED=false",
		"KIOTA_CONSOLE_COLORS_ENABLED=false",
	)

	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("kiota %s: %v\n%s%s", args[0], err, out.String(), errb.String())
	}
	return out.String(), nil
}

// formatTree runs goimports then gofumpt over every generated Go file.
//
// Kiota's output is deterministic but not gofmt-shaped, and it occasionally
// emits a file whose imports it does not use -- a generator bug that would
// otherwise fail the build for the whole tree. goimports repairs the import
// block deterministically; gofumpt then applies the same canonical form the
// provider generator writes, so the repository formatter never thrashes.
func formatTree(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}

		src, err := os.ReadFile(path) //nolint:gosec // walking the tree this run wrote
		if err != nil {
			return err
		}
		repaired, err := imports.Process(path, src, nil)
		if err != nil {
			return fmt.Errorf("repairing imports in %s: %w", path, err)
		}
		formatted, err := format.Source(repaired, format.Options{})
		if err != nil {
			return fmt.Errorf("formatting %s: %w", path, err)
		}
		if bytes.Equal(src, formatted) {
			return nil
		}
		return os.WriteFile(path, formatted, info.Mode().Perm())
	})
}
