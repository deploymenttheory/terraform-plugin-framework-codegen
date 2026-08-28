package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/infer"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
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
	header  http.Header
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

// do sends one request: substitution, budget spend, host allowlist, rate
// limit, auth, per-request timeout, logging. ent may be nil for requests
// outside any entity (cleanup passes).
func (r *runner) do(ctx context.Context, entity *entityState, spec reqSpec) (*httpResult, error) {
	if err := r.spend(entity); err != nil {
		return nil, err
	}

	path, err := r.resolvePath(ctx, entity, spec.path, spec.pathValues)
	if err != nil {
		return nil, err
	}
	u := *r.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	if len(spec.query) > 0 {
		u.RawQuery = spec.query.Encode()
	}

	if mutatingMethod(spec.method) {
		if err := r.refuseForeignHostWrite(&u); err != nil {
			return nil, err
		}
	}

	var rawBody []byte
	if spec.body != nil {
		resolved, err := r.resolveBody(ctx, entity, spec.body)
		if err != nil {
			return nil, err
		}
		rawBody, err = json.Marshal(resolved)
		if err != nil {
			return nil, fmt.Errorf("audit run: encoding a %s %s body: %w", spec.method, spec.path, err)
		}
	}

	// A rate-limit refusal says nothing about the API's behaviour, so it is
	// waited out and retried rather than written down as evidence. Every
	// attempt is charged to the budget: a retried request is real load on the
	// tenant whatever the server did with it. Exhausting the budget mid-retry
	// ends the attempts and hands back the refusal, which is the truthful
	// answer at that point.
	var res *httpResult
	for attempt := 1; ; attempt++ {
		if attempt > 1 {
			if err := r.spend(entity); err != nil {
				return res, nil
			}
		}

		var err error
		res, err = r.send(ctx, u, spec, rawBody)
		if err != nil {
			return nil, err
		}
		if !rateLimited(res.status) {
			return res, nil
		}

		if next := r.backoff.record(r.bucket.rate()); next > 0 {
			r.bucket.slow(next)
			r.log.Info().Int("rps", next).Str("path", spec.path).
				Msg("rate limited repeatedly; slowing the rest of the run down")
		}

		retry, err := r.backoff.pause(ctx, attempt, res.header)
		if err != nil {
			return nil, err
		}
		if !retry {
			return res, nil
		}
		r.log.Debug().Int("attempt", attempt).Str("method", spec.method).Str("path", spec.path).
			Msg("rate limited; retrying")
	}
}

// send makes one attempt: rate limit, jitter, auth, per-request timeout,
// logging. Everything above it — path substitution, the budget charge, the
// host allowlist, body resolution — happens once per logical request, not
// once per attempt.
func (r *runner) send(ctx context.Context, u url.URL, spec reqSpec, rawBody []byte) (*httpResult, error) {
	if err := r.bucket.wait(ctx); err != nil {
		return nil, err
	}
	if err := r.backoff.jitter(ctx, r.bucket.rate()); err != nil {
		return nil, err
	}

	// A fresh reader per attempt: the previous one is spent.
	var bodyReader io.Reader
	if rawBody != nil {
		bodyReader = bytes.NewReader(rawBody)
	}

	reqCtx, cancel := context.WithTimeout(ctx, r.opts.RequestTimeoutOrDefault())
	defer cancel()
	request, err := http.NewRequestWithContext(reqCtx, strings.ToUpper(spec.method), u.String(), bodyReader)
	if err != nil {
		return nil, fmt.Errorf("audit run: building %s %s: %w", spec.method, spec.path, err)
	}
	if rawBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	if err := r.auth.apply(reqCtx, request); err != nil {
		return nil, err
	}

	if r.opts.beforeSend != nil {
		r.opts.beforeSend(request)
	}

	start := time.Now()
	response, err := r.client.Do(request)
	if err != nil {
		r.log.Debug().Str("method", request.Method).Str("path", spec.path).Err(errRedacted(err, r.secretsNow())).Msg("request failed")
		return nil, fmt.Errorf("audit run: %s %s: %w", spec.method, spec.path, errRedacted(err, r.secretsNow()))
	}
	defer func() { _ = response.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if readErr != nil {
		return nil, fmt.Errorf("audit run: reading the %s %s response: %w", spec.method, spec.path, readErr)
	}

	res := &httpResult{
		status:  response.StatusCode,
		body:    body,
		header:  response.Header.Clone(),
		elapsed: time.Since(start),
		excerpt: observe.Excerpt{
			Method:           strings.ToUpper(spec.method),
			PathTemplate:     spec.path,
			Status:           response.StatusCode,
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
func (r *runner) spend(entity *entityState) error {
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
	case entity != nil && entity.requests >= entity.plan.Budget.Requests:
		return budgetError{reason: fmt.Sprintf("the entity's request budget (%d) is exhausted", entity.plan.Budget.Requests)}
	}
	r.reqTotal++
	if entity != nil {
		entity.requests++
	}
	return nil
}

// refuseForeignHostWrite is the per-request guard: the host allowlist derived from
// the base URL, checked before every mutating request however the URL was
// built.
func (r *runner) refuseForeignHostWrite(u *url.URL) error {
	if u.Host != r.base.Host {
		return fmt.Errorf("audit run: refusing a mutating request to %s: the host allowlist derived from the base URL admits only %s", u.Host, r.base.Host)
	}
	return nil
}

// resolvePath substitutes every path parameter and returns the concrete
// path. Missing environment variables and unresolvable created-object
// references surface as blockedError.
func (r *runner) resolvePath(ctx context.Context, entity *entityState, path string, values map[string]string) (string, error) {
	out := path
	for parameter, v := range values {
		resolved, err := r.resolveValue(ctx, entity, v)
		if err != nil {
			return "", err
		}
		out = strings.ReplaceAll(out, "{"+parameter+"}", url.PathEscape(resolved))
	}
	if strings.Contains(out, "{") {
		return "", blockedError{reason: fmt.Sprintf("path %s has a parameter no value was supplied for", path)}
	}
	return out, nil
}

// resolveValue resolves one token: <runid>, ${VAR}, $created:<entity>, or
// a literal.
func (r *runner) resolveValue(ctx context.Context, entity *entityState, v string) (string, error) {
	v = strings.ReplaceAll(v, plan.RunIDToken, r.runID)
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		name := v[2 : len(v)-1]
		value, ok := r.opts.Lookup(name)
		if !ok || value == "" {
			return "", blockedError{reason: fmt.Sprintf("the environment variable %s named by %s is not set", name, plan.InputsPath)}
		}
		return value, nil
	}
	if entityKey, ok := strings.CutPrefix(v, "$created:"); ok {
		return r.resolveCreated(ctx, entity, entityKey)
	}
	if path, ok := strings.CutPrefix(v, BorrowToken); ok {
		id, found := r.borrowFromPath(ctx, entity, path)
		if !found {
			return "", unsatisfiedReference{path: path}
		}
		return id, nil
	}
	return v, nil
}

// unsatisfiedReference is a borrow token whose collection served no object.
// A caller that can leave the reference out does so; one that cannot blocks
// the entity with the collection named.
type unsatisfiedReference struct {
	path string
}

func (u unsatisfiedReference) Error() string {
	return fmt.Sprintf("the %s collection holds no object to reference, and a synthesised id is refused by construction", u.path)
}

// resolveReference resolves one body value under its field name, recording
// a borrow adjustment the first time a bound reference is satisfied: the
// inference reads the adjustment as the signal that the field holds a live
// id, whether the loop borrowed it after a refusal or synthesis bound it
// before the first request.
func (r *runner) resolveReference(ctx context.Context, entity *entityState, field, v string) (string, error) {
	resolved, err := r.resolveValue(ctx, entity, v)
	if err != nil || entity == nil {
		return resolved, err
	}
	if path, ok := strings.CutPrefix(v, BorrowToken); ok && !r.borrowRecorded[entity.plan.Entity+"\x00"+field] {
		r.borrowRecorded[entity.plan.Entity+"\x00"+field] = true
		r.recordAdjustment(entity, infer.AdjustBorrow, field, path, "")
	}
	return resolved, nil
}

// resolveBody substitutes tokens through the body's string values, at every
// depth: a reference bound inside a nested object or a list element is
// resolved the same way as one at the top.
func (r *runner) resolveBody(ctx context.Context, entity *entityState, body map[string]any) (map[string]any, error) {
	resolved, err := r.resolveAny(ctx, entity, "", body, true)
	if err != nil {
		return nil, err
	}
	return resolved.(map[string]any), nil
}

// resolveAny is resolveBody's recursion over maps, lists and strings, carrying
// the field name a value sits under so a resolved reference is recorded
// against it.
//
// A reference the API cannot satisfy — the collection is empty — is left out
// where leaving it out is a body the API can still read: an element of a
// list of ids, or a top-level field the entity does not require. Anywhere
// else it blocks with the collection named, because a nested object missing
// its id, or a required field missing outright, is refused for a reason the
// refusal will not spell.
func (r *runner) resolveAny(ctx context.Context, entity *entityState, field string, value any, top bool) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, inner := range v {
			resolved, err := r.resolveAny(ctx, entity, k, inner, false)
			var unsatisfied unsatisfiedReference
			if errors.As(err, &unsatisfied) && top && !r.requiredField(entity, k) {
				continue
			}
			if err != nil {
				return nil, blockedIfUnsatisfied(err)
			}
			out[k] = resolved
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(v))
		for i := range v {
			resolved, err := r.resolveAny(ctx, entity, field, v[i], false)
			var unsatisfied unsatisfiedReference
			if _, isString := v[i].(string); isString && errors.As(err, &unsatisfied) {
				continue
			}
			if err != nil {
				return nil, err
			}
			out = append(out, resolved)
		}
		return out, nil
	case string:
		return r.resolveReference(ctx, entity, field, v)
	default:
		return value, nil
	}
}

// requiredField reports whether the entity's strategy declares the field
// required in a create body. Unknown fields read as not required.
func (r *runner) requiredField(entity *entityState, field string) bool {
	if entity == nil {
		return false
	}
	h, ok := r.hints[entity.plan.Entity][field]
	return ok && h.Required
}

// blockedIfUnsatisfied turns an unsatisfied reference that nothing above it
// could leave out into the block reason the entity ends on.
func blockedIfUnsatisfied(err error) error {
	var unsatisfied unsatisfiedReference
	if errors.As(err, &unsatisfied) {
		return blockedError{reason: unsatisfied.Error()}
	}
	return err
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
	message := err.Error()
	for _, s := range secrets {
		if s != "" {
			message = strings.ReplaceAll(message, s, "[redacted]")
		}
	}
	return fmt.Errorf("%s", message)
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
