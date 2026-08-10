package providergen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/emit"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/manifest"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/sdkbind"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/sdkgen"
)

// Report is what one `provider verify` run found. The drift vocabulary is
// sdkgen's, reused deliberately: changed, missing and extra come from
// byte-comparing the committed tree against a fresh regeneration, and
// hand-edited from comparing committed files against the digests the
// manifest recorded — the same four answers `sdk verify` gives, spelled
// identically.
type Report struct {
	// RevisedPath is the document the regeneration was taken from.
	RevisedPath string
	// Files is how many files the regeneration produced — the size of the
	// set the committed tree was held against.
	Files int
	// Root is the committed tree that was verified.
	Root string
	// Removals is the regeneration's prune report, for context.
	Removals []sdkbind.Removal
	// Drifts lists every difference, sorted by path then kind. Empty means
	// the committed tree is byte-identical to regeneration and every file
	// matches its manifest digest.
	Drifts []sdkgen.Drift
}

// Clean reports whether the committed tree passed.
func (r Report) Clean() bool { return len(r.Drifts) == 0 }

// Verify is the provider drift gate: regenerate with the exact pipeline
// `provider generate` runs — derive, bind, prune, verify, render, splice —
// into a temporary tree, byte-compare it against the committed files at
// Root, and check the committed files against the digests the manifest
// recorded under the empty origin. It writes nothing under the repo.
//
// A non-empty Drifts is a finding, not an error: the run worked and the
// answer is no. An error means the question could not be answered at all.
func Verify(_ context.Context, opts Options) (Report, error) {
	g, err := generate(opts)
	if err != nil {
		return Report{}, err
	}

	fresh, err := os.MkdirTemp("", "tfpfgen-provider-verify-")
	if err != nil {
		return Report{}, err
	}
	defer os.RemoveAll(fresh) //nolint:errcheck // best-effort temp cleanup

	entries, err := emit.Write(fresh, g.files)
	if err != nil {
		return Report{}, err
	}

	rep := Report{
		RevisedPath: g.res.RevisedPath,
		Files:       len(entries),
		Root:        opts.Root,
		Removals:    g.res.Removals,
	}

	want := make(map[string]string, len(entries))
	for _, e := range entries {
		want[e.Path] = e.SHA256
	}
	for path, sum := range want {
		switch haveSum, there, err := fileDigest(opts.Root, path); {
		case err != nil:
			return Report{}, err
		case !there:
			rep.Drifts = append(rep.Drifts, sdkgen.Drift{Kind: sdkgen.DriftMissing, Path: path})
		case haveSum != sum:
			rep.Drifts = append(rep.Drifts, sdkgen.Drift{Kind: sdkgen.DriftChanged, Path: path})
		}
	}

	recorded, _, err := manifest.Load(opts.Root)
	if err != nil {
		return Report{}, err
	}
	for _, e := range recorded.Files {
		if e.Origin != "" || e.Authored {
			continue
		}
		haveSum, there, err := fileDigest(opts.Root, e.Path)
		if err != nil {
			return Report{}, err
		}
		if !there {
			// A recorded file already gone is the byte-compare's missing,
			// not reported twice.
			continue
		}
		if _, produced := want[e.Path]; !produced {
			rep.Drifts = append(rep.Drifts, sdkgen.Drift{Kind: sdkgen.DriftExtra, Path: e.Path})
		}
		if haveSum != e.SHA256 {
			rep.Drifts = append(rep.Drifts, sdkgen.Drift{Kind: sdkgen.DriftHandEdited, Path: e.Path})
		}
	}

	sort.Slice(rep.Drifts, func(i, j int) bool {
		if rep.Drifts[i].Path != rep.Drifts[j].Path {
			return rep.Drifts[i].Path < rep.Drifts[j].Path
		}
		return rep.Drifts[i].Kind < rep.Drifts[j].Kind
	})
	return rep, nil
}

// fileDigest hashes one committed file, reporting whether it exists.
func fileDigest(root, path string) (string, bool, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path))) //nolint:gosec // paths this toolkit produced or recorded
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), true, nil
}
