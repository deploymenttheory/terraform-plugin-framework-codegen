package emit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnit_Emit_ModuleGoVersionsAreCompatible guards a failure that is invisible
// locally and fatal in CI.
//
// setup-go reads the *root* module's go directive and then pins GOTOOLCHAIN=local,
// so no newer toolchain can be fetched. If the pilot module requires a higher
// version than the root declares, anything that loads the pilot -- which includes
// internal/sdkbind's tests -- fails with:
//
//	go.mod requires go >= 1.25.8 (running go 1.25.0; GOTOOLCHAIN=local)
//
// A developer never sees it, because a local toolchain is usually newer than
// either. This has already happened once: `go get terraform-plugin-testing` raised
// the pilot's requirement and CI broke on the first run of the workflow.
func TestUnit_Emit_ModuleGoVersionsAreCompatible(t *testing.T) {
	t.Parallel()

	root := goDirective(t, filepath.Join("..", ".."))
	pilot := goDirective(t, filepath.Join("..", "..", "pilot", "thousandeyes"))

	if compareVersions(root, pilot) < 0 {
		t.Errorf("the root module declares go %s but the pilot requires go %s.\n"+
			"CI installs the toolchain from the root go.mod and pins GOTOOLCHAIN=local, so "+
			"anything loading the pilot will fail. Run: go mod edit -go=%s",
			root, pilot, pilot)
	}
}

func goDirective(t *testing.T, dir string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod in %s: %v", dir, err)
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "go "); ok {
			return strings.TrimSpace(v)
		}
	}

	t.Fatalf("no go directive in %s/go.mod", dir)
	return ""
}

// compareVersions compares dotted numeric versions, returning -1, 0 or 1.
func compareVersions(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")

	for i := range max(len(as), len(bs)) {
		x, y := atoiOrZero(at(as, i)), atoiOrZero(at(bs, i))
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}

	return 0
}

func at(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return "0"
}

func atoiOrZero(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
