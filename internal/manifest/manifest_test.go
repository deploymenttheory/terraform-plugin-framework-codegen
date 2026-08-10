package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func entries() []Entry {
	return []Entry{
		{Path: "internal/services/resources/b/state.go", SHA256: "bbb", Source: "entity:b"},
		{Path: "internal/services/resources/a/crud.go", SHA256: "aaa", Source: "entity:a"},
		{Path: "internal/sdk/client.go", SHA256: "sss", Origin: OriginSDK},
		{Path: "spec/corrections/001-a.correction.json", Authored: true},
		{Path: "audit/inputs.json", Authored: true},
	}
}

func TestUnit_Manifest_NewSortsByPath(t *testing.T) {
	m := New("v0.1.0", entries())
	for i := 1; i < len(m.Files); i++ {
		if m.Files[i-1].Path >= m.Files[i].Path {
			t.Fatalf("entries are not sorted: %q before %q", m.Files[i-1].Path, m.Files[i].Path)
		}
	}
	if m.FormatVersion != FormatVersion || m.ToolVersion != "v0.1.0" {
		t.Fatalf("header = %q/%q", m.FormatVersion, m.ToolVersion)
	}
}

func TestUnit_Manifest_MarshalIsDeterministic(t *testing.T) {
	a, err := Marshal(New("v0.1.0", entries()))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(New("v0.1.0", entries()))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("two marshals of the same manifest differ")
	}
	if !strings.HasSuffix(string(a), "\n") {
		t.Fatal("marshalled manifest has no trailing newline")
	}
}

func TestUnit_Manifest_AuthoredEntriesCarryNoDigest(t *testing.T) {
	data, err := Marshal(New("v0.1.0", entries()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"authored": true, "sha256"`) {
		t.Fatal("an authored entry serialized a digest")
	}
	m := New("v0.1.0", entries())
	authored := m.AuthoredPaths()
	if len(authored) != 2 || !authored["audit/inputs.json"] || !authored["spec/corrections/001-a.correction.json"] {
		t.Fatalf("AuthoredPaths = %v", authored)
	}
}

func TestUnit_Manifest_RefusesWritesToAuthoredPaths(t *testing.T) {
	m := New("v0.1.0", entries())
	refused := m.RefusesWrites([]string{
		"internal/services/resources/a/crud.go", // generated: fine
		"audit/inputs.json",                     // authored: refused
		"spec/corrections/001-a.correction.json",
	})
	if len(refused) != 2 || refused[0] != "audit/inputs.json" {
		t.Fatalf("RefusesWrites = %v, want both authored paths, sorted", refused)
	}
	if got := m.RefusesWrites([]string{"internal/sdk/client.go"}); got != nil {
		t.Fatalf("a generated path was refused: %v", got)
	}
}

func TestUnit_Manifest_EntriesNotOfKeepsOthersAndAuthored(t *testing.T) {
	m := New("v0.1.0", entries())

	// A provider-generate rewrite (origin "") keeps the sdk entry and both
	// authored entries.
	kept := m.EntriesNotOf("")
	if len(kept) != 3 {
		t.Fatalf("EntriesNotOf(provider) kept %d entries, want 3: %+v", len(kept), kept)
	}

	// An sdk-generate rewrite keeps everything provider generate and humans
	// recorded.
	kept = m.EntriesNotOf(OriginSDK)
	if len(kept) != 4 {
		t.Fatalf("EntriesNotOf(sdk) kept %d entries, want 4: %+v", len(kept), kept)
	}
}

func TestUnit_Manifest_SaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, New("v0.1.0", entries())); err != nil {
		t.Fatal(err)
	}

	m, ok, err := Load(root)
	if err != nil || !ok {
		t.Fatalf("Load = %v, %v", ok, err)
	}
	if len(m.Files) != len(entries()) || m.ToolVersion != "v0.1.0" {
		t.Fatalf("round trip lost data: %+v", m)
	}
}

func TestUnit_Manifest_LoadDistinguishesAbsentFromBroken(t *testing.T) {
	_, ok, err := Load(t.TempDir())
	if err != nil || ok {
		t.Fatalf("an absent manifest is not an error: %v, %v", ok, err)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, Name), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(root); err == nil {
		t.Fatal("a broken manifest loaded cleanly")
	}

	if err := os.WriteFile(filepath.Join(root, Name), []byte(`{"formatVersion":"99"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(root); err == nil || !strings.Contains(err.Error(), "unsupported manifest format") {
		t.Fatalf("a future format version must refuse, got: %v", err)
	}
}

func TestUnit_Manifest_OrphansOfFindsOnlyItsOwnLeftovers(t *testing.T) {
	root := t.TempDir()

	// Three files on disk: one still produced, one orphaned, one belonging
	// to the other verb.
	for _, p := range []string{"kept.go", "orphan.go", "sdk.go"} {
		if err := os.WriteFile(filepath.Join(root, p), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := New("v0.1.0", []Entry{
		{Path: "kept.go", SHA256: "k"},
		{Path: "orphan.go", SHA256: "o"},
		{Path: "deleted.go", SHA256: "d"}, // recorded, not on disk: not an orphan
		{Path: "sdk.go", SHA256: "s", Origin: OriginSDK},
		{Path: "audit/inputs.json", Authored: true}, // authored: never an orphan
	})

	orphans, err := m.OrphansOf(root, "", map[string]bool{"kept.go": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0] != "orphan.go" {
		t.Fatalf("orphans = %v, want exactly orphan.go", orphans)
	}

	orphans, err = m.OrphansOf(root, OriginSDK, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0] != "sdk.go" {
		t.Fatalf("sdk orphans = %v, want exactly sdk.go", orphans)
	}
}
