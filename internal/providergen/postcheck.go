package providergen

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// postcheckStepTimeout bounds each toolchain command, so a hung module
// proxy cannot pin a generate run forever.
const postcheckStepTimeout = 5 * time.Minute

// postcheckSteps are the toolchain commands the installed tree is held to,
// in order. `go mod tidy` first, because the freshly rendered go.mod has no
// go.sum yet on a first run.
var postcheckSteps = [][]string{
	{"mod", "tidy"},
	{"build", "./..."},
	{"vet", "./..."},
}

// offlineSignatures are the toolchain messages that mean "no network", not
// "broken tree". A postcheck failing with one of these skips rather than
// fails: a developer offline should not read a red run as a broken
// generator, while CI, which is online, gets the real answer.
var offlineSignatures = []string{
	"module lookup disabled",
	"dial tcp",
	"no such host",
	"connection refused",
	"i/o timeout",
	"TLS handshake timeout",
	"proxy.golang.org",
	"sum.golang.org",
}

// PostcheckReport says what the toolchain gate did.
type PostcheckReport struct {
	// Ran is true when every step ran and passed.
	Ran bool
	// Steps lists the commands that passed, e.g. "go mod tidy".
	Steps []string
	// SkippedReason is why the gate did not run to completion without
	// that being a failure: postcheck disabled, no toolchain on PATH, or
	// an offline failure quoted verbatim.
	SkippedReason string
}

// postcheck holds the installed tree to `go mod tidy`, `go build ./...`
// and `go vet ./...`. A real failure is returned verbatim — the
// toolchain's own words, not a summary. A missing toolchain or an offline
// signature skips cleanly instead.
func postcheck(ctx context.Context, root string, enabled bool) (PostcheckReport, error) {
	if !enabled {
		return PostcheckReport{SkippedReason: "postcheck disabled"}, nil
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		return PostcheckReport{SkippedReason: "go is not on PATH"}, nil
	}

	rep := PostcheckReport{}
	for _, args := range postcheckSteps {
		name := "go " + strings.Join(args, " ")
		out, err := runBounded(ctx, goTool, root, args)
		if err == nil {
			rep.Steps = append(rep.Steps, name)
			continue
		}
		for _, signature := range offlineSignatures {
			if strings.Contains(out, signature) {
				rep.SkippedReason = fmt.Sprintf("%s needs the network and cannot reach it:\n%s", name, out)
				return rep, nil
			}
		}
		return rep, fmt.Errorf("postcheck: %s failed in %s:\n%s", name, root, out)
	}
	rep.Ran = true
	return rep, nil
}

// runBounded runs one toolchain command under the step timeout and returns
// its combined output.
func runBounded(ctx context.Context, goTool, dir string, args []string) (string, error) {
	stepCtx, cancel := context.WithTimeout(ctx, postcheckStepTimeout)
	defer cancel()

	cmd := exec.CommandContext(stepCtx, goTool, args...) //nolint:gosec // the resolved go tool with fixed arguments
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
