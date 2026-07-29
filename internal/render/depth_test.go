package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// deepSchema builds a schema nested `levels` deep, every level a set of objects.
//
// Level n is named lN, its model is LNModel, and its helpers are expandLN/flattenLN. The
// innermost level carries one scalar so the leaf is not an empty object, which validation
// refuses.
func deepSchema(levels int) blueprint.Schema {
	fallible := func(f string) *blueprint.ConvertCall {
		return &blueprint.ConvertCall{Func: f, NeedsCtx: true, ReturnsError: true}
	}

	// Built inside out, so each level's wire can name the level below it.
	inner := []blueprint.Attribute{{
		Name: "leaf", GoField: "Leaf",
		Type:                     blueprint.AttrType{Kind: blueprint.KindString},
		ComputedOptionalRequired: blueprint.Optional,
		Wire: blueprint.WireBinding{
			JSONPath: "leaf", SDKField: "Leaf", SDKGoType: "*string",
			Expand:  &blueprint.ConvertCall{Func: "convert.FrameworkToPtrString"},
			Flatten: &blueprint.ConvertCall{Func: "convert.PtrStringToFramework"},
		},
	}}

	for n := levels; n >= 1; n-- {
		name := fmt.Sprintf("l%d", n)
		up := strings.ToUpper(name)

		inner = []blueprint.Attribute{{
			Name: name, GoField: up,
			Type: blueprint.AttrType{
				Kind: blueprint.KindSetNested,
				NestedObject: &blueprint.NestedAttributeObject{
					GoTypeName:    up + "Model",
					SDKType:       "sdk." + up,
					AttrTypesVar:  name + "AttrTypes",
					ObjectTypeVar: name + "ObjectType",
					ExpandFunc:    "expand" + up,
					FlattenFunc:   "flatten" + up,
					Attributes:    inner,
				},
			},
			ComputedOptionalRequired: blueprint.Optional,
			Wire: blueprint.WireBinding{
				JSONPath: name, SDKField: up, SDKGoType: "[]sdk." + up,
				Expand:  fallible("expand" + up),
				Flatten: fallible("flatten" + up),
			},
		}}
	}

	return blueprint.Schema{Attributes: inner}
}

// TestUnit_Render_NestingIsGeneratedAtAnyDepth is the phase's central claim.
//
// One shape per level, in pre-order, each with its own model, attr.Type map and helper
// pair. Before this phase the emitter refused anything past one level.
func TestUnit_Render_NestingIsGeneratedAtAnyDepth(t *testing.T) {
	t.Parallel()

	const levels = 4

	shapes, err := nestedShapes(testResourceScope, deepSchema(levels))
	if err != nil {
		t.Fatalf("nestedShapes at %d levels: %v", levels, err)
	}
	if len(shapes) != levels {
		t.Fatalf("got %d shapes, want one per level (%d)", len(shapes), levels)
	}

	// Pre-order: the outermost object first, so a reader meets the schema top down.
	for i, sh := range shapes {
		wantPath := "l1"
		for n := 2; n <= i+1; n++ {
			wantPath += fmt.Sprintf(".l%d", n)
		}
		if sh.path != wantPath {
			t.Errorf("shape %d path = %q, want %q", i, sh.path, wantPath)
		}
	}

	// Each level's model names the level below through its object type var, rather than
	// restating the shape.
	for i, sh := range shapes[:len(shapes)-1] {
		nm, err := nestedModelView(sh)
		if err != nil {
			t.Fatalf("nestedModelView(%s): %v", sh.path, err)
		}

		child := fmt.Sprintf("l%dObjectType", i+2)
		joined := strings.Join(nm.AttrTypeEntries, "\n")
		if !strings.Contains(joined, child) {
			t.Errorf("level %d attr.Type map should refer to %s:\n%s", i+1, child, joined)
		}
	}

	// The leaf level has no nested child and so refers to no object type var.
	leaf, err := nestedModelView(shapes[len(shapes)-1])
	if err != nil {
		t.Fatalf("nestedModelView(leaf): %v", err)
	}
	if strings.Contains(strings.Join(leaf.AttrTypeEntries, "\n"), "ObjectType") {
		t.Errorf("the leaf level should refer to no object type var: %v", leaf.AttrTypeEntries)
	}
}

// TestUnit_Render_DeepExpandAndFlattenChain checks both directions at depth.
//
// Each level's helper must call the level below it. The flatten direction is proved by the
// pilot's data source; the expand direction has no pilot coverage, because the tag API has
// nothing writable nested two deep.
func TestUnit_Render_DeepExpandAndFlattenChain(t *testing.T) {
	t.Parallel()

	shapes, err := nestedShapes(testResourceScope, deepSchema(3))
	if err != nil {
		t.Fatalf("nestedShapes: %v", err)
	}

	for i, sh := range shapes[:len(shapes)-1] {
		below := fmt.Sprintf("L%d", i+2)

		ev := nestedExpandView(sh)
		if got := strings.Join(ev.Assignments, "\n"); !strings.Contains(got, "expand"+below) {
			t.Errorf("level %d expand should call expand%s:\n%s", i+1, below, got)
		}
		if !ev.NeedsDiagnostics {
			t.Errorf("level %d expand calls a fallible helper, so it must carry diagnostics", i+1)
		}

		fv := nestedFlattenView(sh)
		if got := strings.Join(fv.Assignments, "\n"); !strings.Contains(got, "flatten"+below) {
			t.Errorf("level %d flatten should call flatten%s:\n%s", i+1, below, got)
		}
	}
}

// TestUnit_Render_DeepSchemaDeclarationRecurses checks the schema literal itself, which
// already recursed before this phase but was unreachable past one level.
func TestUnit_Render_DeepSchemaDeclarationRecurses(t *testing.T) {
	t.Parallel()

	imports := newImportSet()

	decl, err := nestedAttributeDecl(testResourceScope, deepSchema(3).Attributes[0], imports)
	if err != nil {
		t.Fatalf("nestedAttributeDecl: %v", err)
	}

	// Three NestedObject wrappers, one per level.
	if got := strings.Count(decl, "schema.NestedAttributeObject{"); got != 3 {
		t.Errorf("got %d nested object literals, want 3:\n%s", got, decl)
	}
	if !strings.Contains(decl, `"leaf"`) {
		t.Errorf("the innermost attribute should reach the declaration:\n%s", decl)
	}
}

// TestUnit_Render_NestingBeyondTheCeilingIsRefusedByName covers the runaway guard.
//
// The ceiling is above any fixed schema in the reference provider. What exceeds it is a
// schema whose depth is decided at runtime, which this IR cannot express, so the message
// says to write that resource by hand rather than suggesting a flatter shape.
func TestUnit_Render_NestingBeyondTheCeilingIsRefusedByName(t *testing.T) {
	t.Parallel()

	_, err := nestedShapes(testResourceScope, deepSchema(maxNestDepth+1))
	if err == nil {
		t.Fatalf("nesting %d levels deep must be refused", maxNestDepth+1)
	}

	msg := err.Error()
	// The attribute is named, so the reader knows where to look.
	if !strings.Contains(msg, fmt.Sprintf("l%d", maxNestDepth+1)) {
		t.Errorf("the error should name the offending attribute: %v", msg)
	}
	if !strings.Contains(msg, "by hand") {
		t.Errorf("the error should say what to do instead: %v", msg)
	}

	// And the ceiling itself is generated, not refused.
	if _, err := nestedShapes(testResourceScope, deepSchema(maxNestDepth)); err != nil {
		t.Errorf("exactly %d levels must be supported: %v", maxNestDepth, err)
	}
}

// TestUnit_Render_TwoNestedObjectsMayNotShareAnIdentifier is the collision arbitrary depth
// makes likely.
//
// Every nested object declares a package-level model, attr.Type map, object type var and
// helper pair. At one level a repeat was unlikely; at four, two objects called "ItemModel"
// is an easy mistake, and it emits two declarations of the same name.
func TestUnit_Render_TwoNestedObjectsMayNotShareAnIdentifier(t *testing.T) {
	t.Parallel()

	s := deepSchema(2)

	// Make the inner object claim the outer one's model name.
	outer := s.Attributes[0].Type.NestedObject
	outer.Attributes[0].Type.NestedObject.GoTypeName = outer.GoTypeName

	_, err := nestedShapes(testResourceScope, s)
	if err == nil {
		t.Fatal("two nested objects sharing a goTypeName must be refused")
	}

	msg := err.Error()
	for _, want := range []string{"goTypeName", "L1Model", "l1.l2"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error should mention %q: %v", want, msg)
		}
	}
}

// TestUnit_Render_NestedAttributeCarriesItsValidators is a gap this phase closed.
//
// A nested attribute's validators and plan modifiers were dropped silently: the blueprint
// said the value was constrained and the generated provider did not enforce it. Silently
// is the operative word -- a refusal would have been fine.
func TestUnit_Render_NestedAttributeCarriesItsValidators(t *testing.T) {
	t.Parallel()

	a := deepSchema(1).Attributes[0]
	a.Validators = []blueprint.CustomCode{{
		SchemaDefinition: "setvalidator.SizeAtLeast(1)",
		Imports: []blueprint.Import{{
			Path: "github.com/hashicorp/terraform-plugin-framework-validators/setvalidator",
		}},
	}}

	imports := newImportSet()

	decl, err := nestedAttributeDecl(testResourceScope, a, imports)
	if err != nil {
		t.Fatalf("nestedAttributeDecl: %v", err)
	}

	if !strings.Contains(decl, "setvalidator.SizeAtLeast(1)") {
		t.Errorf("a declared validator must reach the schema:\n%s", decl)
	}
	// A set_nested attribute takes []validator.Set. The fallthrough used to make this
	// []validator.String, which does not compile against a SetNestedAttribute.
	if !strings.Contains(decl, "[]validator.Set{") {
		t.Errorf("a set_nested attribute's validators must be validator.Set:\n%s", decl)
	}
	if !strings.Contains(imports.render("irrelevant"), "setvalidator") {
		t.Error("a validator's own imports must be registered")
	}
}

// TestUnit_Render_ValidatorKindCoversTheNestedKinds pins the mapping directly.
//
// The default was String, so every nested kind silently rendered []validator.String.
func TestUnit_Render_ValidatorKindCoversTheNestedKinds(t *testing.T) {
	t.Parallel()

	want := map[blueprint.TypeKind]string{
		blueprint.KindListNested:   "List",
		blueprint.KindSetNested:    "Set",
		blueprint.KindSingleNested: "Object",
	}

	for kind, w := range want {
		if got := validatorKind(kind); got != w {
			t.Errorf("validatorKind(%q) = %q, want %q", kind, got, w)
		}
	}
}
