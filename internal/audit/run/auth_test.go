package run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
)

func testRequest(t *testing.T) *http.Request {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.invalid/things", nil)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func TestUnit_Auth_BearerTokenHeader(t *testing.T) {
	t.Parallel()
	a, err := newAuthenticator(Auth{Method: config.AuthBearerToken},
		lookupOf(map[string]string{config.SecretToken: "tok123456"}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest(t)
	if err := a.apply(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer tok123456" {
		t.Fatalf("Authorization = %q", got)
	}
	if secrets := a.secretValues(); len(secrets) != 1 || secrets[0] != "tok123456" {
		t.Fatalf("secretValues = %v", secrets)
	}
}

func TestUnit_Auth_APIKeyHeader(t *testing.T) {
	t.Parallel()
	a, err := newAuthenticator(Auth{Method: config.AuthAPIKeyHeader, APIKeyHeader: "X-Api-Key"},
		lookupOf(map[string]string{config.SecretToken: "key123456"}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest(t)
	_ = a.apply(context.Background(), request)
	if got := request.Header.Get("X-Api-Key"); got != "key123456" {
		t.Fatalf("X-Api-Key = %q", got)
	}

	// The method without its header name is unusable.
	_, err = newAuthenticator(Auth{Method: config.AuthAPIKeyHeader},
		lookupOf(map[string]string{config.SecretToken: "key123456"}), time.Second)
	if err == nil || !strings.Contains(err.Error(), "api_key_header") {
		t.Fatalf("err = %v, want a refusal naming the missing config key", err)
	}
}

func TestUnit_Auth_Basic(t *testing.T) {
	t.Parallel()
	a, err := newAuthenticator(Auth{Method: config.AuthBasic}, lookupOf(map[string]string{
		config.SecretUsername: "alice", config.SecretPassword: "s3cretpw",
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest(t)
	_ = a.apply(context.Background(), request)
	user, pass, ok := request.BasicAuth()
	if !ok || user != "alice" || pass != "s3cretpw" {
		t.Fatalf("basic auth = %q %q %v", user, pass, ok)
	}
	if secrets := a.secretValues(); len(secrets) != 2 {
		t.Fatalf("secretValues = %v, want both halves", secrets)
	}
}

// TestUnit_Auth_OAuth2FetchesAndRefreshes exercises the client-credentials
// grant against a local token endpoint: the token is fetched on first
// use, sent as a bearer, and re-fetched once its declared lifetime falls
// inside the refresh margin. Every token ever held is a secret.
func TestUnit_Auth_OAuth2FetchesAndRefreshes(t *testing.T) {
	t.Parallel()
	var issued atomic.Int32
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil ||
			r.Form.Get("grant_type") != "client_credentials" ||
			r.Form.Get("client_id") != "cid12345" ||
			r.Form.Get("client_secret") != "csecret12345" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		n := issued.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			// One second of lifetime sits inside the 30s refresh margin,
			// so every apply refetches — which is the refresh path.
			"access_token": fmt.Sprintf("fetched-token-%d", n), "expires_in": 1,
		})
	}))
	t.Cleanup(tokens.Close)

	a, err := newAuthenticator(Auth{Method: config.AuthOAuth2ClientCredentials, TokenURL: tokens.URL},
		lookupOf(map[string]string{
			config.SecretClientID: "cid12345", config.SecretClientSecret: "csecret12345",
		}), time.Second)
	if err != nil {
		t.Fatal(err)
	}

	request := testRequest(t)
	if err := a.apply(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer fetched-token-1" {
		t.Fatalf("Authorization = %q", got)
	}
	req2 := testRequest(t)
	if err := a.apply(context.Background(), req2); err != nil {
		t.Fatal(err)
	}
	if got := req2.Header.Get("Authorization"); got != "Bearer fetched-token-2" {
		t.Fatalf("second Authorization = %q, want a refreshed token", got)
	}

	secrets := strings.Join(a.secretValues(), " ")
	for _, want := range []string{"cid12345", "csecret12345", "fetched-token-1", "fetched-token-2"} {
		if !strings.Contains(secrets, want) {
			t.Errorf("secretValues misses %q: %v", want, a.secretValues())
		}
	}

	// A missing token_url is refused at construction.
	if _, err := newAuthenticator(Auth{Method: config.AuthOAuth2ClientCredentials},
		lookupOf(map[string]string{config.SecretClientID: "x", config.SecretClientSecret: "y"}), time.Second); err == nil {
		t.Fatal("oauth2 without token_url must refuse")
	}
}

func TestUnit_Auth_OAuth2TokenEndpointFailure(t *testing.T) {
	t.Parallel()
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	t.Cleanup(tokens.Close)

	a, err := newAuthenticator(Auth{Method: config.AuthOAuth2ClientCredentials, TokenURL: tokens.URL},
		lookupOf(map[string]string{config.SecretClientID: "cid", config.SecretClientSecret: "cs"}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.apply(context.Background(), testRequest(t)); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want the endpoint status without the body", err)
	}
}

func TestUnit_Auth_GitHubAppIsNotYetSupported(t *testing.T) {
	t.Parallel()
	_, err := newAuthenticator(Auth{Method: config.AuthGitHubApp}, lookupOf(map[string]string{
		config.SecretAppID: "1", config.SecretAppPrivateKey: "pem",
	}), time.Second)
	if err == nil || !strings.Contains(err.Error(), "not supported yet") {
		t.Fatalf("err = %v, want the not-yet refusal", err)
	}
}

func TestUnit_Auth_MissingSecretsAreNamed(t *testing.T) {
	t.Parallel()
	_, err := newAuthenticator(Auth{Method: config.AuthBasic}, lookupOf(map[string]string{}), time.Second)
	if err == nil || !strings.Contains(err.Error(), config.SecretUsername) || !strings.Contains(err.Error(), config.SecretPassword) {
		t.Fatalf("err = %v, want both missing roles named", err)
	}

	_, err = newAuthenticator(Auth{Method: "carrier_pigeon"}, lookupOf(nil), time.Second)
	if err == nil || !strings.Contains(err.Error(), "carrier_pigeon") {
		t.Fatalf("err = %v, want the unknown method named", err)
	}
}

// TestUnit_Auth_OAuth2CachesALongLivedToken: a token with real lifetime
// is fetched once and reused.
func TestUnit_Auth_OAuth2CachesALongLivedToken(t *testing.T) {
	t.Parallel()
	var issued atomic.Int32
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		issued.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "long-lived-token", "expires_in": 3600})
	}))
	t.Cleanup(tokens.Close)

	a, err := newAuthenticator(Auth{Method: config.AuthOAuth2ClientCredentials, TokenURL: tokens.URL},
		lookupOf(map[string]string{config.SecretClientID: "cid", config.SecretClientSecret: "cs"}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := a.apply(context.Background(), testRequest(t)); err != nil {
			t.Fatal(err)
		}
	}
	if got := issued.Load(); got != 1 {
		t.Fatalf("the token endpoint was hit %d times for a one-hour token", got)
	}
}
