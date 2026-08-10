package observe

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sample is an unsorted, multi-entity observation set with empty IDs, as
// an executor would hand to Write.
func sample() []Observation {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	closed := true
	return []Observation{
		{
			Entity: "tag", Attribute: "color", Kind: KindValues,
			Value:   Values{Accepted: []string{"red", "blue"}, Rejected: []string{"mauve"}, Closed: &closed},
			Outcome: OutcomeConfirmed, RunID: "r1", SpecHash: "h1", ObservedAt: at,
		},
		{
			Entity: "tag", Kind: KindUpdateStyle, Value: "patch-merge",
			Outcome: OutcomeConfirmed, RunID: "r1", SpecHash: "h1", ObservedAt: at,
			Excerpts: []Excerpt{{
				Method: "PATCH", PathTemplate: "/tags/{tagId}", Status: 200,
				RequestFragment:  []byte(`{"color":"blue"}`),
				ResponseFragment: []byte(`{"color":"blue","name":"kept"}`),
			}},
		},
		{
			Entity: "project", Attribute: "name", Kind: KindWritable, Value: true,
			Outcome: OutcomeConfirmed, RunID: "r1", SpecHash: "h1", ObservedAt: at,
		},
		{
			Entity: "tag", Attribute: "query", Kind: KindRequiredWhen, Value: true,
			Condition: &Condition{Attribute: "type", Equals: "dynamic"},
			Outcome:   OutcomeConfirmed, RunID: "r1", SpecHash: "h1", ObservedAt: at,
		},
		{
			Entity: "tag", Attribute: "updated_at", Kind: KindVolatile,
			Outcome: OutcomeInconclusive, RunID: "r1", SpecHash: "h1", ObservedAt: at,
		},
	}
}

func TestUnit_Observe_WriteIsDeterministicAndPerEntity(t *testing.T) {
	dir1, dir2 := filepath.Join(t.TempDir(), "obs1"), filepath.Join(t.TempDir(), "obs2")
	if err := Write(dir1, sample()); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// One file per entity, named for it.
	for _, name := range []string{"project" + FileSuffix, "tag" + FileSuffix} {
		if _, err := os.Stat(filepath.Join(dir1, name)); err != nil {
			t.Errorf("expected %s: %v", name, err)
		}
	}

	// Reversing the input order must not move a byte.
	reversed := sample()
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	if err := Write(dir2, reversed); err != nil {
		t.Fatalf("Write reversed: %v", err)
	}
	for _, name := range []string{"project" + FileSuffix, "tag" + FileSuffix} {
		a, _ := os.ReadFile(filepath.Join(dir1, name))
		b, _ := os.ReadFile(filepath.Join(dir2, name))
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs between input orders", name)
		}
		if !bytes.HasSuffix(a, []byte("\n")) {
			t.Errorf("%s does not end in a newline", name)
		}
	}
}

func TestUnit_Observe_WriteFillsIDsAndRefusesDriftedOnes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "obs")
	if err := Write(dir, sample()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, o := range got {
		if want := ComputeID(o.Entity, o.Attribute, o.Kind, o.Condition); o.ID != want {
			t.Errorf("%s.%s: id %q, want %q", o.Entity, o.Attribute, o.ID, want)
		}
	}

	bad := sample()[:1]
	bad[0].ID = "1234567890abcdef"
	if err := Write(filepath.Join(t.TempDir(), "obs"), bad); err == nil ||
		!strings.Contains(err.Error(), "does not match the computed") {
		t.Fatalf("Write with a drifted id = %v, want a mismatch error", err)
	}
}

func TestUnit_Observe_RoundTripIsAFixedPoint(t *testing.T) {
	dir1 := filepath.Join(t.TempDir(), "obs")
	if err := Write(dir1, sample()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	loaded, err := Read(dir1)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(loaded) != len(sample()) {
		t.Fatalf("Read returned %d observations, want %d", len(loaded), len(sample()))
	}

	dir2 := filepath.Join(t.TempDir(), "obs")
	if err := Write(dir2, loaded); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	for _, name := range []string{"project" + FileSuffix, "tag" + FileSuffix} {
		a, _ := os.ReadFile(filepath.Join(dir1, name))
		b, _ := os.ReadFile(filepath.Join(dir2, name))
		if !bytes.Equal(a, b) {
			t.Errorf("%s changed across a read/write round trip", name)
		}
	}
}

func TestUnit_Observe_ReadRefusesWhatItCannotTrust(t *testing.T) {
	write := func(t *testing.T, name, content string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	id := ComputeID("tag", "", KindDeleteNotFoundOK, nil)

	cases := []struct {
		name    string
		file    string
		content string
		want    string
	}{
		{
			"unknown key", "tag" + FileSuffix,
			`{"entity":"tag","observations":[],"extra":1}`,
			"unknown field",
		},
		{
			"filename and entity disagree", "colour" + FileSuffix,
			`{"entity":"tag","observations":[]}`,
			"belongs in tag" + FileSuffix,
		},
		{
			"observation about another entity", "tag" + FileSuffix,
			`{"entity":"tag","observations":[{"id":"` + id + `","entity":"project","kind":"deleteNotFoundOK","value":true,"outcome":"confirmed","runId":"r","specHash":"h","observedAt":"2026-01-01T00:00:00Z"}]}`,
			`about entity "project"`,
		},
		{
			"observation with no id", "tag" + FileSuffix,
			`{"entity":"tag","observations":[{"entity":"tag","kind":"deleteNotFoundOK","value":true,"outcome":"confirmed","runId":"r","specHash":"h","observedAt":"2026-01-01T00:00:00Z"}]}`,
			"no id",
		},
		{
			"hand-edited id", "tag" + FileSuffix,
			`{"entity":"tag","observations":[{"id":"beefbeefbeefbeef","entity":"tag","kind":"deleteNotFoundOK","value":true,"outcome":"confirmed","runId":"r","specHash":"h","observedAt":"2026-01-01T00:00:00Z"}]}`,
			"does not match the computed",
		},
		{
			"not JSON at all", "tag" + FileSuffix,
			`observations: []`,
			"invalid character",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := write(t, tc.file, tc.content)
			if _, err := Read(dir); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Read = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestUnit_Observe_ReadTreatsAbsenceAsEmpty(t *testing.T) {
	obs, err := Read(filepath.Join(t.TempDir(), "never-written"))
	if err != nil || obs != nil {
		t.Fatalf("Read(missing dir) = %v, %v; want nil, nil", obs, err)
	}

	// Files without the suffix, and directories, are not observations.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "nested"+FileSuffix), 0o755); err != nil {
		t.Fatal(err)
	}
	obs, err = Read(dir)
	if err != nil || len(obs) != 0 {
		t.Fatalf("Read(dir with strangers) = %v, %v; want empty, nil", obs, err)
	}

	// An invalid observation inside an otherwise well-formed file is
	// refused by Validate on read.
	raw := `{"entity":"tag","observations":[{"id":"` +
		ComputeID("tag", "", "sideEffect", nil) +
		`","entity":"tag","kind":"sideEffect","value":true,"outcome":"confirmed","runId":"r","specHash":"h","observedAt":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(dir, "tag"+FileSuffix), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir); err == nil || !strings.Contains(err.Error(), "unknown observation kind") {
		t.Fatalf("Read = %v, want an unknown-kind error", err)
	}
}

func TestUnit_Observe_WriteRefusesAnInvalidObservation(t *testing.T) {
	bad := sample()[:1]
	bad[0].Kind = "sideEffect"
	if err := Write(filepath.Join(t.TempDir(), "obs"), bad); err == nil ||
		!strings.Contains(err.Error(), "unknown observation kind") {
		t.Fatalf("Write = %v, want an unknown-kind error", err)
	}
}
