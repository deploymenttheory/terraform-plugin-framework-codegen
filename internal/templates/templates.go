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

// ShellFS holds the provider shell templates -- the support packages generated
// code calls into, promoted from the kiota pilot's proven tree. Each template's
// path under shell/ mirrors the path it is emitted to, with .tmpl appended, so
// finding the template for an emitted file needs no lookup table.
//
// A separate FS rather than more patterns in FS, because the shell tree
// contains several base names more than once (crud/errors.go.tmpl and
// errors/errors.go.tmpl); parsed into one template set, the later would
// silently shadow the former.
//
//go:embed shell
var ShellFS embed.FS
