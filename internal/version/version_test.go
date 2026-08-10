package version

import "testing"

func TestUnit_Version_UnstampedBuildReportsDev(t *testing.T) {
	if got := Version(); got != "dev" {
		t.Fatalf("Version() = %q, want %q on an unstamped build", got, "dev")
	}
}
