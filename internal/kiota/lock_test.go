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
