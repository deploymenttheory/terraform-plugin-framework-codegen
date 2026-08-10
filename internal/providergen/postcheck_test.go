package providergen

import (
	"context"
	"os"
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
