package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/openapi"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/snapshot"
)

const usageSpecs = "specs [-url URL] [-output-dir DIR] [-dry-run]"

// specsFetchTimeout bounds the whole fetch. A specification is a couple of megabytes;
// anything that takes longer than this is a network problem worth hearing about.
const specsFetchTimeout = 2 * time.Minute

// runSpecs fetches the upstream OpenAPI document and pins it as a new snapshot.
//
// The refresh loop is the point: with -url omitted, the source comes from the latest
// snapshot's own metadata, so `specs -output-dir openapi-specs/thousandeyes` re-fetches
// from wherever the last pin came from -- no one has to remember the URL, because the
// snapshot that would go stale is the thing that records it.
func runSpecs(args []string) error {
	fs, _ := newFlagSet("specs", usageSpecs)

	var (
		url       = fs.String("url", "", "the document to fetch; defaults to the latest snapshot's recorded source")
		outputDir = fs.String("output-dir", "", "snapshot root, e.g. openapi-specs/thousandeyes (required)")
		dryRun    = fs.Bool("dry-run", false, "fetch and report, but pin nothing")
	)

	if err := parse(fs, args); err != nil {
		return err
	}

	if *outputDir == "" {
		return usagef("-output-dir is required")
	}

	sourceURL := *url
	var previous snapshot.Snapshot
	havePrevious := false

	if latest, err := snapshot.Latest(*outputDir); err == nil {
		previous, havePrevious = latest, true
		if sourceURL == "" {
			meta, mErr := latest.LoadMetadata()
			if mErr != nil {
				return fmt.Errorf("reading the latest snapshot's metadata for its source: %w", mErr)
			}
			sourceURL = meta.SourceURL
		}
	} else if !errors.Is(err, snapshot.ErrNoSnapshot) {
		return err
	}

	if sourceURL == "" {
		return usagef("-url is required for the first snapshot; later refreshes reuse the recorded source")
	}

	doc, err := fetchSpec(sourceURL)
	if err != nil {
		return err
	}

	sum := sha256.Sum256(doc)
	digest := hex.EncodeToString(sum[:])

	// Unchanged upstream means nothing to pin. Said explicitly and exited zero: the
	// weekly refresh should be quiet when there is nothing to review, and a new
	// snapshot identical to the last would only churn the tree.
	if havePrevious {
		if prevSum, cErr := previous.Checksum(); cErr == nil && prevSum == digest {
			log.Printf("✅ upstream is unchanged from %s (sha256:%s…); nothing pinned",
				previous.Name, digest[:12])
			return nil
		}
	}

	// Parsed before anything is written: a document the ingester cannot read is not a
	// snapshot, it is a broken download wearing one's name.
	tmp, err := os.CreateTemp("", "tfpfgen-spec-*.yaml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(doc); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	parsed, err := openapi.Load(tmp.Name())
	if err != nil {
		return fmt.Errorf("the fetched document does not parse as OpenAPI: %w", err)
	}
	paths, operations := parsed.Stats()

	log.Printf("fetched %s: version %s, %d path(s), %d operation(s), sha256:%s…",
		sourceURL, parsed.Version, paths, operations, digest[:12])

	if *dryRun {
		log.Printf("dry run; nothing pinned")
		return nil
	}

	snap, err := snapshot.Pin(*outputDir, doc, snapshot.Metadata{
		Version:        parsed.Version,
		SHA256:         digest,
		SourceURL:      sourceURL,
		FetchedAt:      time.Now().UTC(),
		PathCount:      paths,
		OperationCount: operations,
	})
	if err != nil {
		return err
	}

	log.Printf("✅ pinned %s", filepath.Join(*outputDir, snap.Name))

	return nil
}

// fetchSpec downloads the document, refusing anything but a clean 200.
func fetchSpec(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), specsFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building the request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: %s", url, resp.Status)
	}

	doc, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	if len(doc) == 0 {
		return nil, fmt.Errorf("fetching %s: the response was empty", url)
	}

	return doc, nil
}
