package generate

import (
	"fmt"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// This file is the single seam between the emitter and an SDK model's field
// access. Everything that reads or writes an SDK field goes through these
// three helpers, so supporting a new access style is a branch here rather
// than a hunt through every view builder.
//
// The zero AccessStyle means struct fields -- the resty dialect and every
// blueprint written before the field existed -- so existing documents and
// fixtures render byte-identically without naming a style.

// readExpr renders reading an SDK model field: "remote.Title" for a struct
// field, "remote.GetTitle()" for a method-access model.
func readExpr(style blueprint.AccessStyle, target, field string) string {
	if style == blueprint.AccessMethod {
		return target + ".Get" + field + "()"
	}
	return target + "." + field
}

// writeStmt renders one statement storing value into target's field:
// "body.Title = v" or "body.SetTitle(v)".
func writeStmt(style blueprint.AccessStyle, target, field, value string) string {
	if style == blueprint.AccessMethod {
		return target + ".Set" + field + "(" + value + ")"
	}
	return target + "." + field + " = " + value
}

// expandStmt renders the full expand statement for one attribute: the convert
// call applied to src, stored into target's field, with the temp-variable
// dance a fallible or wrapped conversion needs.
//
// A setter cannot receive a two-value call, so under method access every
// fallible conversion lands in a temp; under struct access the temp appears
// only when a wrapper (Deref, Cast) has to apply to the converted value --
// exactly the shapes expandAssignment always produced, which is what keeps
// the resty output byte-identical.
// sharedDiag, when non-nil, is set for the plain two-value form that assigns
// the enclosing scope's shared d -- the one form that needs `var d` declared
// outside a loop body; the temp forms declare their own with `:=`.
func expandStmt(
	style blueprint.AccessStyle,
	target, field string,
	call blueprint.ConvertCall,
	src, tmpBase string,
	needsDiags, sharedDiag *bool,
) string {
	if !call.ReturnsError {
		return writeStmt(style, target, field, convertExpr(call, src))
	}

	*needsDiags = true

	if needsTemp(call) || style == blueprint.AccessMethod {
		inner := call
		inner.Deref, inner.Cast = false, ""
		tmp := tmpBase + "Raw"
		return fmt.Sprintf(
			"%s, d := %s\ndiags.Append(d...)\n%s",
			tmp, convertExpr(inner, src),
			writeStmt(style, target, field, wrapConverted(call, tmp)))
	}

	if sharedDiag != nil {
		*sharedDiag = true
	}
	return fmt.Sprintf(
		"%s.%s, d = %s\ndiags.Append(d...)",
		target, field, convertExpr(call, src))
}
