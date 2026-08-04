package probe

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
)

// The mutating runner. Three decisions here are worth stating, because each one is a choice
// against the obvious implementation.
//
// # Cleanup is per probe, and the runner does it
//
// Not at the end of the run: a deadline or a ^C between the first probe and the last leaves
// everything behind, and peak live objects becomes the whole run's total rather than one
// probe's. The caps bound the peak, and only per-probe release makes the peak small.
//
// Not per object either: the immutability protocol needs its first object alive while it
// creates the second, which is the entire point of the second.
//
// And emphatically not by the probe itself. A probe that abandons its protocol returns early,
// which skips its own defer -- and the abandon paths are the majority of the branches in every
// mutating protocol. Cleanup must not depend on nine authors each remembering.
//
// # A panic is converted, not re-raised here
//
// Re-raising inside this package destroys the evidence: cmd never gets the report, never writes
// the orphan table, never appends the step summary. So the stack is captured *at the fault* --
// a later bare panic(r) yields a stack from the re-raise point, which is useless -- the sweep
// runs, and cmd re-raises last, after everything a human needs has been written.
//
// # Partial results survive an error
//
// MaxDeleteFailures defaults to zero, so a run stops early routinely. A probe that returned
// nothing on the way out would lose facts it had already established honestly, so every result
// is accumulated before the error is inspected.

// PanicError is a bug in a probe, captured rather than re-raised.
type PanicError struct {
	// Probe names what faulted.
	Probe string
	// Value is what was passed to panic.
	Value any
	// Stack is debug.Stack() taken at the fault. Captured there and not later, because a
	// stack from a re-raise point shows the re-raise and nothing about the cause.
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("probe %s panicked: %v", e.Probe, e.Value)
}

// ErrPanicked marks a run that hit a bug in a probe.
var ErrPanicked = errors.New("a probe panicked")

// Is makes errors.Is(err, ErrPanicked) work, so the exit-code mapping needs no type switch.
func (e *PanicError) Is(target error) bool { return target == ErrPanicked }

// exercise runs one probe and converts a panic into an error.
//
// Named and separate so the recover is around exactly one call. A recover around the whole loop
// would lose which probe faulted, and would abandon the remaining probes for a bug in one.
func exercise(
	ctx context.Context,
	s *MutatingSession,
	p MutatingProbe,
	sc Scope,
) (result Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Probe: p.Name(), Value: r, Stack: debug.Stack()}
		}
	}()

	// Returned unwrapped, deliberately. The caller classifies this error against the sentinels
	// that decide whether the run stops, and wrapping it here would add a layer that says only
	// "a probe returned this" -- which the ProbeOutcome already records.
	//
	//nolint:wrapcheck // a deliberate pass-through; see above
	return p.Exercise(ctx, s, sc)
}

// ReleaseProbe removes everything one probe created.
//
// Called by the runner immediately after Exercise returns, whatever it returned. Driven by the
// ledger rather than by anything the probe reports, so a probe that abandoned its protocol
// halfway is cleaned up exactly as thoroughly as one that finished.
//
// Failures are not returned: the run-level sweep is the backstop, and it will find anything left
// here. What matters is that the delete failure was counted, because that is what stops the run
// creating more.
func (s *MutatingSession) ReleaseProbe(ctx context.Context, probe string) int {
	released := 0

	for _, o := range Unresolved(s.cfg.Ledger.Entries()) {
		if o.Probe != probe || o.ID == "" {
			continue
		}

		resp, err := s.Delete(ctx, probe, o.ID)
		if err == nil && resp.Status < 400 {
			released++
		}
	}

	return released
}

// runMutatingProbes runs the mutating tier and reports what each probe did.
//
// Returns the first error that stopped the run, or nil. The report is always populated: an
// error here means fewer facts, never no report.
func runMutatingProbes(
	ctx context.Context,
	s *MutatingSession,
	probes []MutatingProbe,
	opts RunOptions,
	report *Report,
) error {
	sc := opts.scope()

	// Seeded with what the read tier established, so a mutating probe's precondition can be
	// satisfied by a read-tier fact as readily as by an earlier mutating one -- and, later, by a
	// committed facts document. The precondition is a fact, not a probe.
	findings := NewFactSet(report.Facts)
	s.cfg.Findings = findings

	var halt error

	for _, p := range probes {
		if halt != nil {
			// Reported rather than omitted, so a reader can see that the catalogue did not
			// finish and why. Silence would read as "these probes had nothing to say".
			report.Probes = append(report.Probes, ProbeOutcome{
				Name:   p.Name(),
				Kind:   ProbeKindMutating,
				Status: "skipped",
				Reason: "the run stopped earlier: " + halt.Error(),
			})

			continue
		}

		outcome, result, err := runOneMutating(ctx, s, p, sc, opts)

		report.Probes = append(report.Probes, outcome)
		// Accumulated before the error is inspected, deliberately: a probe stopped by a
		// budget or a delete failure has usually established something honestly first.
		report.Facts = append(report.Facts, result.Facts...)
		report.Notes = append(report.Notes, result.Notes...)
		report.Rehearsal = append(report.Rehearsal, result.Rehearsal...)

		// The runner is the only writer, which is what stops two probes disagreeing about what
		// the run has established and the last one to finish winning silently.
		findings.Add(result.Facts...)

		if err != nil && stopsTheRun(err) {
			halt = err
			report.Notes = append(report.Notes, Note{
				Resource: opts.Subject.Resource,
				Probe:    p.Name(),
				Message:  "the run stopped early: " + err.Error(),
			})
		}
	}

	return halt
}

// runOneMutating runs a probe, releases what it created, and classifies the outcome.
func runOneMutating(
	ctx context.Context,
	s *MutatingSession,
	p MutatingProbe,
	sc Scope,
	opts RunOptions,
) (ProbeOutcome, Result, error) {
	outcome := ProbeOutcome{Name: p.Name(), Kind: ProbeKindMutating}

	result, err := exercise(ctx, s, p, sc)

	// Before the outcome is classified, and unconditionally. This is the line that makes peak
	// live objects a property of one probe rather than of the whole run.
	s.ReleaseProbe(ctx, p.Name())

	outcome.Requests = result.Requests
	outcome.Facts = len(result.Facts)

	switch {
	case err == nil:
		outcome.Status = "ok"
		if len(result.Facts) == 0 {
			// A protocol that could not be completed honestly emits no fact, and that is a
			// correct outcome rather than a failure. Distinguished so a reader can tell it
			// from a probe that contributed.
			outcome.Status = "abandoned"
			outcome.Reason = "ran but established nothing"
		}

	case errors.Is(err, errNotImplemented):
		outcome.Status = "skipped"
		outcome.Reason = "not implemented yet"

	case errors.Is(err, ErrNoIdentifier):
		// The object exists, was recorded by name, and the prefix sweep will remove it. Not a
		// halt: the next probe can still work, and stopping would leave the remaining
		// protocols unexercised for a response-shape problem.
		outcome.Status = "failed"
		outcome.Reason = err.Error()

	default:
		outcome.Status = "failed"
		outcome.Reason = err.Error()
	}

	return outcome, result, err
}

// stopsTheRun reports whether an error means nothing further should be attempted.
//
// The list is short and every entry is about not making things worse. A budget cap was set
// deliberately; a delete failure means we have demonstrated we cannot clean up, and creating
// more after that is the worst available behaviour; a cancelled run has no time left; a
// credential failure would make every later observation a fact about the token; and a panic is
// a bug whose blast radius is unknown by definition.
func stopsTheRun(err error) bool {
	for _, sentinel := range []error{
		ErrBudget, ErrDeleteFailures, ErrCancelled, ErrAuth, ErrLedger, ErrPanicked,
	} {
		if errors.Is(err, sentinel) {
			return true
		}
	}

	return false
}
