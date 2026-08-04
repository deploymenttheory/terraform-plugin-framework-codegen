package cassette

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// The on-disk layout mirrors internal/specstore:
//
//	probe-evidence/<provider>/<resource>/<version>-t<epochMillis>/
//	  metadata.json
//	  interactions/001-get-tags.json … NNN-*.json
//
// Copied rather than generalised from specstore. Two callers do not justify an
// abstraction, specstore's doc comment is specifically about specifications, and the
// duplicated part is about 120 lines of directory handling -- against which the cost of a
// shared abstraction is that neither caller can change its layout without considering the
// other.
//
// One interaction per file, not one array. A re-record that changes a single response
// yields a one-file diff, and the numbered prefix makes ordering explicit rather than
// positional -- so a reviewer can see that interaction 004 changed without reading 003
// and 005 to work out where they are.

const (
	// MetadataFileName holds the snapshot's own record.
	MetadataFileName = "metadata.json"
	// InteractionsDirName holds the numbered interaction files.
	InteractionsDirName = "interactions"
	// FactsFileName holds the facts derived from this snapshot.
	FactsFileName = "facts.json"
	// ReportFileName holds the run report.
	ReportFileName = "report.json"
	// PlanFileName holds the probe plan the recording was made with. Absent for a read-only
	// recording, which needs no plan.
	PlanFileName = "plan.json"
	// SubjectFileName holds the flattened subject the recording was made with. Absent
	// only for snapshots that predate freezing.
	SubjectFileName = "subject.json"
	// RehearsalFileName holds the derived wire bodies the rehearsal probe sent, one
	// entry per fixpoint round. Absent for recordings that predate the probe.
	RehearsalFileName = "rehearsal.json"
)

// ErrNoSnapshot is returned when a directory holds no cassette.
var ErrNoSnapshot = errors.New("no cassette snapshot found")

// ErrChecksumMismatch marks a snapshot whose interactions no longer match its metadata.
var ErrChecksumMismatch = errors.New("cassette checksum does not match its metadata")

// dirPattern matches "<version>-t<epochMillis>", the same shape specstore uses.
var dirPattern = regexp.MustCompile(`^(.+)-t(\d+)$`)

// Snapshot is one recorded session on disk.
type Snapshot struct {
	// Dir is the snapshot directory.
	Dir string
	// Version is the API version the recording was made against.
	Version string
	// Millis is the epoch-milliseconds encoded in the directory name.
	//
	// Ordering uses this rather than mtime, deliberately: a git checkout does not
	// preserve modification times, so ordering by mtime would give a different answer in
	// CI than locally -- and "the latest snapshot" would then mean different things in
	// the two places.
	Millis int64
}

// Metadata is the snapshot's own record.
//
// No timestamp field beyond the one in the directory name, and no tool version: the
// directory is committed and diffed, so a value that changed without an input changing
// would make the drift check useless. Same rule as internal/blueprint and internal/emit.
type Metadata struct {
	// Provider and Resource identify what was probed.
	Provider string `json:"provider"`
	Resource string `json:"resource"`
	// APIVersion is what the profile's endpoint reported, where known.
	APIVersion string `json:"apiVersion,omitempty"`
	// Host is the API host. Not a secret, and the one thing a reader needs in order to
	// know what these interactions describe.
	Host string `json:"host"`
	// BasePath is the path prefix the endpoint carried, e.g. "/v7".
	//
	// Recorded because an interaction stores the *full* request path while a probe asks for one
	// relative to the base URL. Without this, replay builds "/tags" where the cassette holds
	// "/v7/tags" and every request mismatches -- which is exactly what happened the first time
	// this ran against a real API, and which no httptest-backed test can reproduce because an
	// httptest base URL has no path.
	BasePath string `json:"basePath,omitempty"`
	// NamePrefix is the prefix every object a mutating run created carried in its name field.
	//
	// Recorded for the same reason as BasePath, and with the same failure mode if it is not:
	// ReplayTransport matches request bodies, the stamped name is *in* the body, and a replay
	// that regenerated the prefix from anything but this would mismatch on every single create.
	NamePrefix string `json:"namePrefix,omitempty"`
	// Only is the probe filter the recording ran under, empty for a full run.
	//
	// Recorded because replay executes the catalogue: a rehearse-only cassette holds
	// no read-tier traffic, and a full replay against it mismatches on the first GET
	// the read tier issues. Replay narrows itself to what was actually recorded.
	Only string `json:"only,omitempty"`
	// Interactions counts the files, so a truncated directory is detectable without
	// reading them all.
	Interactions int `json:"interactions"`
	// SHA256 covers the canonical concatenation of every interaction file.
	SHA256 string `json:"sha256"`
	// ProbeVersion pins the derivation logic. A cassette replayed against a different
	// probe version may legitimately produce different facts, and that has to be a hard,
	// explanatory error rather than a silent difference.
	ProbeVersion string `json:"probeVersion"`
}

// DirName builds a snapshot directory name.
func DirName(version string, at time.Time) string {
	if version == "" {
		version = "unknown"
	}
	return fmt.Sprintf("%s-t%d", version, at.UnixMilli())
}

// InteractionsDir is where the numbered files live.
func (s Snapshot) InteractionsDir() string { return filepath.Join(s.Dir, InteractionsDirName) }

// MetadataPath is the metadata file.
func (s Snapshot) MetadataPath() string { return filepath.Join(s.Dir, MetadataFileName) }

// FactsPath is the derived facts.
func (s Snapshot) FactsPath() string { return filepath.Join(s.Dir, FactsFileName) }

// ReportPath is the run report.
func (s Snapshot) ReportPath() string { return filepath.Join(s.Dir, ReportFileName) }

// PlanPath is the frozen copy of the probe plan the recording was made with.
//
// Frozen into the snapshot rather than read from the working tree at replay time, and the reason is
// specific to the write tier. Facts are derived from the transcript, but *which requests exist in
// the transcript* is a function of the plan: its fixtures are the request bodies. Editing a fixture
// would then make replay fail with a body mismatch that looks exactly like a probe regression and
// is nothing of the kind.
func (s Snapshot) PlanPath() string { return filepath.Join(s.Dir, PlanFileName) }

// SubjectPath is the frozen copy of the flattened subject the recording was made with.
//
// Frozen for the same reason the plan is, and its absence caused a real failure. Body
// synthesis reads the subject -- which fields are writable, their documented values,
// their recorded behaviour -- so replay building its subject from the working tree
// diverges the moment curation moves the blueprint. The committed case: the re-record's
// own merge made `type` writable, and every full-body probe then replayed with one more
// key than the transcript held, reporting seven phantom orphans.
func (s Snapshot) SubjectPath() string { return filepath.Join(s.Dir, SubjectFileName) }

// RehearsalPath is the frozen copy of the bodies the rehearsal probe derived and sent.
//
// Frozen for the same reason the plan and subject are: the bodies are derived from the
// blueprint, and the very merge that follows a record run moves the blueprint. Replay
// re-deriving from the working tree would fail with a body mismatch on the first
// curation pass after the recording.
func (s Snapshot) RehearsalPath() string { return filepath.Join(s.Dir, RehearsalFileName) }

// List returns every snapshot under root, oldest first.
func List(root string) ([]Snapshot, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", root, err)
	}

	var out []Snapshot

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		m := dirPattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}

		millis, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			// Unreachable: the pattern only matches digits. Skipped rather than
			// returned, because one oddly named directory should not make every other
			// snapshot unreadable.
			continue
		}

		out = append(out, Snapshot{
			Dir:     filepath.Join(root, e.Name()),
			Version: m[1],
			Millis:  millis,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Millis < out[j].Millis })

	return out, nil
}

// Latest returns the newest snapshot under root.
func Latest(root string) (Snapshot, error) {
	all, err := List(root)
	if err != nil {
		return Snapshot{}, err
	}
	if len(all) == 0 {
		return Snapshot{}, fmt.Errorf("%w under %s", ErrNoSnapshot, root)
	}

	return all[len(all)-1], nil
}

// Find returns the snapshot with a given directory name.
func Find(root, name string) (Snapshot, error) {
	all, err := List(root)
	if err != nil {
		return Snapshot{}, err
	}

	for _, s := range all {
		if filepath.Base(s.Dir) == name {
			return s, nil
		}
	}

	return Snapshot{}, fmt.Errorf("%w: %s under %s", ErrNoSnapshot, name, root)
}

// Write creates a snapshot directory and writes its interactions and metadata.
//
// The fail-closed order is the important part: every interaction is scanned before
// anything is written, and a finding writes nothing at all -- not a partial directory,
// not a single file. So a leak cannot be committed; it can only fail the build.
func Write(
	root string,
	meta Metadata,
	interactions []Interaction,
	secrets map[string]string,
	at time.Time,
) (Snapshot, error) {
	if len(interactions) == 0 {
		return Snapshot{}, fmt.Errorf(
			"%w: refusing to write a snapshot with no interactions",
			ErrInvalidCassette,
		)
	}

	// Scan first, write second. Nothing below this point can leak.
	var findings []Leak
	for _, i := range interactions {
		found, err := ScanInteraction(i, secrets)
		if err != nil {
			return Snapshot{}, err
		}
		findings = append(findings, found...)
	}
	if len(findings) > 0 {
		return Snapshot{}, LeaksError(findings)
	}

	encoded := make(map[string][]byte, len(interactions))
	for _, i := range interactions {
		data, err := marshalCanonical(i)
		if err != nil {
			return Snapshot{}, err
		}
		encoded[i.ID+".json"] = data
	}

	meta.Interactions = len(interactions)
	meta.SHA256 = checksumOf(encoded)

	snap := Snapshot{
		Dir:     filepath.Join(root, DirName(meta.APIVersion, at)),
		Version: meta.APIVersion,
		Millis:  at.UnixMilli(),
	}

	if err := os.MkdirAll(snap.InteractionsDir(), 0o750); err != nil {
		return Snapshot{}, fmt.Errorf("creating %s: %w", snap.InteractionsDir(), err)
	}

	names := make([]string, 0, len(encoded))
	for name := range encoded {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		path := filepath.Join(snap.InteractionsDir(), name)
		if err := os.WriteFile(path, encoded[name], 0o600); err != nil {
			return Snapshot{}, fmt.Errorf("writing %s: %w", path, err)
		}
	}

	metaBytes, err := marshalCanonical(meta)
	if err != nil {
		return Snapshot{}, err
	}
	if err := os.WriteFile(snap.MetadataPath(), metaBytes, 0o600); err != nil {
		return Snapshot{}, fmt.Errorf("writing %s: %w", snap.MetadataPath(), err)
	}

	return snap, nil
}

// LoadMetadata reads a snapshot's metadata.
func (s Snapshot) LoadMetadata() (Metadata, error) {
	data, err := os.ReadFile(s.MetadataPath()) //nolint:gosec // operator-supplied path by design
	if err != nil {
		return Metadata{}, fmt.Errorf("reading %s: %w", s.MetadataPath(), err)
	}

	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return Metadata{}, fmt.Errorf(
			"%w: parsing %s: %w",
			ErrInvalidCassette,
			s.MetadataPath(),
			err,
		)
	}

	return m, nil
}

// LoadInteractions reads a snapshot's interactions in sequence order.
//
// Ordered by the Seq field rather than by filename, and then checked for gaps. Filename
// order and sequence order agree today because the id is built from the sequence, but
// relying on that would make a hand-edited cassette replay in a subtly wrong order rather
// than failing.
func (s Snapshot) LoadInteractions() ([]Interaction, error) {
	entries, err := os.ReadDir(s.InteractionsDir())
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.InteractionsDir(), err)
	}

	var out []Interaction

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}

		path := filepath.Join(s.InteractionsDir(), e.Name())

		data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path by design
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}

		var i Interaction
		if err := json.Unmarshal(data, &i); err != nil {
			return nil, fmt.Errorf("%w: parsing %s: %w", ErrInvalidCassette, path, err)
		}

		out = append(out, i)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s holds no interactions", ErrNoSnapshot, s.InteractionsDir())
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })

	for idx, i := range out {
		if i.Seq != idx+1 {
			return nil, fmt.Errorf("%w: interaction sequence jumps from %d to %d; "+
				"a cassette with a gap cannot be replayed in order",
				ErrInvalidCassette, idx, i.Seq)
		}
	}

	return out, nil
}

// Verify checks a snapshot's interactions against its recorded checksum.
func (s Snapshot) Verify() error {
	meta, err := s.LoadMetadata()
	if err != nil {
		return err
	}

	interactions, err := s.LoadInteractions()
	if err != nil {
		return err
	}

	if len(interactions) != meta.Interactions {
		return fmt.Errorf("%w: metadata records %d interaction(s), found %d",
			ErrChecksumMismatch, meta.Interactions, len(interactions))
	}

	encoded := make(map[string][]byte, len(interactions))
	for _, i := range interactions {
		data, err := marshalCanonical(i)
		if err != nil {
			return err
		}
		encoded[i.ID+".json"] = data
	}

	if got := checksumOf(encoded); got != meta.SHA256 {
		return fmt.Errorf("%w: %s (metadata says %s)",
			ErrChecksumMismatch, short(got), short(meta.SHA256))
	}

	return nil
}

// checksumOf hashes the interaction files in a fixed order.
//
// Over the canonical encodings rather than the bytes on disk, so the checksum is a
// property of the content and not of how a filesystem happened to store it.
func checksumOf(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	h := sha256.New()
	for _, name := range names {
		h.Write([]byte(name))
		h.Write(files[name])
	}

	return hex.EncodeToString(h.Sum(nil))
}

// marshalCanonical encodes as two-space-indented JSON with a trailing newline and no HTML
// escaping, matching blueprint.Marshal.
func marshalCanonical(v any) ([]byte, error) {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)

	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encoding a cassette file: %w", err)
	}

	return buf.Bytes(), nil
}

func short(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}
