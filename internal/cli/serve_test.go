package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/quirkserver"
)

// syncWriter is a threadsafe buffer: the serving goroutine writes while the
// test polls for the announced base URL.
type syncWriter struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

var servingAt = regexp.MustCompile(`quirkserver serving at (http://\S+)`)

// awaitBaseURL polls the announcement line for the server's address.
func awaitBaseURL(t *testing.T, out *syncWriter) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := servingAt.FindStringSubmatch(out.String()); m != nil {
			return m[1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the serve command never announced its base URL; output so far: %q", out.String())
	return ""
}

// TestUnit_ServeQuirkserver_ServesWritesTheSpecAndStopsOnCancel runs the
// full hidden verb through the command tree: it must write the document,
// announce a base URL that answers the documented surface, and return
// cleanly when its context ends — the programmatic stand-in for SIGINT.
func TestUnit_ServeQuirkserver_ServesWritesTheSpecAndStopsOnCancel(t *testing.T) {
	t.Parallel()
	specPath := filepath.Join(t.TempDir(), "openapi.yaml")
	out := &syncWriter{}

	root := newRootCommand()
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"__serve-quirkserver", "--addr", "127.0.0.1:0", "--spec", specPath})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()

	base := awaitBaseURL(t, out)

	// The spec landed, byte-identical to what the package embeds.
	written, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("the spec was not written: %v", err)
	}
	if !bytes.Equal(written, quirkserver.Spec()) {
		t.Error("the written spec differs from quirkserver.Spec()")
	}

	// The announced URL serves the documented surface. A private transport,
	// not http.DefaultClient: parallel tests elsewhere close httptest
	// servers, and that closes the default transport's idle connections
	// mid-flight.
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	client := &http.Client{Transport: tr}
	response, err := client.Post(base+"/things", "application/json", //nolint:noctx // a test
		strings.NewReader(`{"name":"one","mode":"basic","code":"abc","notes":"n"}`))
	if err != nil {
		t.Fatalf("POST %s/things: %v", base, err)
	}
	var created map[string]any
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatalf("decoding the create response: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated || created["id"] == nil {
		t.Fatalf("create answered %d %v, want 201 with an id", response.StatusCode, created)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the serve command exited with %v, want a clean stop", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the serve command did not stop after its context was cancelled")
	}
}

// TestUnit_ServeQuirkserver_HiddenFromHelp holds the verb to its bargain:
// development machinery appears in no help output.
func TestUnit_ServeQuirkserver_HiddenFromHelp(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := run(t, "--help")
	if code != ExitOK {
		t.Fatalf("--help exited %d: %s", code, stderr)
	}
	if strings.Contains(stdout, "quirkserver") {
		t.Fatalf("help mentions the hidden verb:\n%s", stdout)
	}
}

// TestUnit_ServeQuirkserver_RefusesAnUnusableAddress asserts the listen
// failure comes back as a failure exit, not a hang.
func TestUnit_ServeQuirkserver_RefusesAnUnusableAddress(t *testing.T) {
	t.Parallel()
	err := serveQuirkserver(context.Background(), "not-an-address", "", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "cannot listen") {
		t.Fatalf("err = %v, want a listen refusal", err)
	}
}

// TestUnit_ServeQuirkserver_RefusesAnUnwritableSpecPath asserts a spec path
// that cannot be written fails the verb before it settles into serving.
func TestUnit_ServeQuirkserver_RefusesAnUnwritableSpecPath(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "no-such-dir", "openapi.yaml")
	err := serveQuirkserver(context.Background(), "127.0.0.1:0", missing, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "writing the quirkserver document") {
		t.Fatalf("err = %v, want a spec-write refusal", err)
	}
}
