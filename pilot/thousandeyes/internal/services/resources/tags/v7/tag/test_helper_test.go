package tag

import (
	"context"

	"github.com/hashicorp/terraform-plugin-testing/terraform"

	te "github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/pilot/thousandeyes/internal/acceptance/exists"
)

// TagTestResource answers "is this tag really in the API" for an acceptance test.
//
// In a _test.go file rather than an ordinary one, which is the one deliberate departure from the
// reference provider: ms365 puts its equivalent in the package proper, so its test scaffolding is
// compiled into the shipped provider binary. Declared here, package tag_test can still reach it
// -- an external test package sees the exported identifiers of the package's own test files --
// and nothing test-only reaches a release build.
type TagTestResource struct{}

// Exists reads the tag back through the same operation the resource's Read uses.
//
// Deliberately the same call, not an equivalent one: a check that queried a list endpoint could
// pass while the read the provider actually depends on was broken.
func (TagTestResource) Exists(
	ctx context.Context,
	state *terraform.InstanceState,
) (*bool, error) {
	return exists.Check(ctx, state, func(
		ctx context.Context,
		client *te.Client,
		state *terraform.InstanceState,
	) error {
		_, _, err := client.API.Tags.GetTag(ctx, state.ID)

		return err
	})
}
