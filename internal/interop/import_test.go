package interop

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/terraform-plugin-codegen-spec/code"
	"github.com/hashicorp/terraform-plugin-codegen-spec/datasource"
	"github.com/hashicorp/terraform-plugin-codegen-spec/provider"
	"github.com/hashicorp/terraform-plugin-codegen-spec/resource"
	"github.com/hashicorp/terraform-plugin-codegen-spec/schema"
	"github.com/hashicorp/terraform-plugin-codegen-spec/spec"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

func importOpts() Options {
	return Options{
		Provider:      "example",
		TypePrefix:    "example",
		GoModule:      "example.com/terraform-provider-example",
		APIVersionDir: "v1",
		ServiceGroup:  "things",
	}
}

// TestUnit_Interop_Roundtrip asserts the schema slice survives export and import
// unchanged.
//
// Only the schema slice: the round trip is lossy by construction, and asserting
// otherwise would be asserting something false. Everything the official format
// cannot carry is zeroed before the comparison, and the list of what gets zeroed is
// itself the interesting part -- it is the same list the downgrade report produces,
// so if a field is added to the IR and not classified, this test and
// TestUnit_Interop_Severities disagree with each other.
func TestUnit_Interop_Roundtrip(t *testing.T) {
	t.Parallel()

	original := bp(everyKind()...)

	s, _, err := FromBlueprint(original)
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	back, _, err := ToBlueprint(s, importOpts())
	if err != nil {
		t.Fatalf("ToBlueprint: %v", err)
	}

	if len(back.Resources) != 1 {
		t.Fatalf("got %d resources, want 1", len(back.Resources))
	}

	want := schemaSliceOf(original.Resources[0])
	got := schemaSliceOf(back.Resources[0])

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the schema slice did not survive the round trip (-want +got):\n%s", diff)
	}
}

// schemaSlice is the part of a resource the official format can express.
//
// Extracted into its own type rather than zeroed in place with cmp options, because
// the explicit field list documents the boundary: everything here crosses, and
// everything on blueprint.Resource that is not here is a loss the report has to
// mention.
type schemaSlice struct {
	MarkdownDescription string
	DeprecationMessage  string
	Attributes          []attrSlice
}

type attrSlice struct {
	Name                     string
	Kind                     blueprint.TypeKind
	ComputedOptionalRequired blueprint.ComputedOptionalRequired
	Sensitive                bool
	MarkdownDescription      string
	DeprecationMessage       string
	Validators               []blueprint.CustomCode
	PlanModifiers            []blueprint.CustomCode
	Default                  *blueprint.Default
	ElemKind                 blueprint.TypeKind
	SDKType                  string
	NestedObject             []attrSlice
}

func schemaSliceOf(r blueprint.Resource) schemaSlice {
	return schemaSlice{
		MarkdownDescription: r.MarkdownDescription,
		DeprecationMessage:  r.DeprecationMessage,
		Attributes:          attrSlicesOf(r.Attributes),
	}
}

func attrSlicesOf(in []blueprint.Attribute) []attrSlice {
	var out []attrSlice

	for _, a := range in {
		if a.Drop {
			continue
		}

		s := attrSlice{
			Name: a.Name,
			// int32 and float32 widen on export and cannot come back, so the
			// expectation is normalised the same way rather than pretending
			// otherwise.
			Kind:                     widen(a.Type.Kind),
			ComputedOptionalRequired: a.ComputedOptionalRequired,
			Sensitive:                a.Sensitive,
			MarkdownDescription:      a.MarkdownDescription,
			DeprecationMessage:       a.DeprecationMessage,
			Validators:               a.Validators,
			PlanModifiers:            a.PlanModifiers,
			Default:                  normaliseDefault(a.Default, widen(a.Type.Kind)),
		}

		if a.Type.ElementType != nil {
			s.ElemKind = widen(a.Type.ElementType.Kind)
		}
		if a.Type.NestedObject != nil {
			s.SDKType = a.Type.NestedObject.SDKType
			s.NestedObject = attrSlicesOf(a.Type.NestedObject.Attributes)
		}

		out = append(out, s)
	}

	return out
}

func widen(k blueprint.TypeKind) blueprint.TypeKind {
	switch k {
	case blueprint.KindInt32:
		return blueprint.KindInt64
	case blueprint.KindFloat32:
		return blueprint.KindFloat64
	default:
		return k
	}
}

// normaliseDefault rewrites a static literal into the form an import produces.
//
// A raw string literal exports and re-imports as an interpreted one -- `devices`
// becomes "devices" -- because the official format carries the value, not the
// syntax. That is a real and acceptable difference, so the expectation is normalised
// rather than the code being made to preserve backticks it cannot see.
func normaliseDefault(d *blueprint.Default, kind blueprint.TypeKind) *blueprint.Default {
	if d == nil || d.Static == nil {
		return d
	}

	out := *d
	lit := *d.Static
	lit.Kind = kind

	if strings.HasPrefix(lit.Raw, "`") {
		lit.Raw = `"` + strings.Trim(lit.Raw, "`") + `"`
	}

	out.Static = &lit

	return &out
}

// TestUnit_Interop_RoundtripStaticDefaults covers each static default type through
// both directions, which the every-kind fixture cannot: it uses custom defaults,
// because only four kinds accept a static one.
func TestUnit_Interop_RoundtripStaticDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind blueprint.TypeKind
		raw  string
	}{
		{blueprint.KindString, `"devices"`},
		{blueprint.KindBool, "true"},
		{blueprint.KindBool, "false"},
		{blueprint.KindInt64, "0"},
		{blueprint.KindInt64, "-7"},
		{blueprint.KindFloat64, "1.5"},
		{blueprint.KindFloat64, "0"},
	}

	for _, tc := range tests {
		t.Run(string(tc.kind)+"="+tc.raw, func(t *testing.T) {
			t.Parallel()

			a := attr("f", tc.kind, blueprint.ComputedOptional)
			a.Default = &blueprint.Default{Static: &blueprint.Literal{Kind: tc.kind, Raw: tc.raw}}

			s, _, err := FromBlueprint(bp(a))
			if err != nil {
				t.Fatalf("FromBlueprint: %v", err)
			}

			back, _, err := ToBlueprint(s, importOpts())
			if err != nil {
				t.Fatalf("ToBlueprint: %v", err)
			}

			got := back.Resources[0].Attributes[0].Default
			if got == nil || got.Static == nil {
				t.Fatalf("the static default did not come back: %+v", got)
			}
			if got.Static.Raw != tc.raw {
				t.Errorf("Raw = %q, want %q", got.Static.Raw, tc.raw)
			}
			if got.Static.Kind != tc.kind {
				t.Errorf("Kind = %q, want %q", got.Static.Kind, tc.kind)
			}
		})
	}
}

// TestUnit_Interop_ExportImportExportIsByteStable is the property the milestone
// names.
//
// Stronger than the struct comparison above and for a different reason: it catches an
// asymmetry that happens to cancel out in the IR but shows up in the document -- an
// import that drops a field the export re-derives, say.
func TestUnit_Interop_ExportImportExportIsByteStable(t *testing.T) {
	t.Parallel()

	first, _, err := FromBlueprint(bp(everyKind()...))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	firstBytes, err := Marshal(first)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	back, _, err := ToBlueprint(first, importOpts())
	if err != nil {
		t.Fatalf("ToBlueprint: %v", err)
	}

	second, _, err := FromBlueprint(back)
	if err != nil {
		t.Fatalf("FromBlueprint (second): %v", err)
	}

	secondBytes, err := Marshal(second)
	if err != nil {
		t.Fatalf("Marshal (second): %v", err)
	}

	if diff := cmp.Diff(string(firstBytes), string(secondBytes)); diff != "" {
		t.Errorf("export -> import -> export is not byte-stable (-first +second):\n%s", diff)
	}
}

// TestUnit_Interop_Drafts is the guard on the whole draft mechanism.
//
// DraftExt keeps an unemittable blueprint out of the pipeline by being a filename the
// loader does not match. That is invisible: it depends entirely on findBlueprints
// using a suffix test, and loosening it to a substring test would make every draft
// loadable by emit in one line, with nothing failing nearby.
func TestUnit_Interop_Drafts(t *testing.T) {
	t.Parallel()

	if strings.HasSuffix(DraftExt, blueprint.Ext) {
		t.Fatalf("DraftExt %q ends in %q, so LoadDir would pick drafts up", DraftExt, blueprint.Ext)
	}

	// And the positive half: it must still look like a blueprint to a human.
	if !strings.HasPrefix(DraftExt, ".blueprint.") || !strings.HasSuffix(DraftExt, ".json") {
		t.Errorf("DraftExt %q should still read as a blueprint file", DraftExt)
	}
}

func TestUnit_Interop_ImportRequiresAProviderName(t *testing.T) {
	t.Parallel()

	s, _, err := FromBlueprint(bp(attr("f", blueprint.KindString, blueprint.Optional)))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	if _, _, err := ToBlueprint(s, Options{}); !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("error = %v, want ErrInvalidSpec", err)
	}

	// TypePrefix defaults to Provider rather than producing "_thing".
	back, _, err := ToBlueprint(s, Options{Provider: "example"})
	if err != nil {
		t.Fatalf("ToBlueprint: %v", err)
	}
	if got := back.Resources[0].TerraformType; got != "example_thing" {
		t.Errorf("TerraformType = %q, want example_thing", got)
	}
	if got := back.Provider.TypePrefix; got != "example" {
		t.Errorf("TypePrefix = %q, want example", got)
	}
}

func TestUnit_Interop_ImportRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   spec.Specification
		want string
		is   error
	}{
		{
			name: "no resources",
			in:   spec.Specification{Version: SpecVersion},
			want: "no resources",
			is:   ErrInvalidSpec,
		},
		{
			name: "resource with no name",
			in:   specOf(resource.Resource{Schema: &resource.Schema{}}),
			want: "no name",
			is:   ErrInvalidSpec,
		},
		{
			name: "resource with no schema",
			in:   specOf(resource.Resource{Name: "thing"}),
			want: "no schema",
			is:   ErrInvalidSpec,
		},
		{
			name: "schema with no attributes",
			in:   specOf(resource.Resource{Name: "thing", Schema: &resource.Schema{}}),
			want: "no attributes",
			is:   ErrInvalidSpec,
		},
		{
			name: "attribute with no type",
			in:   specOf(resourceWith(resource.Attribute{Name: "f"})),
			want: "declares no type",
			is:   ErrInvalidSpec,
		},
		{
			name: "attribute with no name",
			in: specOf(resourceWith(resource.Attribute{
				String: &resource.StringAttribute{ComputedOptionalRequired: schema.Optional},
			})),
			want: "no name",
			is:   ErrInvalidSpec,
		},
		{
			// dynamic is the case the published documentation most invites, and
			// tfplugingen-framework never implemented it either.
			name: "dynamic",
			in: specOf(resourceWith(resource.Attribute{
				Name: "anything", Dynamic: &resource.DynamicAttribute{ComputedOptionalRequired: schema.Optional},
			})),
			want: "dynamic",
			is:   ErrUnrepresentable,
		},
		{
			name: "map_nested",
			in: specOf(resourceWith(resource.Attribute{
				Name: "things", MapNested: &resource.MapNestedAttribute{
					ComputedOptionalRequired: schema.Optional,
					NestedObject: resource.NestedAttributeObject{
						Attributes: resource.Attributes{{
							Name:   "id",
							String: &resource.StringAttribute{ComputedOptionalRequired: schema.Required},
						}},
					},
				},
			})),
			want: "map_nested",
			is:   ErrUnrepresentable,
		},
		{
			name: "object attribute",
			in: specOf(resourceWith(resource.Attribute{
				Name: "cfg", Object: &resource.ObjectAttribute{
					ComputedOptionalRequired: schema.Optional,
					AttributeTypes: schema.ObjectAttributeTypes{
						{Name: "id", String: &schema.StringType{}},
					},
				},
			})),
			want: "object attributes",
			is:   ErrUnrepresentable,
		},
		{
			name: "object element type",
			in: specOf(resourceWith(resource.Attribute{
				Name: "rows", List: &resource.ListAttribute{
					ComputedOptionalRequired: schema.Optional,
					ElementType: schema.ElementType{Object: &schema.ObjectType{
						AttributeTypes: schema.ObjectAttributeTypes{
							{Name: "id", String: &schema.StringType{}},
						},
					}},
				},
			})),
			want: "object element type",
			is:   ErrUnrepresentable,
		},
		{
			name: "element type with no type",
			in: specOf(resourceWith(resource.Attribute{
				Name: "rows", List: &resource.ListAttribute{
					ComputedOptionalRequired: schema.Optional,
					ElementType:              schema.ElementType{},
				},
			})),
			want: "declares no type",
			is:   ErrInvalidSpec,
		},
		{
			name: "nested object with no attributes",
			in: specOf(resourceWith(resource.Attribute{
				Name: "things", SetNested: &resource.SetNestedAttribute{
					ComputedOptionalRequired: schema.Optional,
				},
			})),
			want: "no attributes",
			is:   ErrInvalidSpec,
		},
		{
			name: "block with no type",
			in: specOf(resource.Resource{Name: "thing", Schema: &resource.Schema{
				Attributes: resource.Attributes{{
					Name:   "f",
					String: &resource.StringAttribute{ComputedOptionalRequired: schema.Optional},
				}},
				Blocks: resource.Blocks{{Name: "b"}},
			}}),
			want: "declares no type",
			is:   ErrInvalidSpec,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := ToBlueprint(tc.in, importOpts())
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not mention %q: %v", tc.want, err)
			}
			if !errors.Is(err, tc.is) {
				t.Errorf("error = %v, want %v", err, tc.is)
			}
		})
	}
}

func specOf(res resource.Resource) spec.Specification {
	return spec.Specification{Version: SpecVersion, Resources: resource.Resources{res}}
}

func resourceWith(attrs ...resource.Attribute) resource.Resource {
	return resource.Resource{
		Name:   "thing",
		Schema: &resource.Schema{Attributes: resource.Attributes(attrs)},
	}
}

// TestUnit_Interop_ImportConvertsBlocks: a block and a nested attribute are the same
// data with different configuration syntax, so importing a hand-authored
// specification that uses blocks must work -- and must say what it did, because the
// choice is permanent once a provider is published.
func TestUnit_Interop_ImportConvertsBlocks(t *testing.T) {
	t.Parallel()

	child := resource.Attributes{{
		Name:   "id",
		String: &resource.StringAttribute{ComputedOptionalRequired: schema.Required},
	}}

	in := specOf(resource.Resource{Name: "thing", Schema: &resource.Schema{
		Attributes: resource.Attributes{{
			Name:   "f",
			String: &resource.StringAttribute{ComputedOptionalRequired: schema.Optional},
		}},
		Blocks: resource.Blocks{
			{Name: "listed", ListNested: &resource.ListNestedBlock{
				NestedObject: resource.NestedBlockObject{Attributes: child},
			}},
			{Name: "setted", SetNested: &resource.SetNestedBlock{
				NestedObject: resource.NestedBlockObject{Attributes: child},
			}},
			{Name: "single", SingleNested: &resource.SingleNestedBlock{Attributes: child}},
		},
	}})

	back, report, err := ToBlueprint(in, importOpts())
	if err != nil {
		t.Fatalf("ToBlueprint: %v", err)
	}

	attrs := back.Resources[0].Attributes
	if len(attrs) != 4 {
		t.Fatalf("got %d attributes, want 4 (one attribute plus three converted blocks)", len(attrs))
	}

	wantKinds := map[string]blueprint.TypeKind{
		"listed": blueprint.KindListNested,
		"setted": blueprint.KindSetNested,
		"single": blueprint.KindSingleNested,
	}
	for _, a := range attrs {
		if want, ok := wantKinds[a.Name]; ok {
			if a.Type.Kind != want {
				t.Errorf("%s: kind = %q, want %q", a.Name, a.Type.Kind, want)
			}
			// A block has no presence upstream; optional is the only choice that
			// neither forces the practitioner to write it nor forbids them.
			if a.ComputedOptionalRequired != blueprint.Optional {
				t.Errorf("%s: presence = %q, want optional", a.Name, a.ComputedOptionalRequired)
			}
		}
	}

	found := false
	for _, n := range report.Notes {
		if n.Severity == SeverityLossy && strings.Contains(n.Message, "block") {
			found = true
		}
	}
	if !found {
		t.Errorf("converting blocks must be reported as lossy:\n%v", report.Sorted())
	}
}

// TestUnit_Interop_ImportPromotesAPlainDescription: a document that sets only the
// plain description has its text reinterpreted as markdown, which is nearly always
// harmless and occasionally not, so it is reported.
func TestUnit_Interop_ImportPromotesAPlainDescription(t *testing.T) {
	t.Parallel()

	plain := "a_field_with_underscores"

	in := specOf(resourceWith(resource.Attribute{
		Name: "f", String: &resource.StringAttribute{
			ComputedOptionalRequired: schema.Optional,
			Description:              &plain,
		},
	}))

	back, report, err := ToBlueprint(in, importOpts())
	if err != nil {
		t.Fatalf("ToBlueprint: %v", err)
	}

	if got := back.Resources[0].Attributes[0].MarkdownDescription; got != plain {
		t.Errorf("MarkdownDescription = %q, want %q", got, plain)
	}

	found := false
	for _, n := range report.Notes {
		if strings.Contains(n.Message, "markdown") {
			found = true
		}
	}
	if !found {
		t.Errorf("promoting a plain description must be reported:\n%v", report.Sorted())
	}
}

// TestUnit_Interop_ImportedBlueprintIsNotEmittable is the point of the draft
// mechanism, asserted rather than asserted-in-a-comment.
//
// An imported blueprint has a schema and no bindings. blueprint.Validate must refuse
// it, because a blueprint that validated and then failed at emission would be a much
// worse thing to hand somebody.
func TestUnit_Interop_ImportedBlueprintIsNotEmittable(t *testing.T) {
	t.Parallel()

	s, _, err := FromBlueprint(bp(everyKind()...))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	back, _, err := ToBlueprint(s, importOpts())
	if err != nil {
		t.Fatalf("ToBlueprint: %v", err)
	}

	err = back.Validate()
	if err == nil {
		t.Fatal("an imported blueprint has no bindings, so it must not validate")
	}
	if !errors.Is(err, blueprint.ErrInvalid) {
		t.Errorf("error = %v, want ErrInvalid", err)
	}

	// And the reason must be the bindings rather than something incidental, or the
	// draft mechanism is solving a different problem than the one described.
	if !strings.Contains(err.Error(), "binding") {
		t.Errorf("the refusal should be about bindings:\n%v", err)
	}
}

// TestUnit_Interop_ImportReportsWhatMustBeAuthored: the unauthored fields are the
// whole output a human acts on, so their absence is reported rather than left to be
// discovered by running emit.
func TestUnit_Interop_ImportReportsWhatMustBeAuthored(t *testing.T) {
	t.Parallel()

	s, _, err := FromBlueprint(bp(nested("items", blueprint.KindSetNested)))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	// Strip the external type, which is how a hand-authored document arrives.
	s.Resources[0].Schema.Attributes[0].SetNested.NestedObject.AssociatedExternalType = nil

	_, report, err := ToBlueprint(s, importOpts())
	if err != nil {
		t.Fatalf("ToBlueprint: %v", err)
	}

	found := false
	for _, n := range report.Notes {
		if strings.HasSuffix(n.Path, ".type.nested.sdkType") && n.Severity == SeverityDropped {
			found = true
		}
	}
	if !found {
		t.Errorf("a nested object with no external type must be reported:\n%v", report.Sorted())
	}
}

func TestUnit_Interop_ImportSkipsDataSourcesAndProviderSchema(t *testing.T) {
	t.Parallel()

	in := specOf(resourceWith(resource.Attribute{
		Name: "f", String: &resource.StringAttribute{ComputedOptionalRequired: schema.Optional},
	}))
	in.DataSources = datasourcesWithOne()
	in.Provider = providerWithSchema()

	_, report, err := ToBlueprint(in, importOpts())
	if err != nil {
		t.Fatalf("ToBlueprint: %v", err)
	}

	found := map[string]bool{}
	for _, n := range report.Notes {
		if n.Path == "datasources" {
			found["datasources"] = true
		}
		if n.Path == "provider.schema" {
			found["provider"] = true
		}
	}
	if !found["datasources"] || !found["provider"] {
		t.Errorf("both skips must be reported, got %v from:\n%v", found, report.Sorted())
	}
}

// TestUnit_Interop_ImportCustomTypeIsReported: the blueprint has no field for a
// custom type and value, so it is dropped -- loudly, because it is a deliberate
// choice somebody made in the source document.
func TestUnit_Interop_ImportCustomTypeIsReported(t *testing.T) {
	t.Parallel()

	in := specOf(resourceWith(resource.Attribute{
		Name: "f", String: &resource.StringAttribute{
			ComputedOptionalRequired: schema.Optional,
			CustomType: &schema.CustomType{
				Type:      "customtypes.StringType",
				ValueType: "customtypes.StringValue",
			},
		},
	}))

	_, report, err := ToBlueprint(in, importOpts())
	if err != nil {
		t.Fatalf("ToBlueprint: %v", err)
	}

	found := false
	for _, n := range report.Notes {
		if strings.HasSuffix(n.Path, ".customType") && n.Severity == SeverityDropped {
			found = true
		}
	}
	if !found {
		t.Errorf("a custom type must be reported as dropped:\n%v", report.Sorted())
	}
}

func TestUnit_Interop_ImportSortsResourcesByKey(t *testing.T) {
	t.Parallel()

	in := spec.Specification{Version: SpecVersion, Resources: resource.Resources{
		resourceNamed("zebra"), resourceNamed("antelope"),
	}}

	back, _, err := ToBlueprint(in, importOpts())
	if err != nil {
		t.Fatalf("ToBlueprint: %v", err)
	}

	if back.Resources[0].Key != "antelope" || back.Resources[1].Key != "zebra" {
		t.Errorf("keys = %q, %q; want antelope then zebra", back.Resources[0].Key, back.Resources[1].Key)
	}
}

func resourceNamed(name string) resource.Resource {
	return resource.Resource{Name: name, Schema: &resource.Schema{
		Attributes: resource.Attributes{{
			Name:   "id",
			String: &resource.StringAttribute{ComputedOptionalRequired: schema.Computed},
		}},
	}}
}

func TestUnit_Interop_LowerFirstRune(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"Assignments": "assignments",
		"A":           "a",
		"":            "",
	}

	for in, want := range tests {
		if got := lowerFirstRune(in); got != want {
			t.Errorf("lowerFirstRune(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnit_Interop_PresenceFromToleratesAnUnknownValue(t *testing.T) {
	t.Parallel()

	// Carried through rather than rejected: blueprint.Validate refuses it with a
	// message naming the attribute, which is a better report than one from here.
	if got := presenceFrom("telepathy"); got != blueprint.ComputedOptionalRequired("telepathy") {
		t.Errorf("presenceFrom = %q, want it carried through", got)
	}
}

// TestUnit_Interop_ImportDefaultsCarryBothForms covers a default that has a custom
// definition and a static value at once.
//
// Upstream allows both on the same field, and the readers build the blueprint default
// incrementally, so "custom present, static absent" and "both present" take different
// paths through the same function. Getting the second wrong would drop one of them
// silently.
func TestUnit_Interop_ImportDefaultsCarryBothForms(t *testing.T) {
	t.Parallel()

	custom := &schema.CustomDefault{
		SchemaDefinition: "defaults.Something()",
		Imports:          []code.Import{{Path: "example.com/defaults"}},
	}

	boolStatic, strStatic, intStatic, floatStatic := true, "x", int64(3), 2.5

	in := specOf(resourceWith(
		resource.Attribute{Name: "b", Bool: &resource.BoolAttribute{
			ComputedOptionalRequired: schema.ComputedOptional,
			Default:                  &schema.BoolDefault{Custom: custom, Static: &boolStatic},
		}},
		resource.Attribute{Name: "s", String: &resource.StringAttribute{
			ComputedOptionalRequired: schema.ComputedOptional,
			Default:                  &schema.StringDefault{Custom: custom, Static: &strStatic},
		}},
		resource.Attribute{Name: "i", Int64: &resource.Int64Attribute{
			ComputedOptionalRequired: schema.ComputedOptional,
			Default:                  &schema.Int64Default{Custom: custom, Static: &intStatic},
		}},
		resource.Attribute{Name: "f", Float64: &resource.Float64Attribute{
			ComputedOptionalRequired: schema.ComputedOptional,
			Default:                  &schema.Float64Default{Custom: custom, Static: &floatStatic},
		}},
	))

	back, _, err := ToBlueprint(in, importOpts())
	if err != nil {
		t.Fatalf("ToBlueprint: %v", err)
	}

	wantRaw := map[string]string{"b": "true", "s": `"x"`, "i": "3", "f": "2.5"}

	for _, a := range back.Resources[0].Attributes {
		if a.Default == nil {
			t.Errorf("%s: lost its default", a.Name)
			continue
		}
		if a.Default.Custom == nil {
			t.Errorf("%s: lost the custom half of its default", a.Name)
		}
		if a.Default.Static == nil {
			t.Errorf("%s: lost the static half of its default", a.Name)
			continue
		}
		if got := a.Default.Static.Raw; got != wantRaw[a.Name] {
			t.Errorf("%s: Raw = %q, want %q", a.Name, got, wantRaw[a.Name])
		}
		if len(a.Default.Custom.Imports) != 1 {
			t.Errorf("%s: the custom default's imports did not cross", a.Name)
		}
	}
}

// TestUnit_Interop_ImportSkipsNilValidators: upstream's slice elements hold a pointer,
// so a document can declare a validator entry with nothing in it. Dereferencing that
// would panic on input a schema-valid document can legitimately contain.
func TestUnit_Interop_ImportSkipsNilValidators(t *testing.T) {
	t.Parallel()

	in := specOf(resourceWith(resource.Attribute{
		Name: "f", String: &resource.StringAttribute{
			ComputedOptionalRequired: schema.Optional,
			Validators:               schema.StringValidators{{Custom: nil}},
			PlanModifiers:            schema.StringPlanModifiers{{Custom: nil}},
		},
	}))

	back, _, err := ToBlueprint(in, importOpts())
	if err != nil {
		t.Fatalf("ToBlueprint: %v", err)
	}

	a := back.Resources[0].Attributes[0]
	if len(a.Validators) != 0 || len(a.PlanModifiers) != 0 {
		t.Errorf("an empty entry should be skipped, got %d validators and %d modifiers",
			len(a.Validators), len(a.PlanModifiers))
	}
}

// TestUnit_Interop_ImportCollectionsOfCollections covers the recursive element walk in
// the import direction, which the pilot only exercises one level deep.
func TestUnit_Interop_ImportCollectionsOfCollections(t *testing.T) {
	t.Parallel()

	in := specOf(resourceWith(resource.Attribute{
		Name: "matrix", List: &resource.ListAttribute{
			ComputedOptionalRequired: schema.Optional,
			ElementType: schema.ElementType{Set: &schema.SetType{
				ElementType: schema.ElementType{Map: &schema.MapType{
					ElementType: schema.ElementType{Bool: &schema.BoolType{}},
				}},
			}},
		},
	}))

	back, _, err := ToBlueprint(in, importOpts())
	if err != nil {
		t.Fatalf("ToBlueprint: %v", err)
	}

	got := back.Resources[0].Attributes[0].Type
	if got.Kind != blueprint.KindList ||
		got.ElementType == nil || got.ElementType.Kind != blueprint.KindSet ||
		got.ElementType.ElementType == nil || got.ElementType.ElementType.Kind != blueprint.KindMap ||
		got.ElementType.ElementType.ElementType == nil || got.ElementType.ElementType.ElementType.Kind != blueprint.KindBool {
		t.Errorf("the nested element types did not survive: %+v", got)
	}
}

// TestUnit_Interop_ImportEveryScalarElementType covers the remaining element branches.
func TestUnit_Interop_ImportEveryScalarElementType(t *testing.T) {
	t.Parallel()

	elems := map[blueprint.TypeKind]schema.ElementType{
		blueprint.KindBool:    {Bool: &schema.BoolType{}},
		blueprint.KindString:  {String: &schema.StringType{}},
		blueprint.KindInt64:   {Int64: &schema.Int64Type{}},
		blueprint.KindFloat64: {Float64: &schema.Float64Type{}},
		blueprint.KindNumber:  {Number: &schema.NumberType{}},
	}

	for want, elem := range elems {
		t.Run(string(want), func(t *testing.T) {
			t.Parallel()

			in := specOf(resourceWith(resource.Attribute{
				Name: "f", Set: &resource.SetAttribute{
					ComputedOptionalRequired: schema.Optional,
					ElementType:              elem,
				},
			}))

			back, _, err := ToBlueprint(in, importOpts())
			if err != nil {
				t.Fatalf("ToBlueprint: %v", err)
			}

			if got := back.Resources[0].Attributes[0].Type.ElementType.Kind; got != want {
				t.Errorf("element kind = %q, want %q", got, want)
			}
		})
	}
}

func TestUnit_Interop_Unauthored(t *testing.T) {
	t.Parallel()

	s, _, err := FromBlueprint(bp(nested("items", blueprint.KindSetNested)))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}
	s.Resources[0].Schema.Attributes[0].SetNested.NestedObject.AssociatedExternalType = nil

	back, _, err := ToBlueprint(s, importOpts())
	if err != nil {
		t.Fatalf("ToBlueprint: %v", err)
	}

	got := strings.Join(Unauthored(back), "\n")

	// The bindings a human has to write, and the collapsed wire line with its count.
	for _, want := range []string{
		"provider.sdk", "binding.service", "binding.{create,read,update,delete}",
		"binding.id.fromCreate", "policy.updateStyle",
		"attributes[*].wire", "type.nested.sdkType",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Unauthored is missing %q:\n%s", want, got)
		}
	}

	// Collapsed, not one line per attribute: the whole point is that this reads as a
	// diagnosis rather than the forty-odd messages Validate would produce.
	if n := len(Unauthored(back)); n > 10 {
		t.Errorf("Unauthored returned %d lines, which is a wall rather than a diagnosis", n)
	}
}

func datasourcesWithOne() datasource.DataSources {
	return datasource.DataSources{{
		Name: "thing", Schema: &datasource.Schema{
			Attributes: datasource.Attributes{{
				Name:   "id",
				String: &datasource.StringAttribute{ComputedOptionalRequired: schema.Computed},
			}},
		},
	}}
}

func providerWithSchema() *provider.Provider {
	return &provider.Provider{
		Name: "example",
		Schema: &provider.Schema{
			Attributes: provider.Attributes{{
				Name:   "api_token",
				String: &provider.StringAttribute{OptionalRequired: schema.Required},
			}},
		},
	}
}
