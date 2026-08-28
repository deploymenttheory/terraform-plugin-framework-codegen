package testapiserver

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ErrorBodyShape names the shape an error body takes.
//
// The server and the auditor's error classifier must agree on the shapes:
// an audit that assumed one shape could not tell "rejected because the field
// is immutable" from "rejected because the token expired", and that
// distinction is what makes the immutability protocol possible at all. When
// the classifier lands it must recognise exactly these.
type ErrorBodyShape string

const (
	// ErrorBodyProblem is RFC 7807 application/problem+json, commonly used for
	// validation errors.
	ErrorBodyProblem ErrorBodyShape = "problem"
	// ErrorBodyOAuth is {"error","error_description"}, returned when a bearer
	// token is rejected.
	ErrorBodyOAuth ErrorBodyShape = "oauth"
	// ErrorBodyLegacy is {"errorMessage"}, returned when no credentials are
	// supplied.
	ErrorBodyLegacy ErrorBodyShape = "legacy"
	// ErrorBodyEmpty is no body at all, which is what a real 404 returns.
	ErrorBodyEmpty ErrorBodyShape = "empty"
)

func (s *Server) notFound(w http.ResponseWriter) {
	status := s.quirks.NotFoundStatus
	if status == 0 {
		status = http.StatusNotFound
	}
	s.fail(w, status, "not found", "")
}

// fail writes an error in whichever envelope the quirks select.
func (s *Server) fail(w http.ResponseWriter, status int, title, detail string) {
	switch s.quirks.ErrorBody {
	case ErrorBodyEmpty:
		// No body at all, which is what a real 404 returns and what the error
		// classifier has to have a fallback for.
		w.WriteHeader(status)

	case ErrorBodyOAuth:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":             "invalid_token",
			"error_description": joinDetail(title, detail),
		})

	case ErrorBodyLegacy:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errorMessage": joinDetail(title, detail),
		})

	case ErrorBodyProblem, "":
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		body := map[string]any{
			"type":     "about:blank",
			"title":    title,
			"status":   status,
			"instance": collectionPath,
		}
		if detail != "" {
			// The detail names the offending field, which is what lets the
			// auditor write the cause down as observed rather than guessed.
			body["detail"] = detail
		}
		_ = json.NewEncoder(w).Encode(body)

	default:
		w.WriteHeader(status)
	}
}

func joinDetail(title, detail string) string {
	if detail == "" {
		return title
	}
	return title + ": " + detail
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request) (map[string]any, error) {
	var out map[string]any

	if r.Body == nil {
		return map[string]any{}, nil
	}

	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding the request body: %w", err)
	}

	if out == nil {
		out = map[string]any{}
	}

	return out, nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// equalJSON compares two decoded JSON values.
//
// Via their encodings, because a value that came from json.Unmarshal and one
// written as a Go literal differ in type -- 1 is an int in a literal and a
// float64 after decoding -- and comparing with == would report a difference
// that is not there.
func equalJSON(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ja) == string(jb)
}
