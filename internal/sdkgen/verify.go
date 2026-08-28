package sdkgen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/manifest"
)

// The drift kinds `sdk verify` reports. The first three come from
// byte-comparing the committed tree against a fresh regeneration; the last
// from comparing the committed files against the digests the manifest
// recorded. They answer different questions — changed/missing/extra say the
// tree no longer matches what the inputs produce, hand-edited says a human
// moved a derived file — so one file can carry two kinds at once.
const (
	// DriftChanged is a committed file whose bytes differ from regeneration.
	DriftChanged = "changed"
	// DriftMissing is a file regeneration produces that the tree lacks.
	DriftMissing = "missing"
	// DriftExtra is a file the tree carries that regeneration does not
	// produce.
	DriftExtra = "extra"
	// DriftHandEdited is a committed file that no longer matches the digest
	// `sdk generate` recorded for it in the manifest.
	DriftHandEdited = "hand-edited"
)

// Drift is one path the committed tree gets wrong.
type Drift struct {
	// Kind is one of the Drift* constants.
	Kind string
	// Path is relative to the repo root, forward slashes.
	Path string
}

// String renders one report line: "<kind>: <path>".
func (d Drift) String() string { return d.Kind + ": " + d.Path }

// Report is what one `sdk verify` run found.
type Report struct {
	// Backend and Version name the tool the regeneration ran.
	Backend, Version string
	// RevisedPath is the document the regeneration was taken from.
	RevisedPath string
	// Files is how many files the regeneration produced — the size of the
	// tree the committed one was held against.
	Files int
	// OutDir is the committed tree that was verified.
	OutDir string
	// Drifts lists every difference, sorted by path then kind. Empty means
	// the committed tree is byte-identical to regeneration and every file
	// matches its manifest digest.
	Drifts []Drift
}

// Clean reports whether the committed tree passed.
func (r Report) Clean() bool { return len(r.Drifts) == 0 }

// Verify is the SDK drift gate: regenerate with the exact pipeline `sdk
// generate` runs — gate the pinned tool, pre-normalise, generate, normalize —
// into a temporary tree, byte-compare it against the committed tree at
// opts.Out, and check the committed files against the digests the manifest
// recorded under the sdk origin. It writes nothing under the repo: the
// committed tree and the manifest are read only, and the temporary tree is
// gone before it returns.
//
// A non-empty Drifts is a finding, not an error: the run worked and the
// answer is no. An error means the question could not be answered at all — a
// failed gate, a missing revised document, an unreadable manifest.
func Verify(ctx context.Context, opts Options) (Report, error) {
	outAbs, err := resolveOut(opts)
	if err != nil {
		return Report{}, err
	}

	res, fresh, err := regenerate(ctx, opts, "", "tfpfgen-sdk-verify-")
	if err != nil {
		return Report{}, err
	}
	defer os.RemoveAll(fresh) //nolint:errcheck // best-effort temp cleanup

	want, err := treeDigests(fresh)
	if err != nil {
		return Report{}, err
	}
	have, err := treeDigests(outAbs)
	if err != nil {
		return Report{}, err
	}

	rep := Report{
		Backend:     res.Backend,
		Version:     res.Version,
		RevisedPath: res.RevisedPath,
		Files:       len(want),
		OutDir:      outAbs,
	}

	display := displayPath(opts.Root, outAbs)
	for rel, summary := range want {
		switch haveSum, there := have[rel]; {
		case !there:
			rep.Drifts = append(rep.Drifts, Drift{Kind: DriftMissing, Path: path.Join(display, rel)})
		case haveSum != summary:
			rep.Drifts = append(rep.Drifts, Drift{Kind: DriftChanged, Path: path.Join(display, rel)})
		}
	}
	for rel := range have {
		if _, there := want[rel]; !there {
			rep.Drifts = append(rep.Drifts, Drift{Kind: DriftExtra, Path: path.Join(display, rel)})
		}
	}

	handEdited, err := manifestMismatches(opts.Root)
	if err != nil {
		return Report{}, err
	}
	rep.Drifts = append(rep.Drifts, handEdited...)

	sort.Slice(rep.Drifts, func(i, j int) bool {
		if rep.Drifts[i].Path != rep.Drifts[j].Path {
			return rep.Drifts[i].Path < rep.Drifts[j].Path
		}
		return rep.Drifts[i].Kind < rep.Drifts[j].Kind
	})

	return rep, nil
}

// treeDigests walks a tree and digests every file, keyed by slash-separated
// path relative to dir. A tree that does not exist is empty rather than an
// error: on the committed side that is a tree never generated, and every
// regenerated file duly reports missing.
func treeDigests(dir string) (map[string]string, error) {
	out := map[string]string{}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return out, nil
	}

	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p) //nolint:gosec // walking the tree under comparison
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		summary := sha256.Sum256(data)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(summary[:])
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// displayPath renders the verified tree's path the way the manifest records
// paths: relative to the repo root, forward slashes. A tree outside the root
// is reported as given — absolute, but unambiguous.
func displayPath(root, outAbs string) string {
	if absRoot, err := filepath.Abs(root); err == nil {
		if rel, err := filepath.Rel(absRoot, outAbs); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(outAbs)
}

// manifestMismatches reports every sdk-origin file whose bytes on disk no
// longer match the digest `sdk generate` recorded — a hand edit to a derived
// file, distinct from the byte-compare's changed: changed says the inputs or
// the tool moved, hand-edited says a human moved the output.
//
// A missing manifest checks nothing — a tree never generated has no digests
// to hold it to. A recorded file already gone from disk is the byte-compare's
// missing, not reported twice.
func manifestMismatches(root string) ([]Drift, error) {
	m, ok, err := manifest.Load(root)
	if err != nil || !ok {
		return nil, err
	}

	var out []Drift
	for _, e := range m.Files {
		if e.Origin != manifest.OriginSDK || e.Authored {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.Path))) //nolint:gosec // paths the committed manifest records
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		summary := sha256.Sum256(data)
		if hex.EncodeToString(summary[:]) != e.SHA256 {
			out = append(out, Drift{Kind: DriftHandEdited, Path: e.Path})
		}
	}
	return out, nil
}
