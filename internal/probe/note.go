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

	// ErrLedger marks a ledger that could not be read or written.
	//
	// Carries exit code 5 with the orphans, because the consequence is the same: a create
	// that cannot be recorded is a create that cannot be cleaned up, so the safe response is
	// to refuse to issue it and report the objects that may exist.
	ErrLedger = errors.New("the probe ledger is unusable")

	// ErrNoIdentifier marks a created object whose identifier could not be located.
	//
	// Distinct from ErrLedger: the object exists and was recorded, but nothing can address it
	// for deletion, so only the sweeper's prefix pass can reach it.
	ErrNoIdentifier = errors.New("a created object's identifier could not be located")

	// ErrDeleteFailures marks a run that could not remove something it created.
	//
	// The cap defaults to zero, so this fires on the first failure. Continuing to create after
	// demonstrating you cannot clean up is the worst available behaviour, and it is worth a
	// dedicated sentinel so the runner does not have to infer the reason from a status code.
	ErrDeleteFailures = errors.New("a delete failed, so the run stopped creating")

	// ErrCancelled marks a run stopped by a signal or a deadline.
	//
	// Its own sentinel because it is not a failure of the probes: cancellation is a normal
	// outcome, and the cleanup that follows it is the interesting part.
	ErrCancelled = errors.New("the probe run was cancelled")
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
