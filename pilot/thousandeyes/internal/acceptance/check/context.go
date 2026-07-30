package check

import "context"

// testContext is the context a check runs under.
//
// terraform-plugin-testing's TestCheckFunc signature takes only a state, so there is no context
// to inherit and one has to be made. Deliberately not context.TODO(): the deadline belongs to
// the SDK call, which applies its own, and a check that timed out here would report a resource
// as absent when the request had merely been cut short.
func testContext() context.Context { return context.Background() }
