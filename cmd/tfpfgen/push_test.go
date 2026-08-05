package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/manifest"
)

// needGit skips a test when git is not on PATH: push shells out to it by
// design, the same way postcheck shells out to terraform.
func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
}

// noTokens keeps the developer's real credentials out of the test runs.
func noTokens(t *testing.T) {
	t.Helper()
	t.Setenv(pushTokenEnv, "")
	t.Setenv(pushTokenFallbackEnv, "")
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// seedTarget builds a bare repository whose default branch holds the given
// files, and returns its file:// clone URL.
func seedTarget(t *testing.T, files map[string]string) string {
	t.Helper()

	bare := filepath.Join(t.TempDir(), "target.git")
	gitIn(t, t.TempDir(), "init", "--bare", bare)

	work := t.TempDir()
	gitIn(t, work, "clone", bare, filepath.Join(work, "clone"))
	clone := filepath.Join(work, "clone")

	for rel, content := range files {
		p := filepath.Join(clone, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, clone, "add", "--all")
	gitIn(t, clone, "commit", "-m", "seed")
	gitIn(t, clone, "push", "origin", "HEAD:refs/heads/main")

	return "file://" + bare
}

// generatedTree writes a provider root carrying a manifest, which is what
// makes it something push will agree to publish.
func generatedTree(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	entries := make([]manifest.Entry, 0, len(files))
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, manifest.Entry{Path: rel, SHA256: "x", Blueprint: "blueprints/test"})
	}
	if err := manifest.Save(root, manifest.New("test", entries)); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestUnit_CLI_Push_RequiredFlagsAndManifest(t *testing.T) {
	quiet(t)
	noTokens(t)

	if err := runProviderPush(nil); err == nil {
		t.Error("expected -out to be required")
	}
	if err := runProviderPush([]string{"-out", t.TempDir()}); err == nil {
		t.Error("expected -repo to be required")
	}

	// A tree nobody generated has no manifest, and push must refuse it rather
	// than publish content of unknown provenance.
	err := runProviderPush([]string{"-out", t.TempDir(), "-repo", "owner/name"})
	if err == nil || !strings.Contains(err.Error(), "provider generate first") {
		t.Errorf("expected the manifest refusal, got: %v", err)
	}
}

func TestUnit_CLI_Push_GitHubNeedsAToken(t *testing.T) {
	quiet(t)
	noTokens(t)

	out := generatedTree(t, map[string]string{"main.go": "package main\n"})

	err := runProviderPush([]string{"-out", out, "-repo", "owner/name"})
	if err == nil || !strings.Contains(err.Error(), pushTokenEnv) {
		t.Errorf("expected the token refusal to name %s, got: %v", pushTokenEnv, err)
	}
}

func TestUnit_CLI_Push_ParseRepo(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		in          string
		owner, repo string
		gitHub      bool
	}{
		"https":      {"https://github.com/org/prov.git", "org", "prov", true},
		"https bare": {"https://github.com/org/prov", "org", "prov", true},
		"ssh":        {"git@github.com:org/prov.git", "org", "prov", true},
		"shorthand":  {"org/prov", "org", "prov", true},
		"enterprise": {"https://github.example.com/org/prov", "org", "prov", true},
		"file":       {"file:///somewhere/target.git", "", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := parseRepo(tc.in)
			if err != nil {
				t.Fatalf("parseRepo(%q): %v", tc.in, err)
			}
			if got.owner != tc.owner || got.name != tc.repo || got.gitHub != tc.gitHub {
				t.Errorf("parseRepo(%q) = %+v", tc.in, got)
			}
		})
	}

	if _, err := parseRepo("not-a-repo"); err == nil {
		t.Error("expected a bare word to be refused")
	}
}

// TestUnit_CLI_Push_FirstPushOpensABranch is the main path: a target whose
// default branch holds an older generation plus files of its own, a source
// tree that renamed one generated file, and a push that must sync, prune the
// orphan, leave the target's own files alone, and land on the generator-owned
// branch -- without a pull request, because file:// is not a GitHub host.
func TestUnit_CLI_Push_FirstPushOpensABranch(t *testing.T) {
	quiet(t)
	needGit(t)
	noTokens(t)

	oldManifest, err := manifest.Marshal(manifest.New("old", []manifest.Entry{
		{Path: "internal/old_resource.go", SHA256: "x", Blueprint: "blueprints/test"},
	}))
	if err != nil {
		t.Fatal(err)
	}

	repo := seedTarget(t, map[string]string{
		".github/workflows/release.yml": "name: release\n",
		"internal/old_resource.go":      "package internal\n",
		manifest.Name:                   string(oldManifest),
	})

	out := generatedTree(t, map[string]string{
		"internal/new_resource.go": "package internal\n",
		"go.mod":                   "module example.com/prov\n",
	})

	if err := runProviderPush([]string{"-out", out, "-repo", repo}); err != nil {
		t.Fatalf("push: %v", err)
	}

	// Read the pushed branch back out of the bare repository.
	bare := strings.TrimPrefix(repo, "file://")
	branches := gitIn(t, bare, "branch", "--list")
	if !strings.Contains(branches, pushBranchPrefix) {
		t.Fatalf("no generator-owned branch was pushed; branches:\n%s", branches)
	}
	branch := ""
	for _, b := range strings.Fields(branches) {
		if strings.HasPrefix(b, pushBranchPrefix) {
			branch = b
		}
	}

	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", branch)
	for _, want := range []string{"internal/new_resource.go", "go.mod", ".github/workflows/release.yml", manifest.Name} {
		if !strings.Contains(files, want) {
			t.Errorf("the pushed branch is missing %s:\n%s", want, files)
		}
	}
	if strings.Contains(files, "internal/old_resource.go") {
		t.Error("the orphaned generated file was not pruned")
	}

	msg := gitIn(t, bare, "log", "-1", "--format=%B", branch)
	for _, want := range []string{"blueprints/test", "tfpfgen test", "generator-owned"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the commit message omits %q:\n%s", want, msg)
		}
	}
}

func TestUnit_CLI_Push_UpToDateIsANoOp(t *testing.T) {
	quiet(t)
	needGit(t)
	noTokens(t)

	content := map[string]string{"go.mod": "module example.com/prov\n"}
	out := generatedTree(t, content)

	// The target's default branch already holds exactly what the source tree
	// holds, manifest included.
	data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(manifest.Name)))
	if err != nil {
		t.Fatal(err)
	}
	repo := seedTarget(t, map[string]string{
		"go.mod":      content["go.mod"],
		manifest.Name: string(data),
	})

	if err := runProviderPush([]string{"-out", out, "-repo", repo}); err != nil {
		t.Fatalf("an up-to-date push must succeed quietly: %v", err)
	}

	bare := strings.TrimPrefix(repo, "file://")
	if branches := gitIn(t, bare, "branch", "--list"); strings.Contains(branches, pushBranchPrefix) {
		t.Errorf("an up-to-date push must not create a branch:\n%s", branches)
	}
}

func TestUnit_CLI_Push_DryRunPushesNothing(t *testing.T) {
	quiet(t)
	needGit(t)
	noTokens(t)

	repo := seedTarget(t, map[string]string{"README.md": "seed\n"})
	out := generatedTree(t, map[string]string{"go.mod": "module example.com/prov\n"})

	stdout := captureStdout(t, func() {
		if err := runProviderPush([]string{"-out", out, "-repo", repo, "-dry-run"}); err != nil {
			t.Errorf("dry run: %v", err)
		}
	})
	if !strings.Contains(stdout, "go.mod") {
		t.Errorf("the dry run should report what would change:\n%s", stdout)
	}

	bare := strings.TrimPrefix(repo, "file://")
	if branches := gitIn(t, bare, "branch", "--list"); strings.Contains(branches, pushBranchPrefix) {
		t.Errorf("a dry run must not push:\n%s", branches)
	}
}

// TestUnit_CLI_Push_PullRequest exercises the API half against a stub server:
// a 201 yields the new PR's URL, and a 422 -- the branch already has one --
// finds and reports the open PR instead of failing a push that succeeded.
func TestUnit_CLI_Push_PullRequest(t *testing.T) {
	t.Parallel()

	t.Run("created", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/repos/org/prov/pulls" {
				t.Errorf("unexpected call: %s %s", r.Method, r.URL)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer tok" {
				t.Errorf("Authorization = %q", got)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"html_url":"https://github.example/pr/1"}`))
		}))
		defer srv.Close()

		target := repoTarget{owner: "org", name: "prov", gitHub: true, apiBase: srv.URL}
		url, err := openPullRequest(target, "tok", "tfpfgen/generate-abc", "main", "title", "body")
		if err != nil {
			t.Fatalf("openPullRequest: %v", err)
		}
		if url != "https://github.example/pr/1" {
			t.Errorf("url = %q", url)
		}
	})

	t.Run("already open", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"message":"A pull request already exists"}]}`))
				return
			}
			_, _ = w.Write([]byte(`[{"html_url":"https://github.example/pr/7"}]`))
		}))
		defer srv.Close()

		target := repoTarget{owner: "org", name: "prov", gitHub: true, apiBase: srv.URL}
		url, err := openPullRequest(target, "tok", "tfpfgen/generate-abc", "main", "title", "body")
		if err != nil {
			t.Fatalf("openPullRequest: %v", err)
		}
		if !strings.Contains(url, "https://github.example/pr/7") {
			t.Errorf("url = %q", url)
		}
	})
}
