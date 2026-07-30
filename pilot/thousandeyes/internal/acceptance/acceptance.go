// Package acceptance holds what every acceptance test needs before it can run: the provider
// under test, and the check that it is safe to run at all.
//
// It is hand-written and owned, like internal/client, because authentication and the decision to
// touch a live tenant are not things a generator should decide.
package acceptance

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	tfprovider "github.com/deploymenttheory/terraform-plugin-framework-codegen/pilot/thousandeyes/internal/provider"
)

// envBearerToken is the credential an acceptance run needs.
//
// Read from the environment and never from a flag: a flag lands in shell history and in the
// process table, where a token that cannot be revoked through the ThousandEyes API would stay
// valid until somebody deleted it by hand.
const envBearerToken = "THOUSANDEYES_BEARER_TOKEN"

// TestVersion is the version string the provider under test reports.
const TestVersion = "acc"

// ProtoV6ProviderFactories is what a resource.TestCase needs to serve the provider in process.
//
// One entry, named as the provider is named in HCL, so a generated `.tf` fixture needs no
// provider block of its own.
var ProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"thousandeyes": providerserver.NewProtocol6WithError(tfprovider.New(TestVersion)()),
}

// PreCheck skips or fails a test that cannot possibly pass.
//
// terraform-plugin-testing already requires TF_ACC, so this is only about credentials. Fatal
// rather than a skip: if somebody has set TF_ACC they have asked for a live run, and silently
// skipping would let a CI job report success having tested nothing.
func PreCheck(t *testing.T) {
	t.Helper()

	if os.Getenv(envBearerToken) == "" {
		t.Fatalf(
			"%s must be set for an acceptance run. It is read from the environment only -- "+
				"never pass it as a flag.",
			envBearerToken,
		)
	}
}
