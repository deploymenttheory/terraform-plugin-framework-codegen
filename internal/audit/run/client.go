package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/plan"
)

// blockedError is a precondition failure scoped to one entity: a missing
// ${VAR}, an uncreatable parent, a refused shared tenant. It blocks the
// entity and never the run.
type blockedError struct{ reason string }

func (b blockedError) Error() string { return b.reason }

// budgetError is a budget exhausted mid-entity. It records
// timeoutExhausted and moves on, never failing the run.
type budgetError struct{ reason string }

func (b budgetError) Error() string { return b.reason }

// reqSpec is one request an executing step wants sent. Paths and values
// arrive as the plan spelled them; substitution happens here.
type reqSpec struct {
	method     string
	path       string
	pathValues map[string]string
	body       map[string]any
	query      url.Values
}

// httpResult is one response, body already read.
type httpResult struct {
	status  int
	body    []byte
	elapsed time.Duration
	// excerpt is the redactable proof fragment for observations.
	excerpt observe.Excerpt
}

// ok reports a 2xx.
func (h *httpResult) ok() bool { return h.status >= 200 && h.status < 300 }

// refused reports a 4xx: the API understood and said no, which is
// evidence. A 5xx is not — it discriminates nothing.
func (h *httpResult) refused() bool { return h.status >= 400 && h.status < 500 }

// object decodes the response body as a JSON object, nil when it is not
// one.
func (h *httpResult) object() map[string]any {
	var v map[string]any
	if err := json.Unmarshal(h.body, &v); err != nil {
		return nil
	}
	return v
}

// mentions reports whether the response body names the attribute — what
// lets a refusal be written down as being about the field rather than
// about anything at all.
func (h *httpResult) mentions(attribute string) bool {
	return attribute != "" && bytes.Contains(bytes.ToLower(h.body), []byte(strings.ToLower(attribute)))
}

// do sends one request: substitution, budget spend, sandbox guard, rate
// limit, auth, per-request timeout, logging. ent may be nil for requests
// outside any entity (cleanup passes).
func (r *runner) do(ctx context.Context, ent *entityState, spec reqSpec) (*httpResult, error) {
	if err := r.spend(ent); err != nil {
		return nil, err
	}

	path, err := r.resolvePath(ctx, ent, spec.path, spec.pathValues)
	if err != nil {
		return nil, err
	}
	u := *r.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	if len(spec.query) > 0 {
		u.RawQuery = spec.query.Encode()
	}

	if mutatingMethod(spec.method) {
		if err := r.guardMutation(&u); err != nil {
			return nil, err
		}
	}

	var bodyReader io.Reader
	var rawBody []byte
	if spec.body != nil {
		resolved, err := r.resolveBody(ctx, ent, spec.body)
		if err != nil {
			return nil, err
		}
		rawBody, err = json.Marshal(resolved)
		if err != nil {
			return nil, fmt.Errorf("audit run: encoding a %s %s body: %w", spec.method, spec.path, err)
		}
		bodyReader = bytes.NewReader(rawBody)
	}

	if err := r.bucket.wait(ctx); err != nil {
		return nil, err
	}

	reqCtx, cancel := context.WithTimeout(ctx, r.opts.RequestTimeoutOrDefault())
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, strings.ToUpper(spec.method), u.String(), bodyReader)
	if err != nil {
		return nil, fmt.Errorf("audit run: building %s %s: %w", spec.method, spec.path, err)
	}
	if rawBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if err := r.auth.apply(reqCtx, req); err != nil {
		return nil, err
	}

	if r.opts.beforeSend != nil {
		r.opts.beforeSend(req)
	}

	start := time.Now()
	resp, err := r.client.Do(req)
	if err != nil {
		r.log.Debug().Str("method", req.Method).Str("path", spec.path).Err(errRedacted(err, r.secretsNow())).Msg("request failed")
		return nil, fmt.Errorf("audit run: %s %s: %w", spec.method, spec.path, errRedacted(err, r.secretsNow()))
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return nil, fmt.Errorf("audit run: reading the %s %s response: %w", spec.method, spec.path, readErr)
	}

	res := &httpResult{
		status:  resp.StatusCode,
		body:    body,
		elapsed: time.Since(start),
		excerpt: observe.Excerpt{
			Method:           strings.ToUpper(spec.method),
			PathTemplate:     spec.path,
			Status:           resp.StatusCode,
			RequestFragment:  fragment(rawBody),
			ResponseFragment: fragment(body),
		},
	}

	// Every request and response is logged, redacted, at debug level.
	red := observe.Redact(res.excerpt, r.secretsNow())
	r.log.Debug().
		Str("method", red.Method).
		Str("path", red.PathTemplate).
		Int("status", red.Status).
		Dur("elapsed", res.elapsed).
		RawJSON("request", rawOrNull(red.RequestFragment)).
		RawJSON("response", rawOrNull(red.ResponseFragment)).
		Msg("request")

	return res, nil
}

// secretsNow asks the authenticator for the current secret set on every
// use, because an oauth2 authenticator learns new tokens mid-run and a
// cached list would let a refreshed token through redaction.
func (r *runner) secretsNow() []string { return r.auth.secretValues() }

// spend charges one request against the budgets, refusing before the
// request is made. Boundary cleanups are exempt — they spend from their
// own time allowance instead.
func (r *runner) spend(ent *entityState) error {
	if r.inCleanup {
		return nil
	}
	switch {
	case r.runOut != "":
		return budgetError{reason: r.runOut}
	case !r.deadline.IsZero() && time.Now().After(r.deadline):
		r.runOut = fmt.Sprintf("the run's time budget (%s) is exhausted", r.budget.Duration)
		return budgetError{reason: r.runOut}
	case r.reqTotal >= r.budget.Requests:
		r.runOut = fmt.Sprintf("the run's request budget (%d) is exhausted", r.budget.Requests)
		return budgetError{reason: r.runOut}
	case ent != nil && ent.requests >= ent.plan.Budget.Requests:
		return budgetError{reason: fmt.Sprintf("the entity's request budget (%d) is exhausted", ent.plan.Budget.Requests)}
	}
	r.reqTotal++
	if ent != nil {
		ent.requests++
	}
	return nil
}

// guardMutation is the per-request half of the sandbox guard: the
// operator acknowledgement and the host allowlist derived from the base
// URL, checked before every mutating request however the URL was built.
func (r *runner) guardMutation(u *url.URL) error {
	if err := r.checkSandboxEnv(); err != nil {
		return err
	}
	if u.Host != r.base.Host {
		return fmt.Errorf("audit run: refusing a mutating request to %s: the sandbox allowlist derived from the base URL admits only %s", u.Host, r.base.Host)
	}
	return nil
}

// resolvePath substitutes every path parameter and returns the concrete
// path. Missing environment variables and unresolvable created-object
// references surface as blockedError.
func (r *runner) resolvePath(ctx context.Context, ent *entityState, path string, values map[string]string) (string, error) {
	out := path
	for param, v := range values {
		resolved, err := r.resolveValue(ctx, ent, v)
		if err != nil {
			return "", err
		}
		out = strings.ReplaceAll(out, "{"+param+"}", url.PathEscape(resolved))
	}
	if strings.Contains(out, "{") {
		return "", blockedError{reason: fmt.Sprintf("path %s has a parameter no value was supplied for", path)}
	}
	return out, nil
}

// resolveValue resolves one token: <runid>, ${VAR}, $created:<entity>, or
// a literal.
func (r *runner) resolveValue(ctx context.Context, ent *entityState, v string) (string, error) {
	v = strings.ReplaceAll(v, plan.RunIDToken, r.runID)
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		name := v[2 : len(v)-1]
		val, ok := r.opts.Lookup(name)
		if !ok || val == "" {
			return "", blockedError{reason: fmt.Sprintf("the environment variable %s named by %s is not set", name, plan.InputsPath)}
		}
		return val, nil
	}
	if entity, ok := strings.CutPrefix(v, "$created:"); ok {
		return r.resolveCreated(ctx, ent, entity)
	}
	return v, nil
}

// resolveBody substitutes tokens through the body's string values.
func (r *runner) resolveBody(ctx context.Context, ent *entityState, body map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(body))
	for k, v := range body {
		if s, ok := v.(string); ok {
			resolved, err := r.resolveValue(ctx, ent, s)
			if err != nil {
				return nil, err
			}
			out[k] = resolved
			continue
		}
		out[k] = v
	}
	return out, nil
}

// fragment trims a body to an excerpt-sized JSON fragment; nil for an
// empty body, a marker for a non-JSON one — the redactor would withhold
// unparseable content anyway.
func fragment(body []byte) json.RawMessage {
	if len(body) == 0 {
		return nil
	}
	if !json.Valid(body) {
		out, _ := json.Marshal("[not JSON: " + fmt.Sprintf("%d bytes", len(body)) + "]")
		return out
	}
	if len(body) <= observe.MaxFragmentBytes {
		return json.RawMessage(body)
	}
	out, _ := json.Marshal(fmt.Sprintf("[fragment over %d bytes]", observe.MaxFragmentBytes))
	return out
}

func rawOrNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

// errRedacted removes secret values from an error's text — a URL error
// can embed the full request URL.
func errRedacted(err error, secrets []string) error {
	msg := err.Error()
	for _, s := range secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "[redacted]")
		}
	}
	return fmt.Errorf("%s", msg)
}

// items digs the object list out of a collection response: a bare array,
// or the first array-valued key of an envelope — discovered, not assumed,
// because every API spells its envelope differently.
func items(body []byte) []any {
	var direct []any
	if err := json.Unmarshal(body, &direct); err == nil {
		return direct
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}
	keys := make([]string, 0, len(envelope))
	for k := range envelope {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, preferred := range []string{"items", "data", "results"} {
		if arr, ok := envelope[preferred].([]any); ok {
			return arr
		}
	}
	for _, k := range keys {
		if arr, ok := envelope[k].([]any); ok {
			return arr
		}
	}
	return nil
}

// identifierOf extracts an object's id: the "id" key, else the first key
// (sorted) that spells like one. A JSON number renders without the
// decimal point a float64 would otherwise gain.
func identifierOf(obj map[string]any) string {
	if v, ok := obj["id"]; ok {
		return scalarString(v)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lk := strings.ToLower(k)
		if lk == "id" || strings.HasSuffix(lk, "_id") || strings.HasSuffix(k, "Id") {
			if s := scalarString(obj[k]); s != "" {
				return s
			}
		}
	}
	return ""
}

func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%f", t), "0"), ".")
	default:
		return ""
	}
}

// nameOf finds the prefixed name a body stamps into the object, the
// prefix pass's handle on it. Sorted keys, so the same body always
// yields the same name.
func nameOf(body map[string]any, prefix string) string {
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := body[k].(string); ok && strings.HasPrefix(s, prefix) {
			return s
		}
	}
	return ""
}
