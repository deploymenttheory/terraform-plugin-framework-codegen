package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
)

// authenticator injects credentials into outgoing requests. Exactly one
// exists per run, built once from the auth method and the environment, so
// the set of secret values the redactor must know is closed before the
// first request is sent.
type authenticator interface {
	// apply sets the request's credentials. It may itself fetch a token.
	apply(ctx context.Context, req *http.Request) error
	// secretValues lists every value redaction must remove: the
	// configured secrets plus anything fetched, such as an access token.
	secretValues() []string
}

// newAuthenticator builds the method's authenticator, reading the fixed
// TFPFGEN_AUTH_* role secrets through lookup. Every required secret must
// be present — config validate checks the same thing earlier, but this
// package must not trust that it ran.
func newAuthenticator(a Auth, lookup func(string) (string, bool), timeout time.Duration) (authenticator, error) {
	missing := config.MissingSecrets(a.Method, lookup)
	if len(missing) > 0 {
		return nil, fmt.Errorf("audit run: auth method %s needs secrets that are not set: %s",
			a.Method, strings.Join(missing, ", "))
	}

	get := func(name string) string { v, _ := lookup(name); return v }

	switch a.Method {
	case config.AuthBearerToken:
		return &headerAuth{header: "Authorization", value: "Bearer " + get(config.SecretToken), secret: get(config.SecretToken)}, nil
	case config.AuthAPIKeyHeader:
		if a.APIKeyHeader == "" {
			return nil, fmt.Errorf("audit run: auth method %s needs auth.api_key_header to name the header the token is sent under", a.Method)
		}
		return &headerAuth{header: a.APIKeyHeader, value: get(config.SecretToken), secret: get(config.SecretToken)}, nil
	case config.AuthBasic:
		return &basicAuth{username: get(config.SecretUsername), password: get(config.SecretPassword)}, nil
	case config.AuthOAuth2ClientCredentials:
		if a.TokenURL == "" {
			return nil, fmt.Errorf("audit run: auth method %s needs auth.token_url to fetch tokens from", a.Method)
		}
		return &oauth2Auth{
			tokenURL:     a.TokenURL,
			clientID:     get(config.SecretClientID),
			clientSecret: get(config.SecretClientSecret),
			client:       &http.Client{Timeout: timeout, Transport: newTransport()},
		}, nil
	case config.AuthGitHubApp:
		return nil, fmt.Errorf("audit run: auth method %s is not supported yet; the app-installation token exchange has not been built", a.Method)
	default:
		return nil, fmt.Errorf("audit run: unknown auth method %q", a.Method)
	}
}

// headerAuth sends one fixed header: bearer_token and api_key_header.
type headerAuth struct {
	header string
	value  string
	secret string
}

func (h *headerAuth) apply(_ context.Context, req *http.Request) error {
	req.Header.Set(h.header, h.value)
	return nil
}

func (h *headerAuth) secretValues() []string { return []string{h.secret} }

// basicAuth sends RFC 7617 basic credentials.
type basicAuth struct {
	username string
	password string
}

func (b *basicAuth) apply(_ context.Context, req *http.Request) error {
	req.SetBasicAuth(b.username, b.password)
	return nil
}

// secretValues treats the username as a secret too: it is a credential
// half, and redacting it costs nothing.
func (b *basicAuth) secretValues() []string { return []string{b.username, b.password} }

// oauth2Auth performs the client-credentials grant, caching the token and
// refreshing it shortly before it expires.
type oauth2Auth struct {
	tokenURL     string
	clientID     string
	clientSecret string
	client       *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
	// fetched accumulates every access token this run held, because an
	// already-rotated token in a committed excerpt is still a leak.
	fetched []string
}

// tokenRefreshMargin refreshes a token this long before its declared
// expiry, so a request never sails out with a token about to die mid-air.
const tokenRefreshMargin = 30 * time.Second

func (o *oauth2Auth) apply(ctx context.Context, req *http.Request) error {
	tok, err := o.currentToken(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

func (o *oauth2Auth) currentToken(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.token != "" && time.Now().Before(o.expires.Add(-tokenRefreshMargin)) {
		return o.token, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {o.clientID},
		"client_secret": {o.clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("audit run: building the token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("audit run: fetching a token from %s: %w", o.tokenURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("audit run: reading the token response: %w", err)
	}
	if resp.StatusCode >= 400 {
		// The body is not quoted: a token endpoint's error can echo the
		// client id, and this message may end up in CI logs.
		return "", fmt.Errorf("audit run: the token endpoint answered %d", resp.StatusCode)
	}

	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.AccessToken == "" {
		return "", fmt.Errorf("audit run: the token endpoint answered %d without an access_token", resp.StatusCode)
	}

	o.token = body.AccessToken
	o.fetched = append(o.fetched, body.AccessToken)
	ttl := time.Duration(body.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	o.expires = time.Now().Add(ttl)
	return o.token, nil
}

func (o *oauth2Auth) secretValues() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := []string{o.clientID, o.clientSecret}
	out = append(out, o.fetched...)
	return out
}
