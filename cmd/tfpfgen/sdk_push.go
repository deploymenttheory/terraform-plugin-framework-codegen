package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/kiota"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/manifest"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/version"
)

const usageSDKPush = "sdk push -out DIR -repo URL [-branch NAME] [-base NAME] [-dry-run]"

// sdkPushBranchPrefix namespaces the branches sdk push creates, beside
// provider push's. The suffix is the digest of the lock file, so the same
// generation always names the same branch.
const sdkPushBranchPrefix = "tfpfgen/sdk-"

func runSDKPush(args []string) error {
	fs, _ := newFlagSet("sdk push", usageSDKPush)

	var o pushOptions
	fs.StringVar(&o.out, "out", "", "SDK root to publish, as written by sdk generate -mode external (required)")
	fs.StringVar(&o.repo, "repo", "", "target repository: a clone URL or GitHub owner/name (required)")
	fs.StringVar(&o.branch, "branch", "",
		"branch to push; defaults to "+sdkPushBranchPrefix+"<digest> derived from "+kiota.LockFileName)
	fs.StringVar(&o.base, "base", "",
		"branch to diff and open the pull request against; defaults to the repository's default branch")
	fs.BoolVar(&o.dryRun, "dry-run", false, "clone and compare, but push nothing and open nothing")

	if err := parse(fs, args); err != nil {
		return err
	}

	if o.out == "" {
		return usagef("-out is required: it names the SDK root to publish")
	}
	if o.repo == "" {
		return usagef("-repo is required: it names the repository to publish into")
	}

	// The lock is the SDK's provenance record, the way the manifest is the
	// provider's: a tree without one is a tree this pipeline has not produced,
	// and publishing it would put content of unknown origin under a commit
	// message that claims otherwise.
	lock, hadLock, err := kiota.ReadLock(o.out)
	if err != nil {
		return err
	}
	if !hadLock {
		return usagef("%s has no %s; run sdk generate first -- push publishes generated output, not arbitrary trees",
			o.out, kiota.LockFileName)
	}

	// Only an external-mode tree can live in its own repository: an embedded
	// SDK's import path is the provider module plus a directory, and moving the
	// tree without that module context breaks every import in it.
	if _, err := os.Stat(filepath.Join(o.out, "go.mod")); err != nil {
		return usagef("%s has no go.mod, so it is an embedded SDK; only a tree from "+
			"sdk generate -mode external can be published to its own repository", o.out)
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
		return fmt.Errorf("sdk push needs git on PATH: %w", err)
	}

	work, err := os.MkdirTemp("", "tfpfgen-sdk-push-*")
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

	// The SDK root carries no manifest of its own -- it is byte-compared
	// against fresh kiota output, and a foreign file would fail that check --
	// so the inventory that makes pruning safe lives in the target repository
	// instead, written by each push and read by the next. Files the target
	// carries that no push produced -- its licence, its workflows -- are never
	// touched, exactly as provider push behaves.
	previous, hadManifest, err := manifest.Load(work)
	if err != nil {
		return err
	}

	produced, err := syncTree(o.out, work)
	if err != nil {
		return err
	}

	if err := writeSDKManifest(work, produced, lock); err != nil {
		return err
	}
	produced[manifest.Name] = true

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
		digest, err := lockDigest(o.out)
		if err != nil {
			return err
		}
		branch = sdkPushBranchPrefix + digest
	}

	if _, err := g.run("checkout", "-B", branch); err != nil {
		return err
	}
	if _, err := g.run("commit", "-m", sdkPushCommitMessage(lock, changed)); err != nil {
		return err
	}
	// Forced, and only ever onto the generator-owned branch namespace: the
	// content is a pure function of (kiota version, pinned document, patches),
	// so the newest generation is always the right thing for the branch to hold.
	if _, err := g.run("push", "--force", "origin", "HEAD:refs/heads/"+branch); err != nil {
		return fmt.Errorf("pushing %s: %w", branch, err)
	}

	log.Printf("pushed %d file change(s) to %s on %s", changed, target.cloneURL, branch)

	if !target.gitHub {
		log.Printf("note: %s is not a GitHub host, so no pull request was opened; merge %s where the repository lives",
			target.host, branch)
		return nil
	}

	prURL, err := openPullRequest(target, token, branch, base,
		"Regenerate SDK from the pinned OpenAPI document",
		fmt.Sprintf(
			"Generated by kiota %s via `tfpfgen %s` from the document with hash `%s` — %d file(s) changed.\n\n"+
				"This branch is generator-owned and force-pushed on regeneration. "+
				"Review the diff here; to change the content, refresh the snapshot or its patches and regenerate.",
			lock.KiotaVersion, version.Version, shortHash(lock.DescriptionHash), changed))
	if err != nil {
		// The push itself succeeded, and saying so matters more than the PR
		// call failing: the work is on the branch either way.
		return fmt.Errorf("the branch is pushed, but opening the pull request failed: %w", err)
	}

	log.Printf("✅ pull request: %s", prURL)
	return nil
}

// writeSDKManifest records what this push produced, into the target work tree.
// Deterministic by construction -- sorted entries, no timestamp -- so a push of
// an unchanged generation writes an unchanged manifest and the no-op detection
// stays honest.
func writeSDKManifest(work string, produced map[string]bool, lock kiota.Lock) error {
	paths := make([]string, 0, len(produced))
	for p := range produced {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	entries := make([]manifest.Entry, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(p))) //nolint:gosec // paths this run wrote
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		entries = append(entries, manifest.Entry{
			Path:      p,
			SHA256:    hex.EncodeToString(sum[:]),
			Blueprint: "openapi document " + shortHash(lock.DescriptionHash),
		})
	}

	return manifest.Save(work, manifest.New(version.Version, entries))
}

// lockDigest derives the branch suffix from the lock file bytes, the same
// shape as provider push's manifest digest: one generation, one branch.
func lockDigest(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, kiota.LockFileName)) //nolint:gosec // fixed name under the SDK root
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12], nil
}

// sdkPushCommitMessage states provenance the way provider push does: what
// produced the tree, from what, and how much moved.
func sdkPushCommitMessage(lock kiota.Lock, changed int) string {
	return fmt.Sprintf(
		"Regenerate SDK from the pinned OpenAPI document\n\n"+
			"Produced by kiota %s via tfpfgen %s from the document with hash %s; %d file(s) changed.\n"+
			"This branch is generator-owned: refresh the snapshot or its patches, not these files.",
		lock.KiotaVersion, version.Version, shortHash(lock.DescriptionHash), changed)
}

// shortHash abbreviates the lock's 128-hex-digit description hash to a
// reviewable prefix.
func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
