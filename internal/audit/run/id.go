package run

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// extractID extracts a created object's id from a create — or a subsequent read —
// response, trying the shapes real APIs use in a fixed, documented order so the
// same response always yields the same id. The order runs strongest-signal
// first:
//
//  1. the Location / Content-Location response header. A 201 that points at the
//     new object by URL is the least ambiguous answer; the id is that URL's last
//     non-empty path segment.
//  2. a top-level id key in the body: the plain "id", an entity-scoped
//     "<entity>Id" / "<entity>_id", or any other key that spells like an id
//     (identifierOf's suffix rules).
//  3. a single-object envelope: the id inside a top-level "data", "result",
//     "item" or "<entity>" object that wraps the created object.
//  4. a "self" link: a top-level "self" URL string, or a "links.self" URL, whose
//     last path segment is the id.
//
// It returns "" only when none matched — the object exists (a create answered
// 2xx) but its id could not be learned, so it stays addressable only by the
// cleanup prefix pass and its id-derived observations must be inconclusive
// rather than silently skipped.
func extractID(entity string, res *httpResult) string {
	if res == nil {
		return ""
	}
	if id := idFromLocation(res.header); id != "" {
		return id
	}
	obj := res.object()
	if obj == nil {
		return ""
	}
	if id := idFromObject(entity, obj); id != "" {
		return id
	}
	for _, wrapper := range listWrapperKeys(entity) {
		if inner, ok := obj[wrapper].(map[string]any); ok {
			if id := idFromObject(entity, inner); id != "" {
				return id
			}
		}
	}
	return idFromResourceURL(obj)
}

// idFromLocation reads the id out of a Location or Content-Location header: the
// last non-empty path segment of the URL the header carries, query and fragment
// stripped. A header naming only a collection ("/monitors") yields the
// collection name, which is not an id — but a create that answers a bare
// collection Location is degenerate, and the body-based lookups run only when
// this returns "", so a real per-object Location ("/monitors/abc") wins first.
func idFromLocation(h http.Header) string {
	if h == nil {
		return ""
	}
	for _, key := range []string{"Location", "Content-Location"} {
		raw := strings.TrimSpace(h.Get(key))
		if raw == "" {
			continue
		}
		if u, err := url.Parse(raw); err == nil && u.Path != "" {
			raw = u.Path
		}
		if seg := lastPathSegment(raw); seg != "" {
			return seg
		}
	}
	return ""
}

// idFromObject reads a top-level id key from one object. identifierOf handles
// "id" and any "*_id" / "*Id" suffix; the entity-scoped fallback catches the
// spellings its suffix rules miss, such as an all-caps "monitorID".
func idFromObject(entity string, obj map[string]any) string {
	if id := identifierOf(obj); id != "" {
		return id
	}
	le := strings.ToLower(entity)
	if le == "" {
		return ""
	}
	for _, k := range sortedKeys(obj) {
		lk := strings.ToLower(k)
		if lk == le+"id" || lk == le+"_id" {
			if s := scalarString(obj[k]); s != "" {
				return s
			}
		}
	}
	return ""
}

// idFromResourceURL reads the id out of a self link: a top-level "self" URL string,
// or a "links" object carrying a "self" URL. The id is the link's last path
// segment.
func idFromResourceURL(obj map[string]any) string {
	if s, ok := obj["self"].(string); ok {
		if id := idFromURLString(s); id != "" {
			return id
		}
	}
	if links, ok := obj["links"].(map[string]any); ok {
		if s, ok := links["self"].(string); ok {
			if id := idFromURLString(s); id != "" {
				return id
			}
		}
	}
	return ""
}

// idFromURLString takes a link's last path segment as the id.
func idFromURLString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		raw = u.Path
	}
	return lastPathSegment(raw)
}

// lastPathSegment returns the last non-empty, slash-separated segment of a path.
func lastPathSegment(path string) string {
	path = strings.TrimRight(path, "/")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// listWrapperKeys names the single-object wrappers a create response may nest the
// created object under, entity-specific first so a "<entity>" wrapper is
// preferred over the generic ones, and otherwise in a fixed order for
// determinism.
func listWrapperKeys(entity string) []string {
	keys := []string{"data", "item", "result"}
	if entity != "" {
		keys = append([]string{entity}, keys...)
	}
	return keys
}

// sortedKeys lists a map's keys sorted, so a scan over them is deterministic.
func sortedKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
