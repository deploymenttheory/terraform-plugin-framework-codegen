// propose.go is the compiling half of revision: committed audit
// observations become proposed corrections, closing the evidence loop that
// Materialize's applying half consumes. Propose is deterministic — the same
// observations against the same revised state produce byte-identical files —
// and convergent: an observation whose fact the revised document already
// states proposes nothing, so accept-then-reaudit settles instead of
// oscillating.
package revise

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/correction"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/store"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

// Marker is one rejected-proposal marker in spec/corrections/rejected/: the
// durable record that a human looked at the named observation's proposed
// correction and said no. A marker suppresses re-proposal of that
// observation permanently — deleting the marker is the only way back.
type Marker struct {
	ObservationID string    `json:"observationID"`
	Reason        string    `json:"reason"`
	RejectedAt    time.Time `json:"rejectedAt"`
}

// Options configures ProposeWith beyond the defaults Propose uses.
type Options struct {
	// AutoAccept lists observation kinds — config audit.auto_accept — whose
	// compiled corrections skip proposed/ and land directly in
	// spec/corrections/ as accepted, named with an auto-NNN- prefix.
	AutoAccept []string
	// ObservationsDir overrides where committed observations are read from.
	// Empty means the conventional layout: the audit/observations directory
	// beside the spec directory.
	ObservationsDir string
}

// Written reports one correction file Propose wrote.
type Written struct {
	// Path is where the correction landed.
	Path string
	// ObservationID, Entity, Attribute and Kind identify the observation the
	// correction compiles; Attribute is empty for entity-level kinds.
	ObservationID string
	Entity        string
	Attribute     string
	Kind          observe.Kind
	// Stale marks a correction compiled from an observation recorded against
	// a superseded document — see Proposals.Stale.
	Stale bool
}

// Note reports one observation Propose did not compile into a file, and why.
type Note struct {
	ObservationID string
	Entity        string
	Attribute     string
	Kind          observe.Kind
	Reason        string
}

// Proposals is one Propose run's report.
type Proposals struct {
	// Observations is how many committed observations were read.
	Observations int
	// Proposed lists the corrections written to spec/corrections/proposed/,
	// each awaiting a human decision.
	Proposed []Written
	// AutoAccepted lists the corrections written directly into
	// spec/corrections/ because their kind is configured auto-accept.
	AutoAccepted []Written
	// Suppressed lists observations a rejected marker blocked.
	Suppressed []Note
	// NotConfirmed lists observations whose outcome is inconclusive, blocked
	// or timeoutExhausted — reported, never compiled.
	NotConfirmed []Note
	// AlreadyStated lists observations whose fact the revised document
	// already states, which is what makes re-auditing converge.
	AlreadyStated []Note
	// NoForm lists observations no correction form exists for yet.
	NoForm []Note
	// Vetoed lists observations a sibling observation blocks.
	Vetoed []Note
	// Unplaceable lists observations naming an entity, operation or
	// attribute the revised document does not have.
	Unplaceable []Note
	// Stale lists observations recorded against a superseded document —
	// flagged, and still compiled: the correction's own staleness refusal at
	// application time is the final arbiter.
	Stale []Note
}

// Propose compiles the committed audit observations into proposed
// corrections under dir/corrections/proposed/, with no kinds auto-accepted.
// dir is the spec directory, as for Materialize; observations are read from
// the conventional audit/observations directory beside it.
func Propose(dir string) (Proposals, error) {
	return ProposeWith(dir, Options{})
}

// compiledName matches the proposed files Propose itself writes, which it
// clears and rewrites on every run so a re-run converges instead of
// accreting. Hand-authored proposals do not match and are left alone.
var compiledName = regexp.MustCompile(`^\d{3}-.+\.correction\.json$`)

// ProposeWith is Propose with options.
func ProposeWith(dir string, opts Options) (Proposals, error) {
	var p Proposals
	if err := checkAutoAccept(opts.AutoAccept); err != nil {
		return p, err
	}

	lock, err := store.Verify(dir)
	if err != nil {
		return p, err
	}

	obsDir := opts.ObservationsDir
	if obsDir == "" {
		obsDir = filepath.Join(filepath.Dir(dir), "audit", "observations")
	}
	obs, err := observe.Read(obsDir)
	if err != nil {
		return p, err
	}
	p.Observations = len(obs)

	correctionsDir := filepath.Join(dir, correction.DirName)
	proposedDir := filepath.Join(correctionsDir, ProposedDirName)
	if err := clearCompiled(proposedDir); err != nil {
		return p, err
	}
	if len(obs) == 0 {
		return p, nil
	}

	markers, err := readMarkers(filepath.Join(correctionsDir, RejectedDirName))
	if err != nil {
		return p, err
	}

	state, entities, err := revisedState(dir, correctionsDir)
	if err != nil {
		return p, err
	}

	observe.Sort(obs)
	comp := &compiler{entities: entities, state: state, vetoes: vetoSet(obs)}

	type candidate struct {
		written Written
		corr    correction.Correction
	}
	var candidates []candidate

	for _, o := range obs {
		note := Note{ObservationID: o.ID, Entity: o.Entity, Attribute: o.Attribute, Kind: o.Kind}
		if m, ok := markers[o.ID]; ok {
			note.Reason = m.Reason
			p.Suppressed = append(p.Suppressed, note)
			continue
		}
		if o.Outcome != observe.OutcomeConfirmed {
			note.Reason = fmt.Sprintf("outcome %s never compiles into a correction", o.Outcome)
			p.NotConfirmed = append(p.NotConfirmed, note)
			continue
		}

		stale := o.SpecHash != lock.SHA256
		if stale {
			staleNote := note
			staleNote.Reason = "observed against a superseded document"
			p.Stale = append(p.Stale, staleNote)
		}

		res, err := comp.compile(o)
		if err != nil {
			return p, err
		}
		note.Reason = res.reason
		switch res.category {
		case catCompiled:
			// falls through to the correction below
		case catAlreadyStated:
			p.AlreadyStated = append(p.AlreadyStated, note)
			continue
		case catNoForm:
			p.NoForm = append(p.NoForm, note)
			continue
		case catVetoed:
			p.Vetoed = append(p.Vetoed, note)
			continue
		case catUnplaceable:
			p.Unplaceable = append(p.Unplaceable, note)
			continue
		}

		corr := correction.Correction{
			Justification: res.justification,
			Evidence:      "audit/observations/" + o.Entity + observe.FileSuffix + "#" + o.ID,
			Operations:    res.ops,
		}
		// Later compilations see this correction's effect, so two
		// observations touching the same schema compose instead of
		// colliding.
		comp.state, err = correction.Apply(comp.state, []correction.Correction{corr})
		if err != nil {
			return p, fmt.Errorf("internal: the correction compiled for observation %s does not apply: %w", o.ID, err)
		}
		candidates = append(candidates, candidate{
			written: Written{ObservationID: o.ID, Entity: o.Entity, Attribute: o.Attribute, Kind: o.Kind, Stale: stale},
			corr:    corr,
		})
	}

	auto := map[string]bool{}
	for _, k := range opts.AutoAccept {
		auto[k] = true
	}

	// One shared ordinal sequence over the sorted candidates, so file order
	// is compilation order — the order the operations were derived to apply
	// in.
	for i, cand := range candidates {
		ordinal := i + 1
		var path string
		var err error
		if auto[string(cand.written.Kind)] {
			path, err = writeAuto(correctionsDir, ordinal, cand.written.Entity, cand.corr)
		} else {
			path = filepath.Join(proposedDir, fmt.Sprintf("%03d-%s%s", ordinal, cand.written.Entity, correction.Suffix))
			err = writeCorrection(path, cand.corr)
		}
		if err != nil {
			return p, err
		}
		cand.written.Path = path
		if auto[string(cand.written.Kind)] {
			p.AutoAccepted = append(p.AutoAccepted, cand.written)
		} else {
			p.Proposed = append(p.Proposed, cand.written)
		}
	}
	return p, nil
}

// compilableKinds is the closed observation-kind vocabulary, for validating
// the configured auto-accept list before anything is written.
var compilableKinds = []string{
	string(observe.KindWritable), string(observe.KindImmutable),
	string(observe.KindRequiredByAPI), string(observe.KindRequiredWhen),
	string(observe.KindServerDefault), string(observe.KindDerivedDefault),
	string(observe.KindNormalisation), string(observe.KindIgnoredOnUpdate),
	string(observe.KindServerForced), string(observe.KindVolatile),
	string(observe.KindValues), string(observe.KindUpdateStyle),
	string(observe.KindDeleteNotFoundOK), string(observe.KindReadAfterWrite),
}

func checkAutoAccept(kinds []string) error {
	for _, k := range kinds {
		if !slices.Contains(compilableKinds, k) {
			return fmt.Errorf("audit.auto_accept: %q is not an observation kind (one of %s)",
				k, strings.Join(compilableKinds, ", "))
		}
	}
	return nil
}

// vetoSet gathers the entity/attribute pairs where a confirmed
// derivedDefault observation blocks a static serverDefault correction.
func vetoSet(obs []observe.Observation) map[[2]string]bool {
	vetoes := map[[2]string]bool{}
	for _, o := range obs {
		if o.Kind == observe.KindDerivedDefault && o.Outcome == observe.OutcomeConfirmed {
			vetoes[[2]string{o.Entity, o.Attribute}] = true
		}
	}
	return vetoes
}

// revisedState builds the current revised state — the pinned upstream
// document with every accepted correction applied — and classifies it.
func revisedState(dir, correctionsDir string) ([]byte, map[string]specmodel.Classification, error) {
	accepted, err := correction.Load(correctionsDir)
	if err != nil {
		return nil, nil, err
	}
	upstream, err := os.ReadFile(filepath.Join(dir, store.DocumentName)) //nolint:gosec // the fixed name under the operator-supplied dir
	if err != nil {
		return nil, nil, err
	}
	state, err := correction.Apply(upstream, accepted)
	if err != nil {
		return nil, nil, err
	}

	doc, err := specmodel.Load(state)
	if err != nil {
		return nil, nil, fmt.Errorf("the revised document is not loadable, so observations cannot be placed: %w", err)
	}
	entities := map[string]specmodel.Classification{}
	for _, c := range specmodel.Classify(doc).Entities {
		entities[c.Key] = c
	}
	return state, entities, nil
}

// clearCompiled removes the proposed files a previous Propose run wrote, so
// each run's output replaces the last instead of accreting beside it.
func clearCompiled(proposedDir string) error {
	entries, err := os.ReadDir(proposedDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !compiledName.MatchString(e.Name()) {
			continue
		}
		if err := os.Remove(filepath.Join(proposedDir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// readMarkers loads every rejected marker, keyed by observation ID. The
// files are committed and hand-editable, so decoding is strict: an unknown
// key or a marker naming no observation is refused by file name.
func readMarkers(dir string) (map[string]Marker, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	markers := map[string]Marker{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path) //nolint:gosec // enumerated from the corrections dir
		if err != nil {
			return nil, err
		}
		var m Marker
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			return nil, fmt.Errorf("%s is not a usable rejected marker: %w", path, err)
		}
		if m.ObservationID == "" {
			return nil, fmt.Errorf("%s names no observationID; a marker must say which observation was rejected", path)
		}
		if m.Reason == "" {
			return nil, fmt.Errorf("%s gives no reason; a rejection must say why, or it cannot be revisited", path)
		}
		markers[m.ObservationID] = m
	}
	return markers, nil
}

// writeAuto writes an auto-accepted correction into the accepted directory.
// A name collision with an earlier run's file for a different observation
// bumps the ordinal rather than silently replacing committed evidence; the
// same observation recompiled (its value moved) overwrites its own file.
func writeAuto(correctionsDir string, ordinal int, entity string, corr correction.Correction) (string, error) {
	for ; ; ordinal++ {
		path := filepath.Join(correctionsDir, fmt.Sprintf("auto-%03d-%s%s", ordinal, entity, correction.Suffix))
		existing, err := os.ReadFile(path) //nolint:gosec // the computed name under the corrections dir
		if os.IsNotExist(err) {
			return path, writeCorrection(path, corr)
		}
		if err != nil {
			return "", err
		}
		var prev correction.Correction
		if json.Unmarshal(existing, &prev) == nil && prev.Evidence == corr.Evidence {
			return path, writeCorrection(path, corr)
		}
	}
}

// writeCorrection encodes one correction file deterministically: fixed field
// order, two-space indent, HTML escaping off, trailing newline.
func writeCorrection(path string, corr correction.Correction) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(corr); err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	return os.WriteFile(path, buf.Bytes(), 0o600)
}
