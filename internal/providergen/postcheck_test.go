package providergen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGo puts an executable named go on an otherwise empty PATH, so the
// postcheck's toolchain is this script and nothing else.
func fakeGo(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "go")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil { //nolint:gosec // an executable test stub
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestUnit_Postcheck_DisabledSkips(t *testing.T) {
	rep, err := postcheck(context.Background(), t.TempDir(), false)
	if err != nil {
		t.Fatalf("postcheck: %v", err)
	}
	if rep.Ran || rep.SkippedReason != "postcheck disabled" {
		t.Errorf("report = %+v; disabling must skip with the reason", rep)
	}
}

func TestUnit_Postcheck_NoToolchainSkips(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	rep, err := postcheck(context.Background(), t.TempDir(), true)
	if err != nil {
		t.Fatalf("postcheck: %v", err)
	}
	if rep.Ran || rep.SkippedReason != "go is not on PATH" {
		t.Errorf("report = %+v; a missing toolchain must skip cleanly", rep)
	}
}

func TestUnit_Postcheck_AllStepsPass(t *testing.T) {
	fakeGo(t, "exit 0\n")
	rep, err := postcheck(context.Background(), t.TempDir(), true)
	if err != nil {
		t.Fatalf("postcheck: %v", err)
	}
	if !rep.Ran || len(rep.Steps) != 3 {
		t.Errorf("report = %+v; three passing steps make a run", rep)
	}
	if rep.Steps[0] != "go mod tidy" || rep.Steps[1] != "go build ./..." || rep.Steps[2] != "go vet ./..." {
		t.Errorf("steps = %v; the order is tidy, build, vet", rep.Steps)
	}
}

func TestUnit_Postcheck_FailureIsReportedVerbatim(t *testing.T) {
	fakeGo(t, "echo 'the hull is breached'; exit 1\n")
	_, err := postcheck(context.Background(), t.TempDir(), true)
	if err == nil || !strings.Contains(err.Error(), "the hull is breached") ||
		!strings.Contains(err.Error(), "go mod tidy") {
		t.Fatalf("err = %v; a failure carries the toolchain's own words and the step", err)
	}
}

func TestUnit_Postcheck_OfflineFailureSkips(t *testing.T) {
	fakeGo(t, "echo 'dial tcp 203.0.113.1:443: connect: network is unreachable'; exit 1\n")
	rep, err := postcheck(context.Background(), t.TempDir(), true)
	if err != nil {
		t.Fatalf("postcheck: %v", err)
	}
	if rep.Ran || !strings.Contains(rep.SkippedReason, "dial tcp") {
		t.Errorf("report = %+v; an offline signature skips with the output quoted", rep)
	}
}

func TestUnit_EnvWithBuildVCSOff(t *testing.T) {
	const flag = "-buildvcs=false"
	get := func(env []string) string {
		for _, e := range env {
			if v, ok := strings.CutPrefix(e, "GOFLAGS="); ok {
				return v
			}
		}
		return "<unset>"
	}

	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"no GOFLAGS gains the flag", []string{"PATH=/usr/bin"}, flag},
		{"empty GOFLAGS gains the flag", []string{"GOFLAGS="}, flag},
		{"an existing GOFLAGS keeps its flags", []string{"GOFLAGS=-mod=vendor"}, "-mod=vendor " + flag},
		{"an existing buildvcs is left alone", []string{"GOFLAGS=-buildvcs=true"}, "-buildvcs=true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := get(envWithBuildVCSOff(tc.in)); got != tc.want {
				t.Errorf("GOFLAGS = %q, want %q", got, tc.want)
			}
		})
	}

	// Exactly one GOFLAGS entry survives, never a duplicate the shell would
	// have to disambiguate.
	env := envWithBuildVCSOff([]string{"GOFLAGS=-mod=mod", "PATH=/bin"})
	count := 0
	for _, e := range env {
		if strings.HasPrefix(e, "GOFLAGS=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("GOFLAGS appears %d times, want exactly one", count)
	}
}

// TestUnit_Postcheck_PassesBuildVCSOffToToolchain proves the mechanism: a
// stub go on PATH refuses unless it sees -buildvcs=false in GOFLAGS, so the
// run only completes because the postcheck sets it for every step.
func TestUnit_Postcheck_PassesBuildVCSOffToToolchain(t *testing.T) {
	fakeGo(t, `case "$GOFLAGS" in
*-buildvcs=false*) exit 0 ;;
*) echo "GOFLAGS=$GOFLAGS lacks -buildvcs=false"; exit 1 ;;
esac
`)
	rep, err := postcheck(context.Background(), t.TempDir(), true)
	if err != nil {
		t.Fatalf("postcheck: %v", err)
	}
	if !rep.Ran {
		t.Errorf("report = %+v; the build steps must carry -buildvcs=false in GOFLAGS", rep)
	}
}

// TestUnit_Postcheck_SucceedsInNonGitOutputDir reproduces the reported
// failure with the real toolchain: a freshly generated output tree that is
// not itself a git repository but sits under a broken git ancestor makes
// `go build` fail with "error obtaining VCS status: exit status 128 / Use
// -buildvcs=false". The postcheck must build it anyway.
func TestUnit_Postcheck_SucceedsInNonGitOutputDir(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the real toolchain run in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}

	parent := t.TempDir()
	// A broken git ancestor: a .git directory that is not a valid repo, so
	// `git status` errors with exit 128 and go reports it as a VCS failure —
	// the same shape a real repo with dubious ownership produces in CI.
	if err := os.MkdirAll(filepath.Join(parent, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "output") // the output tree: itself no .git
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	writeStub := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeStub("go.mod", "module example.test/nongit\n\ngo 1.23\n")
	writeStub("main.go", "package main\n\nfunc main() {}\n")

	// Prove the bug is present without the flag: a plain build in this tree
	// fails with the VCS-status error the fix silences.
	probe := exec.Command("go", "build", "./...") //nolint:gosec // fixed args
	probe.Dir = root
	probe.Env = append(os.Environ(), "GOFLAGS=")
	if out, err := probe.CombinedOutput(); err == nil || !strings.Contains(string(out), "buildvcs") {
		t.Skipf("this environment does not stamp VCS here, nothing to guard: err=%v out=%s", err, out)
	}

	rep, err := postcheck(context.Background(), root, true)
	if err != nil {
		t.Fatalf("postcheck failed in a non-git output dir: %v", err)
	}
	if !rep.Ran {
		t.Fatalf("postcheck did not run to completion: %+v", rep)
	}
}
