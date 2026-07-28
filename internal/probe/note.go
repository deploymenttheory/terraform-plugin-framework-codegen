package probe

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidFacts marks a facts document that cannot be trusted.
	ErrInvalidFacts = errors.New("invalid probe facts")

	// ErrRefused marks a mutating run the gate declined. Carries exit code 3.
	ErrRefused = errors.New("mutating probes refused")

	// ErrBudget marks a run that hit a blast-radius cap. Carries exit code 4.
	ErrBudget = errors.New("probe budget exceeded")

	// ErrOrphans marks objects the sweeper could not remove. Carries exit code 5, and
	// it is returned even when every fact was gathered successfully -- leaving rubbish
	// in somebody's tenant is a failed run.
	ErrOrphans = errors.New("probe left objects behind")

	// ErrReplayMismatch marks a replay whose requests did not match the cassette.
	// Carries exit code 6.
	ErrReplayMismatch = errors.New("replay does not match the cassette")

	// ErrRedaction marks a recording that would have committed a secret. Carries exit
	// code 7, and nothing is written at all.
	ErrRedaction = errors.New("redaction failed")
)

// Note is something a probe could not do, or could not decide.
//
// The same shape and purpose as openapi.Note, deliberately: `probe` reports what it
// could not establish exactly as `ingest` reports what it could not infer, so an
// operator reads one format across both stages.
//
// A note rather than a weak fact is the right output whenever a protocol was abandoned
// partway through -- a control request that failed, two fixtures that disagreed, a
// field that turned out not to be readable. Emitting a Suspected fact in those cases
// would put a claim in the store that no sequence actually supports.
type Note struct {
	Resource string `json:"resource"`
	// JSONPath is the field, empty for a resource-level note.
	JSONPath string `json:"jsonPath,omitempty"`
	// Probe names what gave up, so the note can be acted on.
	Probe   string `json:"probe,omitempty"`
	Message string `json:"message"`
}

func (n Note) String() string {
	at := n.Resource
	if n.JSONPath != "" {
		at += "." + n.JSONPath
	}
	if n.Probe != "" {
		return fmt.Sprintf("%s [%s]: %s", at, n.Probe, n.Message)
	}
	return fmt.Sprintf("%s: %s", at, n.Message)
}
