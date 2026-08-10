package corpus

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// fetchTimeout bounds one download. The largest pinned document is roughly
// 13 MB, so anything slower than this is a network problem rather than a big
// file.
const fetchTimeout = 3 * time.Minute

// fetch downloads a pinned document, preferring the mirror.
//
// Mirror first because several upstreams serve "current" rather than a
// version, so the pinned bytes stop being available the moment the vendor
// publishes again. Preferring the mirror cannot weaken anything: the caller
// checks the same SHA-256 whichever source answered, so a mirror that served
// the wrong bytes fails exactly as loudly as an upstream that did.
func fetch(pin Pin) (doc []byte, source string, err error) {
	var attempts []string

	if pin.MirrorURL != "" {
		attempts = append(attempts, pin.MirrorURL)
	}
	if pin.UpstreamURL != "" {
		attempts = append(attempts, pin.UpstreamURL)
	}
	if len(attempts) == 0 {
		return nil, "", fmt.Errorf("the pin names neither a mirror nor an upstream URL")
	}

	var failures []string

	for _, url := range attempts {
		body, gErr := get(url)
		if gErr == nil {
			return body, url, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", url, gErr))
	}

	return nil, "", fmt.Errorf("every source failed:\n  %s", strings.Join(failures, "\n  "))
}

// get performs one download, refusing anything but a clean, non-empty 200.
func get(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building the request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("the response was empty")
	}

	return body, nil
}
