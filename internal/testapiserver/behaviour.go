package testapiserver

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

func (s *Server) applyDefaults(obj, sent map[string]any) {
	for field, value := range s.quirks.ConstantDefaults {
		if _, ok := sent[field]; !ok {
			obj[field] = value
		}
	}

	// Derived from another field, so two creates with different sources differ
	// -- which is what distinguishes a derived default from a constant one.
	for field, source := range s.quirks.DerivedDefaults {
		if _, ok := sent[field]; ok {
			continue
		}
		obj[field] = fmt.Sprintf("derived-from-%v", sent[source])
	}

	// A counter, so two byte-identical creates differ.
	if field := s.quirks.CounterDefault; field != "" {
		if _, ok := sent[field]; !ok {
			s.counter++
			obj[field] = fmt.Sprintf("counter-%d", s.counter)
		}
	}
}

func (s *Server) applySideEffects(obj, sent map[string]any) {
	for trigger, also := range s.quirks.WriteSideEffects {
		if v, ok := sent[trigger]; ok && v == true {
			obj[also] = true
		}
	}
}

// normalise applies the transforms that cause perpetual diffs.
func (s *Server) normalise(field string, v any) any {
	if contains(s.quirks.NormalisesCase, field) {
		if text, ok := v.(string); ok {
			v = strings.ToLower(text)
		}
	}
	if contains(s.quirks.TrimsWhitespace, field) {
		if text, ok := v.(string); ok {
			v = strings.TrimSpace(text)
		}
	}
	if contains(s.quirks.SortsLists, field) {
		if list, ok := v.([]any); ok {
			sorted := make([]any, len(list))
			copy(sorted, list)
			sort.Slice(sorted, func(i, j int) bool {
				return fmt.Sprint(sorted[i]) < fmt.Sprint(sorted[j])
			})
			v = sorted
		}
	}
	return v
}

// refusedFieldDetail renders the missing field as the quirk asks: a bare name,
// or a sentence an adjustment loop can read.
func (s *Server) refusedFieldDetail(field string) string {
	if s.quirks.NamesRefusedFieldInProse {
		return "field " + field + " is required"
	}
	return field
}

func (s *Server) missingRequired(body map[string]any) string {
	for _, field := range s.quirks.RequiredButUndeclared {
		if _, ok := body[field]; !ok {
			return field
		}
	}

	if c := s.quirks.ConditionallyRequired; c != nil {
		if v, ok := body[c.WhenField]; ok && equalJSON(v, c.WhenValue) {
			if _, present := body[c.Then]; !present {
				return c.Then
			}
		}
	}

	return ""
}

func (s *Server) rejectedEnumValue(body map[string]any) (string, any) {
	for field, allowed := range s.quirks.ClosedEnum {
		v, ok := body[field]
		if !ok {
			continue
		}
		text := fmt.Sprint(v)
		if !contains(allowed, text) {
			return field, v
		}
	}

	// A documented value the API refuses: the specification is stale.
	for field, refused := range s.quirks.RejectsDocumentedValue {
		if v, ok := body[field]; ok && fmt.Sprint(v) == refused {
			return field, v
		}
	}

	// A value legal only on one branch: refused unless the gate holds its
	// value.
	for key, cond := range s.quirks.RejectsValueUnless {
		field, want, found := strings.Cut(key, "=")
		if !found {
			continue
		}
		v, ok := body[field]
		if !ok || fmt.Sprint(v) != want {
			continue
		}
		if fmt.Sprint(body[cond.WhenField]) != fmt.Sprint(cond.WhenValue) {
			return field, v
		}
	}

	return "", nil
}

// knownQueryParameters are the parameters the server understands.
var knownQueryParameters = map[string]bool{
	"expand": true, "limit": true, "cursor": true, "aid": true,
}

func (s *Server) badQueryParameter(r *http.Request) string {
	keys := make([]string, 0, len(r.URL.Query()))
	for k := range r.URL.Query() {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if !knownQueryParameters[k] {
			return k
		}
	}

	return ""
}

// badTypedParameter rejects a bad value for a parameter that has a type, which is
// how the error-envelope check provokes an error without mutating anything.
func (s *Server) badTypedParameter(r *http.Request) string {
	for _, name := range s.quirks.TypedQueryParams {
		v := r.URL.Query().Get(name)
		if v == "" {
			continue
		}
		if _, err := strconv.Atoi(v); err != nil {
			return name
		}
	}
	return ""
}
