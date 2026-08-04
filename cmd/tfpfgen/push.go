package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/manifest"
)

const usageProviderPush = "provider push -out DIR -repo URL [-branch NAME] [-base NAME] [-dry-run]"

// The environment is the only place the token comes from, for the same reason
// as the probe credential: a flag puts it in shell history and in the process
// table. GITHUB_TOKEN is accepted as a fallback because that is what Actions
// injects, so CI needs no extra plumbing.
const (
	pushTokenEnv         = "TFPFGEN_GITHUB_TOKEN"
	pushTokenFallbackEnv = "GITHUB_TOKEN"
)

// pushBranchPrefix namespaces the branches this verb creates. Everything under
// it is generator-owned, which is what makes force-pushing one defensible: the
// branch's content is a pure function of the blueprints, so history on it holds
// nothing a human wrote.
const pushBranchPrefix = "tfpfgen/generate-"

// pushTimeout bounds each git invocation and the PR call. A provider tree is a
// few megabytes; anything slower is a network problem worth hearing about.
const pushTimeout = 2 * time.Minute

type pushOptions struct {
	out    string
	repo   string
	branch string
	base   string
	dryRun bool
}

func runProviderPush(args []string) error {
	fs, _ := newFlagSet("provider push", usageProviderPush)

	var o pushOptions
	fs.StringVar(&o.out, "out", "", "provider root to publish, as written by provider generate (required)")
	fs.StringVar(&o.repo, "repo", "", "target repository: a clone URL or GitHub owner/name (required)")
	fs.StringVar(&o.branch, "branch", "",
		"branch to push; defaults to "+pushBranchPrefix+"<digest> derived from the manifest")
	fs.StringVar(&o.base, "base", "",
		"branch to diff and open the pull request against; defaults to the repository's default branch")
	fs.BoolVar(&o.dryRun, "dry-run", false, "clone and compare, but push nothing and open nothing")

	if err := parse(fs, args); err != nil {
		return err
	}

	if o.out == "" {
		return usagef("-out is required: it names the provider root to publish")
	}
	if o.repo == "" {
		return usagef("-repo is required: it names the repository to publish into")
	}

	// A tree with no manifest is a tree this generator has not produced, and
	// publishing one would put content of unknown provenance under a commit
	// message that claims otherwise.
	m, ok, err := manifest.Load(o.out)
	if err != nil {
		return err
	}
	if !ok {
		return usagef("%s has no %s; run provider generate first -- push publishes generated output, not arbitrary trees",
			o.out, manifest.Name)
	}

	target, err := parseRepo(o.repo)
	if err != nil {
		return err
	}

	token := os.Getenv(pushTokenEnv)
	if token == "" {
		token = os.Getenv(pushTokenFallbackEnv)
	}
	if target.gitHub && token == "" && !o.dryRun {
		return usagef("%s (or %s) must be set to push to %s", pushTokenEnv, pushTokenFallbackEnv, target.host)
	}

	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("provider push needs git on PATH: %w", err)
	}

	work, err := os.MkdirTemp("", "tfpfgen-push-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()

	g := gitRunner{dir: work, host: target.host, token: token}

	cloneArgs := []string{"clone", "--depth", "1"}
	if o.base != "" {
		cloneArgs = append(cloneArgs, "--branch", o.base)
	}
	cloneArgs = append(cloneArgs, target.cloneURL, work)
	if _, err := g.in("").run(cloneArgs...); err != nil {
		return fmt.Errorf("cloning %s: %w", target.cloneURL, err)
	}

	base := o.base
	if base == "" {
		out, err := g.run("rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return fmt.Errorf("finding the default branch: %w", err)
		}
		base = strings.TrimSpace(out)
	}

	// The target's previous manifest is read before the copy overwrites it: it
	// is the only record of which files the generator owned there last time,
	// and pruning works off it. Files the target repository carries that the
	// generator never produced -- its own workflows, its licence -- are never
	// touched, which is the same ownership rule the drift check enforces.
	previous, hadManifest, err := manifest.Load(work)
	if err != nil {
		return err
	}

	produced, err := syncTree(o.out, work)
	if err != nil {
		return err
	}

	if hadManifest {
		orphans, err := previous.Orphans(work, produced)
		if err != nil {
			return err
		}
		for _, p := range orphans {
			if err := os.Remove(filepath.Join(work, p)); err != nil {
				return fmt.Errorf("pruning %s: %w", p, err)
			}
			log.Printf("pruned    %s (generated last push, no longer produced)", p)
		}
	}

	if _, err := g.run("add", "--all"); err != nil {
		return err
	}
	status, err := g.run("status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) == "" {
		log.Printf("✅ %s already matches %s; nothing to push", target.cloneURL, o.out)
		return nil
	}

	changed := strings.Count(strings.TrimSpace(status), "\n") + 1

	if o.dryRun {
		fmt.Fprint(os.Stdout, status)
		log.Printf("%d file(s) would change on %s; dry run, nothing was pushed", changed, base)
		return nil
	}

	branch := o.branch
	if branch == "" {
		digest, err := manifestDigest(o.out)
		if err != nil {
			return err
		}
		branch = pushBranchPrefix + digest
	}

	if _, err := g.run("checkout", "-B", branch); err != nil {
		return err
	}
	if _, err := g.run("commit", "-m", pushCommitMessage(m, changed)); err != nil {
		return err
	}
	// Forced, and only ever onto the generator-owned branch namespace: the
	// content is a pure function of the blueprints, so the newest generation is
	// always the right thing for the branch to hold.
	if _, err := g.run("push", "--force", "origin", "HEAD:refs/heads/"+branch); err != nil {
		return fmt.Errorf("pushing %s: %w", branch, err)
	}

	log.Printf("pushed %d file change(s) to %s on %s", changed, target.cloneURL, branch)

	if !target.gitHub {
		log.Printf("note: %s is not a GitHub host, so no pull request was opened; merge %s where the repository lives",
			target.host, branch)
		return nil
	}

	prURL, err := openPullRequest(target, token, branch, base, m, changed)
	if err != nil {
		// The push itself succeeded, and saying so matters more than the PR
		// call failing: the work is on the branch either way.
		return fmt.Errorf("the branch is pushed, but opening the pull request failed: %w", err)
	}

	log.Printf("✅ pull request: %s", prURL)
	return nil
}

// repoTarget is a parsed -repo value.
type repoTarget struct {
	host     string
	owner    string
	name     string
	cloneURL string
	// gitHub reports whether the host speaks the GitHub API, which is what
	// decides whether a pull request can be opened.
	gitHub  bool
	apiBase string
}

// parseRepo accepts a clone URL (https or ssh) or a bare GitHub owner/name.
func parseRepo(raw string) (repoTarget, error) {
	var t repoTarget

	switch {
	// git@host:owner/name(.git)
	case strings.HasPrefix(raw, "git@"):
		rest := strings.TrimPrefix(raw, "git@")
		host, path, ok := strings.Cut(rest, ":")
		if !ok {
			return t, usagef("-repo %q is not a usable ssh remote", raw)
		}
		t.host = host
		t.cloneURL = raw
		t.owner, t.name = splitOwnerName(path)

	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil || (u.Host == "" && u.Scheme != "file") {
			return t, usagef("-repo %q is not a usable URL", raw)
		}
		// A file:// remote has no host and no forge; it exists so the whole
		// flow short of the pull request can run against a local repository.
		t.host = u.Scheme
		t.cloneURL = raw
		if u.Host != "" {
			t.host = u.Host
			t.owner, t.name = splitOwnerName(u.Path)
		}

	default:
		// owner/name shorthand means github.com, matching the gh CLI.
		t.host = "github.com"
		t.owner, t.name = splitOwnerName(raw)
		if t.owner == "" || t.name == "" {
			return t, usagef("-repo %q is not owner/name or a clone URL", raw)
		}
		t.cloneURL = "https://github.com/" + t.owner + "/" + t.name + ".git"
	}

	if t.host == "github.com" {
		t.gitHub = true
		t.apiBase = "https://api.github.com"
	} else if strings.Contains(t.host, "github") {
		// GitHub Enterprise serves its REST API under /api/v3.
		t.gitHub = true
		t.apiBase = "https://" + t.host + "/api/v3"
	}
	if t.gitHub && (t.owner == "" || t.name == "") {
		return t, usagef("-repo %q does not name an owner and repository", raw)
	}

	return t, nil
}

func splitOwnerName(path string) (owner, name string) {
	path = strings.Trim(strings.TrimSuffix(path, ".git"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// gitRunner runs git with the credential in the environment rather than on the
// command line: GIT_CONFIG_* is invisible to the process table, argv is not.
type gitRunner struct {
	dir   string
	host  string
	token string
}

// in returns a runner working in a different directory; in("") means no
// directory, for the clone that creates it.
func (g gitRunner) in(dir string) gitRunner {
	g.dir = dir
	return g
}

func (g gitRunner) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.dir

	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=tfpfgen",
		"GIT_AUTHOR_EMAIL=tfpfgen@users.noreply.github.com",
		"GIT_COMMITTER_NAME=tfpfgen",
		"GIT_COMMITTER_EMAIL=tfpfgen@users.noreply.github.com",
	)
	if g.token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + g.token))
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.https://"+g.host+"/.extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
		)
	}
	cmd.Env = env

	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v\n%s", args[0], err, errb.String())
	}
	return out.String(), nil
}

// syncTree copies every file under src into dst, and returns the set of
// relative slash paths it produced -- the shape Orphans wants.
func syncTree(src, dst string) (map[string]bool, error) {
	produced := map[string]bool{}

	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// The provider root should carry no .git of its own, but publishing one
		// into another repository would be corrupting, so it is refused by skip
		// rather than trusted to be absent.
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o750)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file; a generated tree holds nothing else", path)
		}

		data, err := os.ReadFile(path) //nolint:gosec // operator-supplied tree by design
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, rel), data, info.Mode().Perm()); err != nil {
			return err
		}
		produced[filepath.ToSlash(rel)] = true
		return nil
	})

	return produced, err
}

// manifestDigest derives the branch suffix from the manifest bytes. The same
// generated content always names the same branch, so re-running push after an
// interruption updates the branch instead of scattering siblings.
func manifestDigest(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(manifest.Name))) //nolint:gosec // fixed relative path
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12], nil
}

// pushCommitMessage states provenance: what produced the tree, from what, and
// how much moved. The blueprint set is taken from the manifest so the message
// cannot claim a source the inventory does not.
func pushCommitMessage(m manifest.Manifest, changed int) string {
	return fmt.Sprintf(
		"Regenerate provider from %s\n\nProduced by tfpfgen %s; %d file(s) changed.\nThis branch is generator-owned: edit the blueprints, not these files.",
		blueprintSources(m), m.ToolVersion, changed)
}

// blueprintSources names the distinct blueprint roots the manifest records.
func blueprintSources(m manifest.Manifest) string {
	seen := map[string]bool{}
	var sources []string
	for _, e := range m.Files {
		if e.Blueprint == "" || e.Blueprint == "orphaned" || seen[e.Blueprint] {
			continue
		}
		seen[e.Blueprint] = true
		sources = append(sources, e.Blueprint)
	}
	if len(sources) == 0 {
		return "its blueprints"
	}
	return strings.Join(sources, ", ")
}

// openPullRequest opens the PR, or finds the one already open for the branch.
// A second push to the same branch must not fail over a PR that already says
// exactly what this one would.
func openPullRequest(t repoTarget, token, branch, base string, m manifest.Manifest, changed int) (string, error) {
	title := "Regenerate provider from blueprints"
	body := fmt.Sprintf(
		"Generated by `tfpfgen %s` from %s — %d file(s) changed.\n\n"+
			"This branch is generator-owned and force-pushed on regeneration. "+
			"Review the diff here; to change the content, edit the blueprints and regenerate.",
		m.ToolVersion, blueprintSources(m), changed)

	payload, err := json.Marshal(map[string]string{
		"title": title,
		"body":  body,
		"head":  branch,
		"base":  base,
	})
	if err != nil {
		return "", err
	}

	status, resp, err := gitHubAPI(t, token, http.MethodPost, "/repos/"+t.owner+"/"+t.name+"/pulls", payload)
	if err != nil {
		return "", err
	}

	switch status {
	case http.StatusCreated:
		var pr struct {
			HTMLURL string `json:"html_url"`
		}
		if err := json.Unmarshal(resp, &pr); err != nil {
			return "", err
		}
		return pr.HTMLURL, nil

	case http.StatusUnprocessableEntity:
		// Usually "a pull request already exists" -- find it and report it,
		// because the branch it points at was just updated by this run.
		status, resp, err := gitHubAPI(t, token, http.MethodGet,
			"/repos/"+t.owner+"/"+t.name+"/pulls?state=open&head="+url.QueryEscape(t.owner+":"+branch), nil)
		if err == nil && status == http.StatusOK {
			var prs []struct {
				HTMLURL string `json:"html_url"`
			}
			if json.Unmarshal(resp, &prs) == nil && len(prs) > 0 {
				return prs[0].HTMLURL + " (already open; the branch behind it was updated)", nil
			}
		}
		return "", fmt.Errorf("GitHub refused the pull request (422): %s", firstAPIError(resp))

	default:
		return "", fmt.Errorf("GitHub answered %d to the pull request: %s", status, firstAPIError(resp))
	}
}

// gitHubAPI issues one authenticated REST call.
func gitHubAPI(t repoTarget, token, method, path string, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, t.apiBase+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: pushTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, data, nil
}

// firstAPIError pulls the human sentence out of a GitHub error body.
func firstAPIError(body []byte) string {
	var e struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &e) != nil || e.Message == "" {
		return strings.TrimSpace(string(body))
	}
	if len(e.Errors) > 0 && e.Errors[0].Message != "" {
		return e.Message + ": " + e.Errors[0].Message
	}
	return e.Message
}
