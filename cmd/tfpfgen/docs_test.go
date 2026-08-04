package main

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestUnit_CLI_DocsMatchTheBinary is the drift check docs/cli.md promises.
//
// The page is hand-maintained, and the failure mode of hand-maintained
// references is silent: the binary moves and the page keeps describing the old
// surface. This test holds the page to the binary in both directions it can
// check mechanically -- every command, verb, usage sketch and registered flag
// must appear in the page, and none of the retired spellings may.
func TestUnit_CLI_DocsMatchTheBinary(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join(repoRoot, "docs", "cli.md"))
	if err != nil {
		t.Fatalf("reading docs/cli.md: %v", err)
	}
	page := string(doc)

	groups := map[string][]command{
		"openapi":   openapiVerbs,
		"sdk":       sdkVerbs,
		"blueprint": blueprintVerbs,
		"probe":     probeVerbs,
		"provider":  providerVerbs,
		"bindings":  bindingsVerbs,
		"spec":      specVerbs,
	}

	for _, c := range commands {
		if !strings.Contains(page, c.name) {
			t.Errorf("docs/cli.md does not mention the %q command", c.name)
		}
	}

	flagLine := regexp.MustCompile(`(?m)^\s+-([a-zA-Z][a-zA-Z-]*)`)

	for group, verbs := range groups {
		for _, v := range verbs {
			if !strings.Contains(page, group+" "+v.name) {
				t.Errorf("docs/cli.md does not mention %q", group+" "+v.name)
			}
			if v.usage != "" && !strings.Contains(page, v.usage) {
				t.Errorf("docs/cli.md does not carry the usage line %q verbatim", v.usage)
			}

			// The verb's own -h output is the binary's statement of its flags;
			// every flag it prints must be named in the page.
			help := captureStdout(t, func() {
				_ = run([]string{group, v.name, "-h"}, io.Discard)
			})
			for _, m := range flagLine.FindAllStringSubmatch(help, -1) {
				name := "-" + m[1]
				if !strings.Contains(page, name) {
					t.Errorf("docs/cli.md does not mention %s %s's flag %s", group, v.name, name)
				}
			}
		}
	}

	// Retired spellings must not resurface. Their presence means an edit
	// described the old surface, which is exactly the drift this test exists
	// to catch.
	// "probe -mode" rather than "-mode": the retired spelling was probe's mode
	// flag; sdk generate legitimately carries a -mode of its own.
	for _, banned := range []string{
		"tfpluginframeworkgen", "probe -mode", "-output-dir", "-github-summary",
		"-accept-conflicts", "-facts-out", "-facts-check", "-no-rehearse",
		"probe.plan", "probe-evidence", "interop-specs", "openapi-specs",
		"--allow-mutations",
	} {
		if strings.Contains(page, banned) {
			t.Errorf("docs/cli.md still contains the retired spelling %q", banned)
		}
	}
}
