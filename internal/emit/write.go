package emit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/manifest"
)

// Write lands rendered files under root and returns their manifest
// entries. Entries carry no origin — the empty origin is `provider
// generate`'s, by the manifest package's convention — and it is the
// caller's job to fold them into the repo manifest alongside the entries
// other verbs recorded.
func Write(root string, files []File) ([]manifest.Entry, error) {
	entries := make([]manifest.Entry, 0, len(files))

	for _, f := range files {
		target := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return nil, fmt.Errorf("creating the directory for %s: %w", f.Path, err)
		}
		if err := os.WriteFile(target, f.Content, 0o600); err != nil {
			return nil, fmt.Errorf("writing %s: %w", f.Path, err)
		}

		sum := sha256.Sum256(f.Content)
		entries = append(entries, manifest.Entry{
			Path:   f.Path,
			SHA256: hex.EncodeToString(sum[:]),
			Source: f.Source,
		})
	}

	return entries, nil
}
