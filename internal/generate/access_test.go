package generate

import (
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

func TestUnit_Generate_AccessExpressions(t *testing.T) {
	t.Parallel()

	if got := readExpr(blueprint.AccessStructField, "remote", "Title"); got != "remote.Title" {
		t.Errorf("struct read = %q", got)
	}
	// The zero style is struct fields: every blueprint written before the
	// field existed must render unchanged.
	if got := readExpr("", "remote", "Title"); got != "remote.Title" {
		t.Errorf("zero-style read = %q", got)
	}
	if got := readExpr(blueprint.AccessMethod, "remote", "Title"); got != "remote.GetTitle()" {
		t.Errorf("method read = %q", got)
	}

	if got := writeStmt(blueprint.AccessStructField, "body", "Title", "v"); got != "body.Title = v" {
		t.Errorf("struct write = %q", got)
	}
	if got := writeStmt(blueprint.AccessMethod, "body", "Title", "v"); got != "body.SetTitle(v)" {
		t.Errorf("method write = %q", got)
	}
}

func TestUnit_Generate_ExpandStmtShapes(t *testing.T) {
	t.Parallel()

	infallible := blueprint.ConvertCall{Func: "convert.FrameworkToPtrString"}
	fallible := blueprint.ConvertCall{Func: "convert.FrameworkToThing", ReturnsError: true}
	wrapped := blueprint.ConvertCall{Func: "convert.FrameworkToThing", ReturnsError: true, Deref: true}

	for name, tc := range map[string]struct {
		style blueprint.AccessStyle
		call  blueprint.ConvertCall
		want  string
		diags bool
	}{
		"struct infallible": {
			style: blueprint.AccessStructField, call: infallible,
			want: "body.Title = convert.FrameworkToPtrString(data.Title)",
		},
		"struct fallible assigns two values": {
			style: blueprint.AccessStructField, call: fallible,
			want:  "body.Title, d = convert.FrameworkToThing(data.Title)\ndiags.Append(d...)",
			diags: true,
		},
		"struct wrapped lands in a temp": {
			style: blueprint.AccessStructField, call: wrapped,
			want: "titleRaw, d := convert.FrameworkToThing(data.Title)\ndiags.Append(d...)\n" +
				"body.Title = convert.Deref(titleRaw)",
			diags: true,
		},
		"method infallible goes through the setter": {
			style: blueprint.AccessMethod, call: infallible,
			want: "body.SetTitle(convert.FrameworkToPtrString(data.Title))",
		},
		"method fallible always needs the temp": {
			style: blueprint.AccessMethod, call: fallible,
			want: "titleRaw, d := convert.FrameworkToThing(data.Title)\ndiags.Append(d...)\n" +
				"body.SetTitle(titleRaw)",
			diags: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var needsDiags bool
			got := expandStmt(tc.style, "body", "Title", tc.call, "data.Title", "title", &needsDiags, nil)
			if got != tc.want {
				t.Errorf("expandStmt =\n%s\nwant\n%s", got, tc.want)
			}
			if needsDiags != tc.diags {
				t.Errorf("needsDiags = %v, want %v", needsDiags, tc.diags)
			}
		})
	}
}
