// Package schema holds schema fragments shared by every generated resource.
//
// A generated schema calls into here rather than repeating the fragment, so that
// changing the timeouts block, for instance, is one edit rather than one per
// resource.
package schema

import (
	"context"

	timeoutsds "github.com/hashicorp/terraform-plugin-framework-timeouts/datasource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

// ResourceTimeouts returns the timeouts block for a resource with all four
// operations.
func ResourceTimeouts(ctx context.Context) schema.Block {
	return timeouts.Block(ctx, timeouts.Opts{
		Create: true,
		Read:   true,
		Update: true,
		Delete: true,
	})
}

// DataSourceTimeouts returns the timeouts block for a data source, which only
// reads.
func DataSourceTimeouts(ctx context.Context) dsschema.Block {
	return timeoutsds.Block(ctx)
}
