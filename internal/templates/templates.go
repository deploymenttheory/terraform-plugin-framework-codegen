// Package templates holds the Go source templates the emitter renders.
//
// They are kept here, embedded in their own package rather than beside the
// generator, so the emitted shape can be reviewed and edited as ordinary text
// without reading the generator that drives it. That is the same reasoning the
// sibling SDK generator uses, and it is the reason these files contain no logic:
// every value they interpolate is a finished string precomputed in
// internal/generate, and the only conditionals are over presence.
//
// If a template needs to decide something, the decision belongs in render.
package templates

import "embed"

// FS holds the .tmpl files.
//
//go:embed *.tmpl
var FS embed.FS
