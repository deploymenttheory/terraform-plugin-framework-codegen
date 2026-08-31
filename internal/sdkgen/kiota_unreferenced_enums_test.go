package sdkgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// orphanedEnumSource is the shape kiota leaves behind when it drops a
// request-body property: the enum type and its companions, referenced by
// nothing.
const orphanedEnumSource = `package teams

type TeamsPostRequestBody_privacy int

const (
    SECRET_TEAMSPOSTREQUESTBODY_PRIVACY TeamsPostRequestBody_privacy = iota
    CLOSED_TEAMSPOSTREQUESTBODY_PRIVACY
)

func ParseTeamsPostRequestBody_privacy(v string) (any, error) {
    return nil, nil
}
`

const bodyWithoutTheProperty = `package teams

type TeamsPostRequestBody struct {
    name *string
}
`

func writeSDKFile(t *testing.T, dir, name, source string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUnit_Sdkgen_AnUnreferencedRequestBodyEnumRefusesTheSDK(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSDKFile(t, dir, "teams/teams_post_request_body.go", bodyWithoutTheProperty)
	writeSDKFile(t, dir, "teams/teams_post_request_body_escaped_privacy.go", orphanedEnumSource)

	err := refuseUnreferencedRequestBodyEnums(dir)
	if err == nil {
		t.Fatal("an SDK with a dropped request-body property was accepted")
	}
	for _, want := range []string{"kiota dropped 1 request-body property", "TeamsPostRequestBody_privacy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

func TestUnit_Sdkgen_ACarriedRequestBodyEnumIsAccepted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	carryingBody := `package teams

type TeamsPostRequestBody struct {
    privacy *TeamsPostRequestBody_privacy
}
`
	writeSDKFile(t, dir, "teams/teams_post_request_body.go", carryingBody)
	writeSDKFile(t, dir, "teams/teams_post_request_body_escaped_privacy.go", orphanedEnumSource)

	if err := refuseUnreferencedRequestBodyEnums(dir); err != nil {
		t.Fatalf("a referenced enum was refused: %v", err)
	}
}

func TestUnit_Sdkgen_AnEnumOutsideARequestBodyFileIsNotConsidered(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A query-parameter enum lives in its own file and is referenced by its
	// request builder; one that is not is still no request-body drop.
	queryEnum := `package teams

type GetSortQueryParameterType int

func ParseGetSortQueryParameterType(v string) (any, error) {
    return nil, nil
}
`
	writeSDKFile(t, dir, "teams/get_sort_query_parameter_type.go", queryEnum)

	if err := refuseUnreferencedRequestBodyEnums(dir); err != nil {
		t.Fatalf("an enum outside a request-body file was refused: %v", err)
	}
}
