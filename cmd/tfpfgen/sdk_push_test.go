package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/kiota"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/manifest"
)

// generatedSDKTree writes an SDK root carrying a kiota lock and a go.mod,
// which is what makes it something sdk push will agree to publish.
func generatedSDKTree(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	files[kiota.LockFileName] = `{
  "descriptionHash": "ABCDEF0123456789ABCDEF",
  "descriptionLocation": "../openapi/test/api.yaml",
  "kiotaVersion": "1.34.1"
}
`
	if _, ok := files["go.mod"]; !ok {
		files["go.mod"] = "module example.com/sdk\n"
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestUnit_CLI_SDKPush_RefusalsNameTheirCause(t *testing.T) {
	quiet(t)
	noTokens(t)

	if err := runSDKPush(nil); err == nil {
		t.Error("expected -out to be required")
	}
	if err := runSDKPush([]string{"-out", t.TempDir()}); err == nil {
		t.Error("expected -repo to be required")
	}

	// A tree without a lock is a tree the pipeline has not produced.
	err := runSDKPush([]string{"-out", t.TempDir(), "-repo", "owner/name"})
	if err == nil || !strings.Contains(err.Error(), "sdk generate first") {
		t.Errorf("expected the lock refusal, got: %v", err)
	}

	// A lock without a go.mod is an embedded SDK, whose import path only works
	// inside the provider module.
	embedded := t.TempDir()
	if err := os.WriteFile(filepath.Join(embedded, kiota.LockFileName), []byte(`{"kiotaVersion":"1.34.1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err = runSDKPush([]string{"-out", embedded, "-repo", "owner/name"})
	if err == nil || !strings.Contains(err.Error(), "-mode external") {
		t.Errorf("expected the embed refusal to name -mode external, got: %v", err)
	}

	// GitHub without a token cannot push.
	out := generatedSDKTree(t, map[string]string{"client.go": "package sdk\n"})
	err = runSDKPush([]string{"-out", out, "-repo", "owner/name"})
	if err == nil || !strings.Contains(err.Error(), pushTokenEnv) {
		t.Errorf("expected the token refusal to name %s, got: %v", pushTokenEnv, err)
	}
}

// TestUnit_CLI_SDKPush_FirstPushOpensABranch is the main path: a target whose
// default branch holds an older generation (inventoried by the manifest the
// previous push wrote) plus files of its own, and a push that must sync, prune
// the orphan, leave the target's own files alone, and land on the
// generator-owned branch -- without a pull request, because file:// is not a
// GitHub host.
func TestUnit_CLI_SDKPush_FirstPushOpensABranch(t *testing.T) {
	quiet(t)
	needGit(t)
	noTokens(t)

	oldManifest, err := manifest.Marshal(manifest.New("old", []manifest.Entry{
		{Path: "models/old_model.go", SHA256: "x", Blueprint: "openapi document abc"},
	}))
	if err != nil {
		t.Fatal(err)
	}

	repo := seedTarget(t, map[string]string{
		".github/workflows/ci.yml": "name: ci\n",
		"LICENSE":                  "MIT\n",
		"models/old_model.go":      "package models\n",
		manifest.Name:              string(oldManifest),
	})

	out := generatedSDKTree(t, map[string]string{
		"models/new_model.go": "package models\n",
		"client.go":           "package sdk\n",
	})

	if err := runSDKPush([]string{"-out", out, "-repo", repo}); err != nil {
		t.Fatalf("sdk push: %v", err)
	}

	bare := strings.TrimPrefix(repo, "file://")
	branches := gitIn(t, bare, "branch", "--list")
	if !strings.Contains(branches, sdkPushBranchPrefix) {
		t.Fatalf("no generator-owned branch was pushed; branches:\n%s", branches)
	}
	branch := ""
	for _, b := range strings.Fields(branches) {
		if strings.HasPrefix(b, sdkPushBranchPrefix) {
			branch = b
		}
	}

	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", branch)
	for _, want := range []string{
		"models/new_model.go", "client.go", "go.mod", kiota.LockFileName,
		".github/workflows/ci.yml", "LICENSE", manifest.Name,
	} {
		if !strings.Contains(files, want) {
			t.Errorf("the pushed branch is missing %s:\n%s", want, files)
		}
	}
	if strings.Contains(files, "models/old_model.go") {
		t.Error("the orphaned generated file was not pruned")
	}

	msg := gitIn(t, bare, "log", "-1", "--format=%B", branch)
	for _, want := range []string{"kiota 1.34.1", "ABCDEF012345", "generator-owned"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the commit message omits %q:\n%s", want, msg)
		}
	}
}

// TestUnit_CLI_SDKPush_SecondIdenticalPushIsANoOp proves the manifest sdk push
// writes is deterministic: after the first push's branch becomes the default
// branch, pushing the same tree again must find nothing to do.
func TestUnit_CLI_SDKPush_SecondIdenticalPushIsANoOp(t *testing.T) {
	quiet(t)
	needGit(t)
	noTokens(t)

	repo := seedTarget(t, map[string]string{"README.md": "seed\n"})
	out := generatedSDKTree(t, map[string]string{"client.go": "package sdk\n"})

	if err := runSDKPush([]string{"-out", out, "-repo", repo}); err != nil {
		t.Fatalf("first push: %v", err)
	}

	bare := strings.TrimPrefix(repo, "file://")
	branches := gitIn(t, bare, "branch", "--list")
	branch := ""
	for _, b := range strings.Fields(branches) {
		if strings.HasPrefix(b, sdkPushBranchPrefix) {
			branch = b
		}
	}
	if branch == "" {
		t.Fatalf("no branch was pushed:\n%s", branches)
	}
	// Merge the generation into the default branch, as a human would.
	gitIn(t, bare, "update-ref", "refs/heads/main", branch)
	gitIn(t, bare, "branch", "-D", branch)

	if err := runSDKPush([]string{"-out", out, "-repo", repo}); err != nil {
		t.Fatalf("second push: %v", err)
	}
	if branches := gitIn(t, bare, "branch", "--list"); strings.Contains(branches, sdkPushBranchPrefix) {
		t.Errorf("an up-to-date push must not create a branch:\n%s", branches)
	}
}

func TestUnit_CLI_SDKPush_DryRunPushesNothing(t *testing.T) {
	quiet(t)
	needGit(t)
	noTokens(t)

	repo := seedTarget(t, map[string]string{"README.md": "seed\n"})
	out := generatedSDKTree(t, map[string]string{"client.go": "package sdk\n"})

	stdout := captureStdout(t, func() {
		if err := runSDKPush([]string{"-out", out, "-repo", repo, "-dry-run"}); err != nil {
			t.Errorf("dry run: %v", err)
		}
	})
	if !strings.Contains(stdout, "client.go") {
		t.Errorf("the dry run should report what would change:\n%s", stdout)
	}

	bare := strings.TrimPrefix(repo, "file://")
	if branches := gitIn(t, bare, "branch", "--list"); strings.Contains(branches, sdkPushBranchPrefix) {
		t.Errorf("a dry run must not push:\n%s", branches)
	}
}
