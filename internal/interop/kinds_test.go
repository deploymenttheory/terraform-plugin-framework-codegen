package interop

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-codegen-spec/datasource"
	"github.com/hashicorp/terraform-plugin-codegen-spec/resource"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// everyKind is one blueprint attribute per kind the IR can express, each carrying a
// validator, a plan modifier and a custom default.
//
// This table exists because the two per-kind switches are the bulk of the package
// and most of their branches are unreachable from any committed blueprint: the pilot
// has no int32, no number, no map and no single-nested attribute. Without synthetic
// coverage the wrappers would be written once, never executed, and wrong in a way
// only a future provider would discover -- a set attribute silently carrying list
// validators, say, which compiles and validates and is still wrong.
//
// A custom default rather than a static one, because every kind accepts a custom
// default while only four accept a static one. Static defaults are covered
// separately in TestUnit_Interop_Defaults.
func everyKind() []blueprint.Attribute {
	kinds := []blueprint.TypeKind{
		blueprint.KindBool, blueprint.KindString,
		blueprint.KindInt32, blueprint.KindInt64,
		blueprint.KindFloat32, blueprint.KindFloat64,
		blueprint.KindNumber,
		blueprint.KindList, blueprint.KindSet, blueprint.KindMap,
		blueprint.KindListNested, blueprint.KindSetNested, blueprint.KindSingleNested,
	}

	out := make([]blueprint.Attribute, 0, len(kinds))

	for _, k := range kinds {
		a := blueprint.Attribute{
			// Names are derived from the kind so a failure names the branch.
			Name:                     string(k) + "_field",
			GoField:                  "Field",
			ComputedOptionalRequired: blueprint.ComputedOptional,
			MarkdownDescription:      "a " + string(k),
			DeprecationMessage:       "deprecated",
			Sensitive:                true,
			Type:                     blueprint.AttrType{Kind: k},
			Validators: []blueprint.CustomCode{{
				SchemaDefinition: "validators.Something()",
				Imports:          []blueprint.Import{{Path: "example.com/validators"}},
			}},
			PlanModifiers: []blueprint.CustomCode{{
				SchemaDefinition: "planmodifiers.Something()",
				Imports:          []blueprint.Import{{Path: "example.com/planmodifiers", Alias: "pm"}},
			}},
			Default: &blueprint.Default{Custom: &blueprint.CustomCode{
				SchemaDefinition: "defaults.Something()",
			}},
			Wire: blueprint.WireBinding{JSONPath: string(k)},
		}

		switch {
		case k.IsCollection():
			a.Type.ElementType = &blueprint.AttrType{Kind: blueprint.KindString}
		case k.IsNested():
			a.Type.NestedObject = &blueprint.NestedAttributeObject{
				GoTypeName: "M", SDKType: "pkg.M",
				AttrTypesVar: "mAttrTypes", ObjectTypeVar: "mObjectType",
				ExpandFunc: "expandM", FlattenFunc: "flattenM",
				Attributes: []blueprint.Attribute{
					// A nested scalar plus a nested collection, so the recursive
					// path is exercised at both shapes.
					attr("id", blueprint.KindString, blueprint.Required),
					collection("tags", blueprint.KindSet, blueprint.KindString),
				},
			}
		}

		out = append(out, a)
	}

	return out
}

// TestUnit_Interop_EveryKindExportsAsAResource drives all thirteen branches of the
// resource switch, with validators, modifiers and defaults attached to each.
func TestUnit_Interop_EveryKindExportsAsAResource(t *testing.T) {
	t.Parallel()

	attrs := everyKind()

	s, report, err := FromBlueprint(bp(attrs...))
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	if report.Attributes != len(attrs) {
		t.Errorf("exported %d attributes, want %d", report.Attributes, len(attrs))
	}

	data, err := Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := Validate(context.Background(), data); err != nil {
		t.Fatalf("every-kind export is not valid: %v\n%s", err, data)
	}

	// Every attribute must have landed in exactly one pointer field, carried its
	// validator and modifier, and kept its default. A branch that forgot one of
	// those still produces a valid document, which is exactly why this is asserted
	// rather than left to the schema check.
	for i, got := range s.Resources[0].Schema.Attributes {
		kind := attrs[i].Type.Kind

		t.Run(string(kind), func(t *testing.T) {
			validators, modifiers, hasDefault := resourceExtras(got)

			if validators != 1 {
				t.Errorf("%s: carried %d validators, want 1", kind, validators)
			}
			if modifiers != 1 {
				t.Errorf("%s: carried %d plan modifiers, want 1", kind, modifiers)
			}
			if !hasDefault {
				t.Errorf("%s: lost its custom default", kind)
			}
		})
	}
}

// resourceExtras counts the validators and plan modifiers an exported attribute
// carried, and whether it kept a default.
//
// Written out per kind rather than with reflection, because the risk this test
// exists to catch is a branch reading from the wrong kind's field -- a set attribute
// given list validators, say. Reflection over "whichever field happens to be set"
// cannot distinguish that from correct behavior, so it would make the test agree
// with any implementation.
func resourceExtras(a resource.Attribute) (validators, modifiers int, hasDefault bool) {
	switch {
	case a.Bool != nil:
		return len(a.Bool.Validators), len(a.Bool.PlanModifiers), a.Bool.Default != nil
	case a.String != nil:
		return len(a.String.Validators), len(a.String.PlanModifiers), a.String.Default != nil
	case a.Int64 != nil:
		return len(a.Int64.Validators), len(a.Int64.PlanModifiers), a.Int64.Default != nil
	case a.Float64 != nil:
		return len(a.Float64.Validators), len(a.Float64.PlanModifiers), a.Float64.Default != nil
	case a.Number != nil:
		return len(a.Number.Validators), len(a.Number.PlanModifiers), a.Number.Default != nil
	case a.List != nil:
		return len(a.List.Validators), len(a.List.PlanModifiers), a.List.Default != nil
	case a.Set != nil:
		return len(a.Set.Validators), len(a.Set.PlanModifiers), a.Set.Default != nil
	case a.Map != nil:
		return len(a.Map.Validators), len(a.Map.PlanModifiers), a.Map.Default != nil
	case a.ListNested != nil:
		return len(a.ListNested.Validators), len(a.ListNested.PlanModifiers), a.ListNested.Default != nil
	case a.SetNested != nil:
		return len(a.SetNested.Validators), len(a.SetNested.PlanModifiers), a.SetNested.Default != nil
	case a.SingleNested != nil:
		return len(a.SingleNested.Validators), len(a.SingleNested.PlanModifiers), a.SingleNested.Default != nil
	default:
		return -1, -1, false
	}
}

// TestUnit_Interop_EveryKindExportsAsADataSource drives the datasource switch.
//
// The same thirteen kinds, and the same assertion that each landed in exactly one
// field with its validator. Plan modifiers and defaults are expected to be *absent*
// here -- the datasource package has no such fields -- and reported as losses, which
// is asserted rather than assumed.
func TestUnit_Interop_EveryKindExportsAsADataSource(t *testing.T) {
	t.Parallel()

	attrs := everyKind()

	in := bp()
	in.Resources = nil
	in.DataSources = []blueprint.DataSource{{
		Key: "thing", TerraformType: "example_thing", Attributes: attrs,
	}}

	s, report, err := FromBlueprint(in)
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	data, err := Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := Validate(context.Background(), data); err != nil {
		t.Fatalf("every-kind data source export is not valid: %v\n%s", err, data)
	}

	for i, got := range s.DataSources[0].Schema.Attributes {
		kind := attrs[i].Type.Kind

		if n := datasourceValidators(got); n != 1 {
			t.Errorf("%s: carried %d validators, want 1", kind, n)
		}
	}

	// One loss note per attribute for the modifier and one for the default: these
	// are selective rather than uniform, since a blueprint need not carry either.
	modifiers, defaults := 0, 0
	for _, n := range report.Notes {
		if strings.HasSuffix(n.Path, ".planModifiers") {
			modifiers++
		}
		if strings.HasSuffix(n.Path, ".default") {
			defaults++
		}
	}
	if modifiers != len(attrs) || defaults != len(attrs) {
		t.Errorf("got %d modifier notes and %d default notes, want %d of each",
			modifiers, defaults, len(attrs))
	}
}

func datasourceValidators(a datasource.Attribute) int {
	switch {
	case a.Bool != nil:
		return len(a.Bool.Validators)
	case a.String != nil:
		return len(a.String.Validators)
	case a.Int64 != nil:
		return len(a.Int64.Validators)
	case a.Float64 != nil:
		return len(a.Float64.Validators)
	case a.Number != nil:
		return len(a.Number.Validators)
	case a.List != nil:
		return len(a.List.Validators)
	case a.Set != nil:
		return len(a.Set.Validators)
	case a.Map != nil:
		return len(a.Map.Validators)
	case a.ListNested != nil:
		return len(a.ListNested.Validators)
	case a.SetNested != nil:
		return len(a.SetNested.Validators)
	case a.SingleNested != nil:
		return len(a.SingleNested.Validators)
	default:
		return -1
	}
}

// TestUnit_Interop_ElementTypesOfEveryScalar covers the element-type walk, which is
// a separate switch from the attribute one and equally unreachable from the pilot.
func TestUnit_Interop_ElementTypesOfEveryScalar(t *testing.T) {
	t.Parallel()

	scalars := []blueprint.TypeKind{
		blueprint.KindBool, blueprint.KindString,
		blueprint.KindInt32, blueprint.KindInt64,
		blueprint.KindFloat32, blueprint.KindFloat64,
		blueprint.KindNumber,
	}

	for _, elem := range scalars {
		t.Run(string(elem), func(t *testing.T) {
			t.Parallel()

			s, _, err := FromBlueprint(bp(
				collection("list_field", blueprint.KindList, elem),
				collection("set_field", blueprint.KindSet, elem),
				collection("map_field", blueprint.KindMap, elem),
			))
			if err != nil {
				t.Fatalf("FromBlueprint: %v", err)
			}

			data, err := Marshal(s)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if err := Validate(context.Background(), data); err != nil {
				t.Errorf("a collection of %s is not valid: %v\n%s", elem, err, data)
			}
		})
	}
}

// TestUnit_Interop_NestedObjectInAnElementType covers the branch that flattens a
// nested shape reached through Elem into a plain object type.
//
// The blueprint does not produce this today -- nested shapes live on the attribute,
// not in Elem -- so the branch exists to report a real loss rather than panic if
// that ever changes. Testing it now is what makes that promise true.
func TestUnit_Interop_NestedObjectInAnElementType(t *testing.T) {
	t.Parallel()

	in := bp(blueprint.Attribute{
		Name: "rows", ComputedOptionalRequired: blueprint.Optional,
		Type: blueprint.AttrType{
			Kind: blueprint.KindList,
			ElementType: &blueprint.AttrType{
				Kind: blueprint.KindSingleNested,
				NestedObject: &blueprint.NestedAttributeObject{
					GoTypeName: "RowModel",
					Attributes: []blueprint.Attribute{
						attr("id", blueprint.KindString, blueprint.Required),
						attr("count", blueprint.KindInt64, blueprint.Optional),
						attr("ratio", blueprint.KindFloat64, blueprint.Optional),
						attr("exact", blueprint.KindNumber, blueprint.Optional),
						attr("on", blueprint.KindBool, blueprint.Optional),
					},
				},
			},
		},
	})

	s, report, err := FromBlueprint(in)
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	obj := s.Resources[0].Schema.Attributes[0].List.ElementType.Object
	if obj == nil {
		t.Fatal("the nested shape did not become an object type")
	}
	if len(obj.AttributeTypes) != 5 {
		t.Errorf("object carries %d attribute types, want 5", len(obj.AttributeTypes))
	}

	// Losing per-field presence and documentation is exactly the kind of thing that
	// must not happen quietly.
	found := false
	for _, n := range report.Notes {
		if n.Severity == SeverityLossy && strings.Contains(n.Message, "object type") {
			found = true
		}
	}
	if !found {
		t.Errorf("flattening to an object type must be reported as lossy:\n%v", report.Sorted())
	}

	data, err := Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := Validate(context.Background(), data); err != nil {
		t.Errorf("the flattened object type is not valid: %v\n%s", err, data)
	}
}

func TestUnit_Interop_ObjectTypeRefusals(t *testing.T) {
	t.Parallel()

	// A dropped child inside a flattened object is omitted and counted.
	in := bp(blueprint.Attribute{
		Name: "rows", ComputedOptionalRequired: blueprint.Optional,
		Type: blueprint.AttrType{
			Kind: blueprint.KindList,
			ElementType: &blueprint.AttrType{
				Kind: blueprint.KindSingleNested,
				NestedObject: &blueprint.NestedAttributeObject{
					GoTypeName: "RowModel",
					Attributes: []blueprint.Attribute{
						attr("id", blueprint.KindString, blueprint.Required),
						{
							Name: "gone", ComputedOptionalRequired: blueprint.Optional, Drop: true,
							Type: blueprint.AttrType{Kind: blueprint.KindString},
						},
					},
				},
			},
		},
	})

	s, report, err := FromBlueprint(in)
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}
	if got := len(s.Resources[0].Schema.Attributes[0].List.ElementType.Object.AttributeTypes); got != 1 {
		t.Errorf("object carries %d attribute types, want 1", got)
	}
	if report.Omitted != 1 {
		t.Errorf("Omitted = %d, want 1", report.Omitted)
	}

	// A kind with no object-type counterpart is refused rather than guessed at.
	bad := bp(blueprint.Attribute{
		Name: "rows", ComputedOptionalRequired: blueprint.Optional,
		Type: blueprint.AttrType{
			Kind: blueprint.KindList,
			ElementType: &blueprint.AttrType{
				Kind: blueprint.KindSingleNested,
				NestedObject: &blueprint.NestedAttributeObject{
					GoTypeName: "RowModel",
					Attributes: []blueprint.Attribute{
						collection("tags", blueprint.KindSet, blueprint.KindString),
					},
				},
			},
		},
	})

	if _, _, err := FromBlueprint(bad); err == nil {
		t.Error("a collection inside a flattened object type must be refused")
	}

	// And a nested kind in Elem with no shape at all.
	empty := bp(blueprint.Attribute{
		Name: "rows", ComputedOptionalRequired: blueprint.Optional,
		Type: blueprint.AttrType{
			Kind:        blueprint.KindList,
			ElementType: &blueprint.AttrType{Kind: blueprint.KindSingleNested},
		},
	})

	if _, _, err := FromBlueprint(empty); err == nil {
		t.Error("a nested element with no object shape must be refused")
	}
}

// TestUnit_Interop_ResourceLossesAreSelective: the resource-level sections are
// reported only when populated, for the same reason Behavior is.
func TestUnit_Interop_ResourceLossesAreSelective(t *testing.T) {
	t.Parallel()

	bare := bp(attr("f", blueprint.KindString, blueprint.Optional))

	_, quiet, err := FromBlueprint(bare)
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	for _, n := range quiet.Notes {
		for _, unwanted := range []string{".policy", ".import", ".timeouts", ".docRefUrl"} {
			if strings.HasSuffix(n.Path, unwanted) {
				t.Errorf("an empty %s should be silent, got %v", unwanted, n)
			}
		}
	}

	full := bp(attr("f", blueprint.KindString, blueprint.Optional))
	full.Resources[0].Policy = blueprint.ResourcePolicy{
		UpdateStyle: blueprint.UpdatePutFull,
		ReadBack:    blueprint.ReadBack{Enabled: true},
		Delete:      blueprint.Delete{NotFoundIsSuccess: true},
	}
	full.Resources[0].Import = blueprint.ImportPolicy{Style: blueprint.ImportPassthroughID, Attribute: "id"}
	full.Resources[0].Timeouts = blueprint.Timeouts{CreateSeconds: 30}
	full.Resources[0].DocRefURL = "https://example.com"
	full.Source = blueprint.SourceInfo{SpecFile: "api.yaml"}
	full.Provider.SDK = blueprint.SDKModule{ModulePath: "example.com/sdk"}
	full.Provider.GoModule = "example.com/provider"
	full.Provider.Conventions = blueprint.Conventions{ResourceRoot: "internal/services"}
	full.Provider.Support = blueprint.SupportPkgs{Convert: blueprint.Import{Path: "example.com/convert"}}

	_, loud, err := FromBlueprint(full)
	if err != nil {
		t.Fatalf("FromBlueprint: %v", err)
	}

	for _, want := range []string{
		".policy.updateStyle", ".policy.readBack", ".policy.delete",
		".import", ".timeouts", ".docRefUrl",
		"provider.sdk", "provider.goModule", "provider.conventions", "provider.support",
		"source",
	} {
		found := false
		for _, n := range loud.Notes {
			if strings.HasSuffix(n.Path, want) || n.Path == want {
				found = true
			}
		}
		if !found {
			t.Errorf("a populated %s must be reported:\n%v", want, loud.Sorted())
		}
	}
}
