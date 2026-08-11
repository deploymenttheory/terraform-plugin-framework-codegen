package store

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// retrieveTimeout bounds one download. Vendors publish documents in the tens
// of megabytes; anything slower than this is a network problem, not a big
// file. No retries — an import is a deliberate act an operator watches.
const retrieveTimeout = 3 * time.Minute

// Retrieve reads the document source names: downloaded when it is an
// http(s) URL, read from disk otherwise. Local paths exist for air-gapped
// imports and for tests; both routes hand back exactly the published bytes.
func Retrieve(source string) ([]byte, error) {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return download(source)
	}

	doc, err := os.ReadFile(source) //nolint:gosec // the operator-supplied source by design
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", source, err)
	}
	if len(doc) == 0 {
		return nil, fmt.Errorf("%s is empty", source)
	}
	return doc, nil
}

// download performs one GET, refusing anything but a clean, non-empty 200.
func download(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), retrieveTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building the request for %s: %w", url, err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", url, resp.Status)
	}

	doc, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading the response from %s: %w", url, err)
	}
	if len(doc) == 0 {
		return nil, fmt.Errorf("%s answered an empty document", url)
	}

	return doc, nil
}

// httpClient owns this package's connection pool. http.DefaultClient rides
// the process-wide default transport, whose idle connections
// httptest.Server.Close closes — under parallel tests a pooled connection
// can be torn out from under an in-flight request, surfacing as
// "transport connection broken". An owned pool is immune to neighbours.
var httpClient = newOwnedClient()

func newOwnedClient() *http.Client {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return &http.Client{Transport: t.Clone()}
	}
	return &http.Client{}
}
