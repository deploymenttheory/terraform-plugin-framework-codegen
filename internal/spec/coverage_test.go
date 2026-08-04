package spec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// bp builds a blueprint carrying one resource with the given attributes.
//
// Constructed in Go rather than read from testdata: these tests are about the
// mapping of individual kinds and combinations, and a JSON fixture per case would
// put the interesting part of each test in a different file from its assertion.
func bp(attrs ...blueprint.Attribute) blueprint.Blueprint {
	return blueprint.Blueprint{
		FormatVersion: blueprint.FormatVersion,
		Provider: blueprint.Provider{
			Name:       "example",
			TypePrefix: "example",
		},
		Resources: []blueprint.Resource{{
			Key:  "thing",
			Name: "thing",
			Schema: blueprint.Schema{
				Attributes: attrs,
			},
		}},
	}
}

func attr(name string, kind blueprint.TypeKind, presence blueprint.ComputedOptionalRequired) blueprint.Attribute {
	return blueprint.Attribute{Name: name, ComputedOptionalRequired: presence, Type: blueprint.AttrType{Kind: kind}}
}

// TestUnit_Spec_Conformance is the assertion that pays for this package.
//
// Every kind the blueprint can express is exported and then checked against
// HashiCorp's own embedded JSON schema. That is a claim no test written inside this
// repository can make on its own: it says the blueprint's schema slice describes a
// schema Terraform can actually have, verified by a party that does not share this
// repository's assumptions about what a valid schema is.
func TestUnit_Spec_Conformance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	scalars := []blueprint.TypeKind{
		blueprint.KindBool, blueprint.KindString,
		blueprint.KindInt32, blueprint.KindInt64,
		blueprint.KindFloat32, blueprint.KindFloat64,
		blueprint.KindNumber,
	}

	tests := []struct {
		name  string
		attrs []blueprint.Attribute
	}{
		{"every scalar", func() []blueprint.Attribute {
			var out []blueprint.Attribute
			for _, k := range scalars {
				out = append(out, attr(string(k)+"_field", k, blueprint.Optional))
			}
			return out
		}()},
		{"collections of strings", []blueprint.Attribute{
			collection("list_field", blueprint.KindList, blueprint.KindString),
			collection("set_field", blueprint.KindSet, blueprint.KindString),
			collection("map_field", blueprint.KindMap, blueprint.KindString),
		}},
		{"nested collection of collections", []blueprint.Attribute{{
			Name: "matrix", ComputedOptionalRequired: blueprint.Optional,
			Type: blueprint.AttrType{
				Kind: blueprint.KindList,
				ElementType: &blueprint.AttrType{
					Kind:        blueprint.KindSet,
					ElementType: &blueprint.AttrType{Kind: blueprint.KindString},
				},
			},
		}}},
		{"the three nested kinds", []blueprint.Attribute{
			nested("items", blueprint.KindListNested),
			nested("things", blueprint.KindSetNested),
			nested("config", blueprint.KindSingleNested),
		}},
		{"validators, modifiers and defaults", []blueprint.Attribute{{
			Name: "name", ComputedOptionalRequired: blueprint.ComputedOptional,
			Type:                blueprint.AttrType{Kind: blueprint.KindString},
			Sensitive:           true,
			MarkdownDescription: "the **name**",
			DeprecationMessage:  "use `title`",
			Validators: []blueprint.CustomCode{{
				SchemaDefinition: "stringvalidator.LengthAtLeast(1)",
				Imports:          []blueprint.Import{{Path: "example.com/validators", Alias: "v"}},
			}},
			PlanModifiers: []blueprint.CustomCode{{
				SchemaDefinition: "stringplanmodifier.RequiresReplace()",
			}},
			Default: &blueprint.Default{
				Static: &blueprint.Literal{Kind: blueprint.KindString, Raw: `"devices"`},
			},
		}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s, _, err := FromBlueprint(bp(tc.attrs...))
			if err != nil {
				t.Fatalf("FromBlueprint: %v", err)
			}

			data, err := Marshal(s)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			if err := Validate(ctx, data); err != nil {
				t.Fatalf("the exported document does not satisfy the upstream schema: %v\n%s", err, data)
			}

			// Parse as well as Validate: the schema check is structural, and
			// upstream's own Go-level validation catches things the schema does not.
			if _, err := Parse(ctx, data); err != nil {
				t.Errorf("upstream could not parse our output: %v", err)
			}
		})
	}
}

func collection(name string, kind, elem blueprint.TypeKind) blueprint.Attribute {
	return blueprint.Attribute{
		Name: name, ComputedOptionalRequired: blueprint.Optional,
		Type: blueprint.AttrType{Kind: kind, ElementType: &blueprint.AttrType{Kind: elem}},
	}
}

func nested(name string, kind blueprint.TypeKind) blueprint.Attribute {
	return blueprint.Attribute{
		Name: name, ComputedOptionalRequired: blueprint.Optional,
		Type: blueprint.AttrType{
			Kind: kind,
			NestedObject: &blueprint.NestedAttributeObject{
				GoTypeName: "ItemModel", SDKType: "pkg.Item",
				AttrTypesVar: "itemAttrTypes", ObjectTypeVar: "itemObjectType",
				ExpandFunc: "expandItem", FlattenFunc: "flattenItem",
				Attributes: []blueprint.Attribute{
					attr("id", blueprint.KindString, blueprint.Required),
				},
			},
		},
	}
}

// TestUnit_Spec_Presence covers all four spellings and the case a cast would
// have laundered.
//
// The two formats spell these identically, which makes schema.ComputedOptionalRequired(p)
// look like a free conversion. It is not: an unrecognised value would reach the JSON
// and be caught, if at all, as an opaque enum violation from upstream's validator,
// a long way from the attribute that caused it.
func TestUnit_Spec_Presence(t *testing.T) {
	t.Parallel()

	for _, p := range []blueprint.ComputedOptionalRequired{
		blueprint.Required, blueprint.Optional, blueprint.Computed, blueprint.ComputedOptional,
	} {
		t.Run(string(p), func(t *testing.T) {
			t.Parallel()

			s, _, err := FromBlueprint(bp(attr("f", blueprint.KindString, p)))
			if err != nil {
				t.Fatalf("FromBlueprint: %v", err)
			}

			got := s.Resources[0].Schema.Attributes[0].String.ComputedOptionalRequired
			if string(got) != string(p) {
				t.Errorf("presence = %q, want %q", got, p)
			}
		})
	}

	t.Run("unknown", func(t *testing.T) {
		t.Parallel()

		_, _, err := FromBlueprint(bp(attr("f", blueprint.KindString, "telepathy")))
		if !errors.Is(err, ErrUnrepresentable) {
			t.Errorf("error = %v, want ErrUnrepresentable", err)
		}
		if err != nil && !strings.Contains(err.Error(), "telepathy") {
			t.Errorf("the error should name the offending value: %v", err)
		}
	})
}

// TestUnit_Spec_Defaults covers the one place in the mapping that needs a real
// parser rather than a copy.
//
// The blueprint stores a default as the Go literal the emitter will write; the
// official format wants a typed JSON value. Getting this wrong is quiet -- an
// unparsed literal would land in the document as a string, or worse be rerouted
// into a custom default that expects a full expression and generates code that does
// not compile.
func TestUnit_Spec_Defaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		kind    blueprint.TypeKind
		raw     string
		wantErr bool
		check   func(*testing.T, blueprint.Blueprint)
	}{
		{name: "string", kind: blueprint.KindString, raw: `"devices"`},
		{name: "raw string literal", kind: blueprint.KindString, raw: "`devices`"},
		{name: "bool false", kind: blueprint.KindBool, raw: "false"},
		{name: "int zero", kind: blueprint.KindInt64, raw: "0"},
		{name: "int32 coarsened", kind: blueprint.KindInt32, raw: "7"},
		{name: "float", kind: blueprint.KindFloat64, raw: "1.5"},

		// A bare identifier is not a literal. Refusing is the point: rerouting it
		// into Custom would emit `Default: devices` into generated Go.
		{name: "bare identifier", kind: blueprint.KindString, raw: "devices", wantErr: true},
		{name: "qualified constant", kind: blueprint.KindString, raw: "pkg.Devices", wantErr: true},
		{name: "not a bool", kind: blueprint.KindBool, raw: "yes", wantErr: true},
		{name: "not an int", kind: blueprint.KindInt64, raw: "1.5", wantErr: true},
		{name: "not a float", kind: blueprint.KindFloat64, raw: "many", wantErr: true},

		// NumberDefault carries only a custom definition upstream -- it has no
		// Static field at all -- so a static default on a number cannot cross.
		{name: "number has no static default", kind: blueprint.KindNumber, raw: "1", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := attr("f", tc.kind, blueprint.ComputedOptional)
			a.Default = &blueprint.Default{Static: &blueprint.Literal{Kind: tc.kind, Raw: tc.raw}}

			_, _, err := FromBlueprint(bp(a))

			if tc.wantErr {
				if !errors.Is(err, ErrUnrepresentable) {
					t.Errorf("error = %v, want ErrUnrepresentable", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("FromBlueprint: %v", err)
			}
		})
	}
}

func TestUnit_Spec_DefaultValuesAreTyped(t *testing.T) {
	t.Parallel()

	a := attr("f", blueprint.KindString, blueprint.ComputedOptional)
	a.Default = &blueprint.Default{Static: &blueprint.Literal{Kind: blueprint.KindString, Raw: `"devices"`}}

	s, _, err := FromBlueprint(bp(a))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	got := s.Resources[0].Schema.Attributes[0].String.Default
	if got == nil || got.Static == nil {
		t.Fatalf("static default did not cross: %+v", got)
	}
	// Unquoted, not the Go literal. Writing `"devices"` with the quotes into JSON
	// would give a default of five characters including quote marks.
	if *got.Static != "devices" {
		t.Errorf("static = %q, want %q", *got.Static, "devices")
	}
}

// TestUnit_Spec_Downgrade covers the widening cases and asserts they are
// reported rather than silent.
func TestUnit_Spec_Downgrade(t *testing.T) {
	t.Parallel()

	s, report, err := FromBlueprint(bp(
		attr("small_int", blueprint.KindInt32, blueprint.Optional),
		attr("small_float", blueprint.KindFloat32, blueprint.Optional),
	))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	attrs := s.Resources[0].Schema.Attributes
	if attrs[0].Int64 == nil {
		t.Errorf("int32 should be written as int64, got %+v", attrs[0])
	}
	if attrs[1].Float64 == nil {
		t.Errorf("float32 should be written as float64, got %+v", attrs[1])
	}

	if report.Count(SeverityLossy) != 2 {
		t.Errorf("two widenings should produce two lossy notes, got %d:\n%v",
			report.Count(SeverityLossy), report.Sorted())
	}
	if !report.Lost() {
		t.Error("Lost should be true when something was coarsened")
	}

	// Strict turns a coarsened export into a failure; the default does not.
	if err := report.Err(false); err != nil {
		t.Errorf("a downgraded export is a success without -strict: %v", err)
	}
	if err := report.Err(true); !errors.Is(err, ErrDowngraded) {
		t.Errorf("error = %v, want ErrDowngraded", err)
	}
}

// TestUnit_Spec_BehaviourReportedOnlyWhenPopulated: an unprobed blueprint must
// not produce a note per attribute saying nothing was observed.
func TestUnit_Spec_BehaviourReportedOnlyWhenPopulated(t *testing.T) {
	t.Parallel()

	_, quiet, err := FromBlueprint(bp(attr("f", blueprint.KindString, blueprint.Optional)))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}
	for _, n := range quiet.Notes {
		if strings.Contains(n.Path, "behaviour") {
			t.Errorf("an unpopulated Behaviour should be silent, got %v", n)
		}
	}

	writable := false
	probed := attr("f", blueprint.KindString, blueprint.Optional)
	probed.Behaviour = blueprint.Behaviour{Writable: &writable}

	_, loud, err := FromBlueprint(bp(probed))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	found := false
	for _, n := range loud.Notes {
		if strings.Contains(n.Path, "behaviour") {
			found = true
		}
	}
	if !found {
		t.Error("a populated Behaviour must be reported as dropped")
	}
}

// TestUnit_Spec_UniformLossesAreAggregated pins the readability decision.
//
// Wire bindings and model field names exist on every attribute, so reporting them
// individually turns the pilot's report into sixty-odd lines of which forty-five are
// identical. The aggregate carries a count so nothing is hidden.
func TestUnit_Spec_UniformLossesAreAggregated(t *testing.T) {
	t.Parallel()

	var attrs []blueprint.Attribute
	for _, name := range []string{"a", "b", "c", "d"} {
		a := attr(name, blueprint.KindString, blueprint.Optional)
		a.GoField = strings.ToUpper(name)
		a.Wire = blueprint.WireBinding{JSONPath: name}
		attrs = append(attrs, a)
	}

	_, report, err := FromBlueprint(bp(attrs...))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	wire, goField := 0, 0
	for _, n := range report.Notes {
		switch {
		case strings.HasSuffix(n.Path, ".wire"):
			wire++
			if !strings.Contains(n.Message, "(4 affected)") {
				t.Errorf("the aggregate note should carry its count: %q", n.Message)
			}
		case strings.HasSuffix(n.Path, ".goField"):
			goField++
		}
	}

	if wire != 1 || goField != 1 {
		t.Errorf("four attributes should give one wire note and one goField note, got %d and %d", wire, goField)
	}
}

// TestUnit_Spec_Severities asserts the loss taxonomy is total.
//
// The expected list is written out rather than derived, so that adding a blueprint
// field with no upstream counterpart and forgetting to classify it fails here. That
// is the whole mechanism keeping the report from going stale as the IR grows.
func TestUnit_Spec_Severities(t *testing.T) {
	t.Parallel()

	want := map[string]Severity{
		"provider.schema":      SeverityInfo,
		"provider.sdk":         SeverityDropped,
		"provider.goModule":    SeverityInfo,
		"provider.typePrefix":  SeverityInfo,
		"provider.conventions": SeverityInfo,
		"provider.support":     SeverityInfo,
		"source":               SeverityInfo,

		"binding":            SeverityDropped,
		"policy.updateStyle": SeverityDropped,
		"policy.readBack":    SeverityDropped,
		"policy.delete":      SeverityDropped,
		"import":             SeverityDropped,
		"timeouts":           SeverityDropped,
		"naming":             SeverityInfo,
		"docRefUrl":          SeverityInfo,

		// Both are facets the format has no representation for at all, so a document
		// exported from a blueprint carrying them describes a resource that cannot be
		// addressed by identity and cannot be listed. Dropped rather than lossy: there is
		// no coarser form to fall back to.
		"identity": SeverityDropped,
		"list":     SeverityDropped,
		// An entire block kind with no counterpart, so it is reported once for the kind
		// rather than per action.
		"actionKind":    SeverityDropped,
		"ephemeralKind": SeverityDropped,

		// The escape hatches, dropped for two different reasons. A cross-attribute rule has
		// no coarser form to degrade to, because the format's validators are per-attribute
		// and it has no path expression at all. The hooks are files somebody else wrote, and
		// an export that stayed quiet about them would invite losing that work.
		"configValidators": SeverityDropped,
		"hooks":            SeverityDropped,

		"goField":             SeverityInfo,
		"wire":                SeverityDropped,
		"behaviour":           SeverityDropped,
		"markdownDescription": SeverityInfo,
		"importedDescription": SeverityInfo,

		"type.kind/int32":   SeverityLossy,
		"type.kind/float32": SeverityLossy,

		"type.nested.names":   SeverityInfo,
		"type.nested.sdkType": SeverityInfo,
	}

	for key, sev := range want {
		entry, ok := taxonomy[key]
		if !ok {
			t.Errorf("taxonomy is missing %q", key)
			continue
		}
		if entry.Severity != sev {
			t.Errorf("taxonomy[%q].Severity = %q, want %q", key, entry.Severity, sev)
		}
		if entry.Message == "" {
			t.Errorf("taxonomy[%q] has no message", key)
		}
	}

	for key := range taxonomy {
		if _, ok := want[key]; !ok {
			t.Errorf("taxonomy has an unexpected entry %q; classify it in the test or remove it", key)
		}
	}
}

// TestUnit_Spec_UnclassifiedLossDegrades: an unknown taxonomy key must produce a
// note rather than kill the run. A missing classification is a bug in this package,
// and losing the whole export over it would be a worse outcome than a vague note.
func TestUnit_Spec_UnclassifiedLossDegrades(t *testing.T) {
	t.Parallel()

	var r Report

	r.note("no-such-key", "somewhere")
	r.noteCount("no-such-key", "somewhere", 3)

	if len(r.Notes) != 2 {
		t.Fatalf("got %d notes, want 2", len(r.Notes))
	}
	for _, n := range r.Notes {
		if n.Severity != SeverityDropped || !strings.Contains(n.Message, "no-such-key") {
			t.Errorf("an unclassified loss should be reported loudly: %v", n)
		}
	}
}

func TestUnit_Spec_ExportRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   blueprint.Blueprint
		want string
	}{
		{
			name: "resource with no attributes",
			in:   bp(),
			want: "no attributes",
		},
		{
			name: "collection with no element type",
			in:   bp(attr("f", blueprint.KindList, blueprint.Optional)),
			want: "element type",
		},
		{
			name: "nested kind with no object shape",
			in:   bp(attr("f", blueprint.KindSetNested, blueprint.Optional)),
			want: "object shape",
		},
		{
			name: "unknown kind",
			in:   bp(attr("f", "octopus", blueprint.Optional)),
			want: "octopus",
		},
		{
			name: "nested object with no attributes",
			in: bp(blueprint.Attribute{
				Name: "f", ComputedOptionalRequired: blueprint.Optional,
				Type: blueprint.AttrType{
					Kind:         blueprint.KindSetNested,
					NestedObject: &blueprint.NestedAttributeObject{GoTypeName: "M"},
				},
			}),
			want: "no attributes",
		},
		{
			name: "unmappable element kind",
			in: bp(blueprint.Attribute{
				Name: "f", ComputedOptionalRequired: blueprint.Optional,
				Type: blueprint.AttrType{
					Kind:        blueprint.KindList,
					ElementType: &blueprint.AttrType{Kind: "octopus"},
				},
			}),
			want: "octopus",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := FromBlueprint(tc.in)
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not mention %q: %v", tc.want, err)
			}
			if !errors.Is(err, ErrUnrepresentable) {
				t.Errorf("error = %v, want ErrUnrepresentable", err)
			}
		})
	}
}

// TestUnit_Spec_DataSourceLosesModifiersAndDefaults: the datasource package has
// no plan modifiers and no defaults, and a blueprint carrying them must be told so
// rather than have them vanish.
func TestUnit_Spec_DataSourceLosesModifiersAndDefaults(t *testing.T) {
	t.Parallel()

	a := attr("f", blueprint.KindString, blueprint.Computed)
	a.PlanModifiers = []blueprint.CustomCode{{SchemaDefinition: "x()"}}
	a.Default = &blueprint.Default{Static: &blueprint.Literal{Kind: blueprint.KindString, Raw: `"y"`}}

	in := bp()
	in.Resources = nil
	in.DataSources = []blueprint.DataSource{{
		Key: "thing", Name: "thing", Schema: blueprint.Schema{
			Attributes: []blueprint.Attribute{a},
		},
	}}

	s, report, err := FromBlueprint(in)
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	if report.DataSources != 1 || report.Resources != 0 {
		t.Errorf("counts = %d data sources, %d resources", report.DataSources, report.Resources)
	}

	found := map[string]bool{}
	for _, n := range report.Notes {
		if strings.HasSuffix(n.Path, ".planModifiers") {
			found["modifiers"] = true
		}
		if strings.HasSuffix(n.Path, ".default") {
			found["default"] = true
		}
	}
	if !found["modifiers"] || !found["default"] {
		t.Errorf("both losses must be reported, got %v from:\n%v", found, report.Sorted())
	}

	// And the exported document must still be valid.
	data, err := Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := Validate(context.Background(), data); err != nil {
		t.Errorf("the exported data source is not valid: %v\n%s", err, data)
	}
}

// TestUnit_Spec_DropIsCountedNotSilent: an attribute the blueprint asked to be
// left out is legitimately absent, but a node-count difference between the two
// documents would alarm anyone comparing them, so it is stated.
func TestUnit_Spec_DropIsCountedNotSilent(t *testing.T) {
	t.Parallel()

	keep := attr("keep", blueprint.KindString, blueprint.Optional)
	drop := attr("drop", blueprint.KindString, blueprint.Optional)
	drop.Drop = true

	s, report, err := FromBlueprint(bp(keep, drop))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	if len(s.Resources[0].Schema.Attributes) != 1 {
		t.Errorf("a dropped attribute must not be exported")
	}
	if report.Omitted != 1 {
		t.Errorf("Omitted = %d, want 1", report.Omitted)
	}
	if report.Attributes != 1 {
		t.Errorf("Attributes = %d, want 1", report.Attributes)
	}

	// A dropped resource, and a dropped data source, take the same path.
	in := bp(keep)
	in.Resources[0].Drop = true
	in.DataSources = []blueprint.DataSource{{Key: "d", Name: "d", Drop: true}}

	_, report, err = FromBlueprint(in)
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}
	if report.Omitted != 2 || report.Resources != 0 || report.DataSources != 0 {
		t.Errorf("report = %+v, want 2 omitted and nothing exported", report)
	}
}

func TestUnit_Spec_ReportRendering(t *testing.T) {
	t.Parallel()

	var r Report
	r.Resources = 1
	r.Attributes = 3
	r.Omitted = 2
	r.add(SeverityInfo, "z", "an info note")
	r.add(SeverityDropped, "b", "a dropped note")
	r.add(SeverityLossy, "a", "a lossy note")
	r.add(SeverityDropped, "a", "another dropped note")

	// Most serious first, then by path, so the report of a given blueprint is
	// byte-stable and can be diffed by CI.
	sorted := r.Sorted()
	gotOrder := make([]string, 0, len(sorted))
	for _, n := range sorted {
		gotOrder = append(gotOrder, string(n.Severity)+":"+n.Path)
	}
	want := []string{"dropped:a", "dropped:b", "lossy:a", "info:z"}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Errorf("Sorted order = %v, want %v", gotOrder, want)
			break
		}
	}

	summary := r.Summary()
	for _, want := range []string{"1 resource(s)", "3 attribute(s)", "2 omitted", "2 dropped", "1 lossy", "1 info"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary %q is missing %q", summary, want)
		}
	}

	// MarshalJSON sorts too, so -report output is stable.
	data, err := r.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if !strings.Contains(string(data), `"severity":"dropped"`) {
		t.Errorf("encoded report looks wrong: %s", data)
	}

	// An empty report says so rather than saying nothing.
	var empty Report
	if empty.Lost() {
		t.Error("an empty report has lost nothing")
	}
	if !strings.Contains(empty.Summary(), "0 note(s)") {
		t.Errorf("empty summary = %q", empty.Summary())
	}
	if got := empty.Sorted(); len(got) != 0 {
		t.Errorf("Sorted on an empty report = %v", got)
	}
}

func TestUnit_Spec_NoteString(t *testing.T) {
	t.Parallel()

	got := Loss{Severity: SeverityDropped, Path: "resources[tag].binding", Message: "no counterpart"}.String()

	for _, want := range []string{"dropped", "resources[tag].binding", "no counterpart"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
}

func TestUnit_Spec_SeverityRank(t *testing.T) {
	t.Parallel()

	// An unknown severity sorts last rather than crashing or sorting first.
	if Severity("invented").rank() <= SeverityInfo.rank() {
		t.Error("an unknown severity should sort after the known ones")
	}
}

func TestUnit_Spec_PointerHelpers(t *testing.T) {
	t.Parallel()

	if strPtr("") != nil {
		t.Error("an empty string must become nil so the key is omitted")
	}
	if got := strPtr("x"); got == nil || *got != "x" {
		t.Errorf("strPtr(%q) = %v", "x", got)
	}
	if boolPtr(false) != nil {
		t.Error("false must become nil so the key is omitted")
	}
	if got := boolPtr(true); got == nil || !*got {
		t.Errorf("boolPtr(true) = %v", got)
	}
	if derefStr(nil) != "" || derefBool(nil) {
		t.Error("the deref helpers must tolerate nil")
	}
	if got := derefStr(strPtr("y")); got != "y" {
		t.Errorf("derefStr round trip = %q", got)
	}
	if !derefBool(boolPtr(true)) {
		t.Error("derefBool round trip failed")
	}
}

// TestUnit_Spec_NestedSDKTypeCrosses: associated_external_type is the one
// upstream field carrying part of the blueprint's binding, so a round trip can
// recover the SDK struct rather than asking a human to retype it.
func TestUnit_Spec_NestedSDKTypeCrosses(t *testing.T) {
	t.Parallel()

	s, _, err := FromBlueprint(bp(nested("items", blueprint.KindSetNested)))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	ext := s.Resources[0].Schema.Attributes[0].SetNested.NestedObject.AssociatedExternalType
	if ext == nil || ext.Type != "pkg.Item" {
		t.Errorf("the SDK type did not cross: %+v", ext)
	}

	// A nested shape with no SDK type must not invent an empty external type.
	bare := nested("items", blueprint.KindSetNested)
	bare.Type.NestedObject.SDKType = ""

	s, _, err = FromBlueprint(bp(bare))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}
	if got := s.Resources[0].Schema.Attributes[0].SetNested.NestedObject.AssociatedExternalType; got != nil {
		t.Errorf("an absent SDK type should leave the field nil, got %+v", got)
	}
}

// TestUnit_Spec_SingleNestedCarriesItsAttributes guards the one upstream
// irregularity: SingleNestedAttribute holds Attributes directly rather than a
// NestedObject, so a copy-paste from the list case would silently produce an
// attribute with no children.
func TestUnit_Spec_SingleNestedCarriesItsAttributes(t *testing.T) {
	t.Parallel()

	s, _, err := FromBlueprint(bp(nested("config", blueprint.KindSingleNested)))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	got := s.Resources[0].Schema.Attributes[0].SingleNested
	if got == nil {
		t.Fatal("single_nested did not cross")
	}
	if len(got.Attributes) != 1 || got.Attributes[0].Name != "id" {
		t.Errorf("attributes = %+v, want one named id", got.Attributes)
	}
	if got.AssociatedExternalType == nil {
		t.Error("single_nested lost its associated external type")
	}
}

// TestUnit_Spec_ResourcesAreSortedByExportedName: LoadDir sorts by blueprint
// key, which need not agree with the name the document carries. Sorting on the
// exported field is what makes the output independent of how the blueprint is split
// across files.
func TestUnit_Spec_ResourcesAreSortedByExportedName(t *testing.T) {
	t.Parallel()

	in := bp(attr("f", blueprint.KindString, blueprint.Optional))
	in.Resources = []blueprint.Resource{
		{
			Key: "aaa", Name: "zebra",
			Schema: blueprint.Schema{
				Attributes: []blueprint.Attribute{attr("f", blueprint.KindString, blueprint.Optional)},
			},
		},
		{
			Key: "zzz", Name: "antelope",
			Schema: blueprint.Schema{
				Attributes: []blueprint.Attribute{attr("f", blueprint.KindString, blueprint.Optional)},
			},
		},
	}

	s, _, err := FromBlueprint(in)
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	if s.Resources[0].Name != "antelope" || s.Resources[1].Name != "zebra" {
		t.Errorf("resources = %q, %q; want antelope then zebra",
			s.Resources[0].Name, s.Resources[1].Name)
	}
}
