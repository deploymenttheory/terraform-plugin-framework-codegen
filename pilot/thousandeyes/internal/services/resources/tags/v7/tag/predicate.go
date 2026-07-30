// This file is yours.
//
// It was scaffolded once, carries no generated marker, and `emit` will not write over it
// again. Deleting it means removing `hooks.readBackPredicate` from the blueprint too, because
// the generated read-after-write calls this function when that flag is set.
//
// # Why this is not generated
//
// The generated retry asks whether the object is *readable*, which every API answers the same
// way -- a 404 means not yet. That is enough for an API whose only inconsistency is a delay
// before the object appears.
//
// It is not enough for an API that returns a *stale* object rather than a 404. There the write
// has not landed until some particular field changes, and which field that is depends on the
// API and sometimes on the resource: a modification timestamp, a revision counter, a status
// moving out of "pending". No specification states it, so it cannot be generated -- 28 of the
// reference provider's 167 resources carry a file like this and the rest do not need one.

package tag

import (
	"context"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// readBackLanded reports whether a write has taken effect in the object just read.
//
// Returning false asks the retry loop to read again; returning true accepts the state as
// mapped. Returning true unconditionally -- as this scaffold does -- reproduces the behaviour
// you would get with no predicate at all, so it is a safe starting point.
//
// state is the model as just populated from the API. Compare it against whatever your API
// touches on write.
func readBackLanded(ctx context.Context, state *TagResourceModel) bool {
	tflog.Trace(ctx, "read-back predicate not implemented, accepting the first read", map[string]any{
		"resource": ResourceName,
	})

	return true
}
