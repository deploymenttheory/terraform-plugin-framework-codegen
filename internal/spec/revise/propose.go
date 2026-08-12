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
	"strconv"
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
	// The report describes the proposals this run writes, so last run's must
	// go before this one's are compiled: a report outliving its proposals
	// would send the pull-request job after files that no longer exist.
	if err := removeReport(proposedDir); err != nil {
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
	comp := &compiler{entities: entities, state: state, vetoes: vetoSet(obs), variants: variantSets(obs)}

	type candidate struct {
		written Written
		corr    correction.Correction
		// obs is kept whole because the report narrates from it: the value,
		// the excerpts and the condition are all things the correction file
		// itself does not carry.
		obs observe.Observation
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
			obs:     o,
		})
	}

	auto := map[string]bool{}
	for _, k := range opts.AutoAccept {
		auto[k] = true
	}

	// One shared ordinal sequence, continuing past every ordinal already
	// claimed — accepted corrections/, auto-accepted auto-NNN-, and any
	// proposal still in proposed/. A first round's proposals accepted into
	// corrections/ raise the floor, so a second round can never reissue a
	// number the first already committed and thereby clobber accepted
	// evidence when its proposals are accepted in turn.
	next, err := highestOrdinal(correctionsDir, proposedDir)
	if err != nil {
		return p, err
	}
	next++

	var reported []proposal
	for i := range candidates {
		cand := &candidates[i]
		var path string
		if auto[string(cand.written.Kind)] {
			path, next, err = writeAutoAccepted(correctionsDir, next, cand.written.Entity, cand.corr)
		} else {
			path = filepath.Join(proposedDir, fmt.Sprintf("%03d-%s%s", next, cand.written.Entity, correction.Suffix))
			err = writeCorrection(path, cand.corr)
			next++
		}
		if err != nil {
			return p, err
		}
		cand.written.Path = path
		if auto[string(cand.written.Kind)] {
			p.AutoAccepted = append(p.AutoAccepted, cand.written)
			continue
		}
		p.Proposed = append(p.Proposed, cand.written)
		reported = append(reported, proposal{
			obs: cand.obs, corr: cand.corr, file: filepath.Base(path), stale: cand.written.Stale,
		})
	}

	// Only what awaits a decision is reported. An auto-accepted correction
	// never becomes a pull request, so narrating it would describe a decision
	// nobody is being asked to make.
	if len(reported) > 0 {
		if err := writeReport(proposedDir, buildReport(reported)); err != nil {
			return p, err
		}
	}
	return p, nil
}

// ordinalName matches any correction file that leads with an ordinal, whether
// a proposed NNN- or an accepted auto-NNN-, capturing the ordinal.
var ordinalName = regexp.MustCompile(`^(?:auto-)?(\d{3})-.+\.correction\.json$`)

// autoName matches an accepted auto-NNN- correction file.
var autoName = regexp.MustCompile(`^auto-\d{3}-.+\.correction\.json$`)

// highestOrdinal is the largest ordinal any correction file across the given
// directories already carries, or zero when none do. Fresh proposals number
// from one past it so they never collide with an accepted file's ordinal.
func highestOrdinal(dirs ...string) (int, error) {
	highest := 0
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			m := ordinalName.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			if n, convErr := strconv.Atoi(m[1]); convErr == nil && n > highest {
				highest = n
			}
		}
	}
	return highest, nil
}

// compilableKinds is the closed observation-kind vocabulary, for validating
// the configured auto-accept list before anything is written. Two of its
// entries — normalisation and derivedDefault — have no correction form yet
// and compile to a NoForm note; naming them is a no-op rather than an error,
// because the auto-accept list says which kinds skip review, not which kinds
// exist.
var compilableKinds = []string{
	string(observe.KindWritable), string(observe.KindImmutable),
	string(observe.KindRequiredByAPI), string(observe.KindRequiredWhen),
	string(observe.KindServerDefault), string(observe.KindDerivedDefault),
	string(observe.KindNormalisation), string(observe.KindIgnoredOnUpdate),
	string(observe.KindServerForced), string(observe.KindVolatile),
	string(observe.KindValues), string(observe.KindUpdateStyle),
	string(observe.KindDeleteNotFoundOK), string(observe.KindReadAfterWrite),
	string(observe.KindUndocumentedFieldInSpec),
	string(observe.KindValidWhen), string(observe.KindDependsOn),
	string(observe.KindMutuallyExclusive), string(observe.KindValidConfiguration),
	string(observe.KindListResponseShape),
}

// CompilableKinds is the sorted vocabulary an audit.auto_accept entry must
// name, as a fresh slice no caller can mutate. Config validation consumes
// exactly this, so the check a human meets at `tfpfgen config validate` and
// the one `tfpfgen spec revise` enforces cannot drift apart.
func CompilableKinds() []string {
	out := slices.Clone(compilableKinds)
	slices.Sort(out)
	return out
}

func checkAutoAccept(kinds []string) error {
	for _, k := range kinds {
		if !slices.Contains(compilableKinds, k) {
			return fmt.Errorf("audit.auto_accept: %q is not an observation kind (one of %s)",
				k, strings.Join(CompilableKinds(), ", "))
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

// variantSets gathers, from every confirmed validWhen observation, which
// subject fields are valid under each gate value — keyed by (entity, gate
// field). A validConfiguration correction reads this to fill its per-value
// field sets, which no single validConfiguration observation carries.
func variantSets(obs []observe.Observation) map[[2]string]map[string][]string {
	out := map[[2]string]map[string][]string{}
	for _, o := range obs {
		if o.Kind != observe.KindValidWhen || o.Outcome != observe.OutcomeConfirmed || o.Condition == nil {
			continue
		}
		key := [2]string{o.Entity, o.Condition.Attribute}
		if out[key] == nil {
			out[key] = map[string][]string{}
		}
		value := literalSpelling(o.Condition.Equals)
		out[key][value] = append(out[key][value], o.Attribute)
	}
	return out
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

// writeAutoAccepted writes an auto-accepted correction into the accepted
// directory and reports the next free ordinal. The same observation
// recompiled — its value moved — overwrites its own file wherever it already
// sits, so no stale duplicate is left beside the new value and the ordinal is
// not consumed; a fresh observation lands at the collision-free ordinal it
// was handed, which the shared sequence has already advanced past every
// accepted file.
func writeAutoAccepted(correctionsDir string, next int, entity string, corr correction.Correction) (string, int, error) {
	if path, ok, err := autoFileFor(correctionsDir, corr.Evidence); err != nil {
		return "", next, err
	} else if ok {
		return path, next, writeCorrection(path, corr)
	}
	path := filepath.Join(correctionsDir, fmt.Sprintf("auto-%03d-%s%s", next, entity, correction.Suffix))
	return path, next + 1, writeCorrection(path, corr)
}

// autoFileFor finds an already-accepted auto- correction compiled from the
// same evidence — the same observation, recompiled — so a moved value
// overwrites in place rather than accreting a second file. Evidence is unique
// per observation, so at most one file matches.
func autoFileFor(correctionsDir, evidence string) (string, bool, error) {
	if evidence == "" {
		return "", false, nil
	}
	entries, err := os.ReadDir(correctionsDir)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	for _, e := range entries {
		if e.IsDir() || !autoName.MatchString(e.Name()) {
			continue
		}
		path := filepath.Join(correctionsDir, e.Name())
		raw, err := os.ReadFile(path) //nolint:gosec // enumerated from the corrections dir
		if err != nil {
			return "", false, err
		}
		var prev correction.Correction
		if json.Unmarshal(raw, &prev) == nil && prev.Evidence == evidence {
			return path, true, nil
		}
	}
	return "", false, nil
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
