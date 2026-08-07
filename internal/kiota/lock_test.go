package kiota

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnit_Kiota_SetDescriptionLocationRewritesOneField proves the rewrite
// touches exactly the path field: a patched generation reads a temporary copy
// of the snapshot, and the temp path must not reach the committed lock.
func TestUnit_Kiota_SetDescriptionLocationRewritesOneField(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lock := `{
  "descriptionHash": "ABC",
  "descriptionLocation": "/tmp/tfpfgen-patched-spec-123/api.yaml",
  "kiotaVersion": "1.34.1"
}
`
	if err := os.WriteFile(filepath.Join(dir, LockFileName), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetDescriptionLocation(dir, "../../openapi/thousandeyes/7.0.98/api.yaml"); err != nil {
		t.Fatalf("SetDescriptionLocation: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, `"descriptionLocation": "../../openapi/thousandeyes/7.0.98/api.yaml"`) {
		t.Errorf("the location was not rewritten:\n%s", got)
	}
	if !strings.Contains(got, `"descriptionHash": "ABC"`) || !strings.Contains(got, `"kiotaVersion": "1.34.1"`) {
		t.Errorf("other fields must survive byte-for-byte:\n%s", got)
	}
}

func TestUnit_Kiota_SetDescriptionLocationRefusesALockWithoutOne(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, LockFileName), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetDescriptionLocation(dir, "x"); err == nil {
		t.Error("a lock with no descriptionLocation must refuse the rewrite")
	}
}

// TestUnit_Kiota_ReadLockCarriesGenerationSettings proves the lock yields the
// settings a tree was generated with, not just its version. A regeneration that
// retypes these from memory produces a different SDK and calls it drift: the
// Jamf pilot's client type silently became ApiClient that way, because the
// generator's flag default disagreed with what was committed.
func TestUnit_Kiota_ReadLockCarriesGenerationSettings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lock := `{
  "descriptionHash": "ABC",
  "descriptionLocation": "../../openapi/example/snap/api.yaml",
  "kiotaVersion": "1.34.1",
  "clientClassName": "JamfProClient",
  "includePatterns": [],
  "excludePatterns": ["/endpoint/tests/dynamic-tests/**", "/endpoint/agents/transfer"]
}`
	if err := os.WriteFile(filepath.Join(dir, LockFileName), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}

	got, had, err := ReadLock(dir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if !had {
		t.Fatal("ReadLock reported no lock, but one was written")
	}

	if got.ClientClassName != "JamfProClient" {
		t.Errorf("client class name = %q, want JamfProClient", got.ClientClassName)
	}
	if len(got.IncludePatterns) != 0 {
		t.Errorf("include patterns = %v, want none", got.IncludePatterns)
	}
	if len(got.ExcludePatterns) != 2 {
		t.Fatalf("exclude patterns = %v, want 2", got.ExcludePatterns)
	}
	if got.ExcludePatterns[0] != "/endpoint/tests/dynamic-tests/**" {
		t.Errorf("first exclude = %q", got.ExcludePatterns[0])
	}
}

// TestUnit_Kiota_ReadLockWithoutSettingsLeavesThemEmpty proves an older lock,
// written before these fields were read, yields no settings rather than an
// error -- adoption then has nothing to adopt and the caller's flags stand.
func TestUnit_Kiota_ReadLockWithoutSettingsLeavesThemEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lock := `{"descriptionHash":"ABC","kiotaVersion":"1.34.1"}`
	if err := os.WriteFile(filepath.Join(dir, LockFileName), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}

	got, had, err := ReadLock(dir)
	if err != nil || !had {
		t.Fatalf("ReadLock: %v (had=%v)", err, had)
	}
	if got.ClientClassName != "" || got.ExcludePatterns != nil || got.IncludePatterns != nil {
		t.Errorf("expected no settings, got %+v", got)
	}
}
