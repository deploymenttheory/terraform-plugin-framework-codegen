package render

import (
	"fmt"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// maxNestDepth is how deep a nested attribute may go.
//
// One level is emitted today. Deeper nesting is refused rather than emitted
// wrongly: each level needs its own model type, attr.Type map and conversion
// helper, and a partially-correct nested mapping is the kind of bug that only
// shows up as a diff a practitioner cannot resolve. Raising this is a deliberate
// piece of work, not a constant change.
const maxNestDepth = 1

// nestedShapes collects every nested object shape a resource declares, in
// declaration order so output does not depend on map iteration.
func nestedShapes(r blueprint.Resource) ([]nestedShape, error) {
	var out []nestedShape

	for _, a := range r.Attributes {
		if a.Drop || !a.Type.Kind.IsNested() {
			continue
		}
		if a.Type.NestedObject == nil {
			return nil, &ErrUnsupported{
				What: fmt.Sprintf("attribute %q of resource %q", a.Name, r.Key),
				Why:  "a nested kind needs a nested object shape",
			}
		}

		// Depth is checked here rather than while rendering, so the error names the
		// attribute instead of surfacing as a confusing type mismatch later.
		if err := checkDepth(r.Key, a.Name, *a.Type.NestedObject, 1); err != nil {
			return nil, err
		}

		out = append(out, nestedShape{attr: a, nested: *a.Type.NestedObject})
	}

	return out, nil
}

type nestedShape struct {
	attr   blueprint.Attribute
	nested blueprint.NestedAttributeObject
}

func checkDepth(resourceKey, path string, n blueprint.NestedAttributeObject, depth int) error {
	for _, child := range n.Attributes {
		if child.Drop || !child.Type.Kind.IsNested() {
			continue
		}
		if depth+1 > maxNestDepth {
			return &ErrUnsupported{
				What: fmt.Sprintf("attribute %q of resource %q", path+"."+child.Name, resourceKey),
				Why: fmt.Sprintf("nesting is %d level(s) deep and the emitter supports %d; "+
					"flatten the shape or extend the emitter deliberately", depth+1, maxNestDepth),
			}
		}
		if child.Type.NestedObject != nil {
			if err := checkDepth(
				resourceKey,
				path+"."+child.Name,
				*child.Type.NestedObject,
				depth+1,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

// nestedAttributeDecl renders a nested attribute's schema declaration.
func nestedAttributeDecl(a blueprint.Attribute, imports *importSet) (string, error) {
	n := a.Type.NestedObject

	var children []string
	for _, child := range n.Attributes {
		if child.Drop {
			continue
		}
		schemaType, ok := frameworkSchemaType[child.Type.Kind]
		if !ok {
			return "", &ErrUnsupported{
				What: fmt.Sprintf("nested attribute %q", a.Name+"."+child.Name),
				Why:  fmt.Sprintf("type kind %q has no framework mapping", child.Type.Kind),
			}
		}
		decl, err := attributeDecl(child, schemaType, imports)
		if err != nil {
			return "", err
		}
		children = append(children, decl)
	}

	var b strings.Builder

	fmt.Fprintf(&b, "%q: schema.%s{\n", a.Name, frameworkSchemaType[a.Type.Kind])

	// A single nested attribute holds its children directly; a collection wraps
	// them in a nested-object descriptor.
	if a.Type.Kind == blueprint.KindSingleNested {
		b.WriteString("Attributes: map[string]schema.Attribute{\n")
		for _, c := range children {
			fmt.Fprintf(&b, "%s,\n", c)
		}
		b.WriteString("},\n")
	} else {
		b.WriteString(
			"NestedObject: schema.NestedAttributeObject{\nAttributes: map[string]schema.Attribute{\n",
		)
		for _, c := range children {
			fmt.Fprintf(&b, "%s,\n", c)
		}
		b.WriteString("},\n},\n")
	}

	writeAttributeFlags(&b, a)

	if a.MarkdownDescription != "" {
		fmt.Fprintf(&b, "MarkdownDescription: %s,\n", goStringLit(a.MarkdownDescription))
	}

	b.WriteString("}")

	return b.String(), nil
}

// nestedModelView builds the sibling model, attr.Type map and object type for one
// nested shape.
func nestedModelView(s nestedShape) (NestedModelView, error) {
	v := NestedModelView{
		GoTypeName:    s.nested.GoTypeName,
		AttrTypesVar:  s.nested.AttrTypesVar,
		ObjectTypeVar: s.nested.ObjectTypeVar,
	}

	for _, child := range s.nested.Attributes {
		if child.Drop {
			continue
		}

		modelType, ok := frameworkModelType[child.Type.Kind]
		if !ok {
			return NestedModelView{}, &ErrUnsupported{
				What: fmt.Sprintf("nested attribute %q", child.Name),
				Why:  fmt.Sprintf("type kind %q has no model mapping", child.Type.Kind),
			}
		}
		v.Fields = append(
			v.Fields,
			fmt.Sprintf("%s %s `tfsdk:%q`", child.GoField, modelType, child.Name),
		)

		attrType, err := attrTypeExpr(child.Type)
		if err != nil {
			return NestedModelView{}, err
		}
		v.AttrTypeEntries = append(v.AttrTypeEntries, fmt.Sprintf("%q: %s,", child.Name, attrType))
	}

	return v, nil
}

// attrTypeExpr renders the attr.Type expression for a type, which the attr.Type
// map of a nested shape needs.
func attrTypeExpr(t blueprint.AttrType) (string, error) {
	if expr, ok := frameworkElemType[t.Kind]; ok {
		return expr, nil
	}

	switch t.Kind {
	case blueprint.KindList, blueprint.KindSet:
		if t.ElementType == nil {
			return "", &ErrUnsupported{What: "collection", Why: "no element type"}
		}
		elem, err := attrTypeExpr(*t.ElementType)
		if err != nil {
			return "", err
		}
		container := "ListType"
		if t.Kind == blueprint.KindSet {
			container = "SetType"
		}
		return fmt.Sprintf("types.%s{ElemType: %s}", container, elem), nil

	case blueprint.KindMap:
		if t.ElementType == nil {
			return "", &ErrUnsupported{What: "map", Why: "no element type"}
		}
		elem, err := attrTypeExpr(*t.ElementType)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("types.MapType{ElemType: %s}", elem), nil

	default:
		return "", &ErrUnsupported{
			What: fmt.Sprintf("type kind %q", t.Kind),
			Why:  "it has no attr.Type expression; nesting inside a nested object is not supported",
		}
	}
}

// nestedExpandView builds the helper converting a framework collection into SDK
// structs.
func nestedExpandView(s nestedShape) NestedFuncView {
	v := NestedFuncView{
		FuncName:      s.nested.ExpandFunc,
		FrameworkType: frameworkModelType[s.attr.Type.Kind],
		SDKType:       s.nested.SDKType,
		ObjectTypeVar: s.nested.ObjectTypeVar,
		ModelType:     s.nested.GoTypeName,
		IsCollection:  s.attr.Type.Kind.IsNestedCollection(),
	}

	for _, child := range s.nested.Attributes {
		if child.Drop || child.Wire.SkipExpand || child.Wire.Expand == nil {
			continue
		}

		call := *child.Wire.Expand
		if call.ReturnsError {
			// A child conversion that can fail makes the whole helper fallible.
			v.NeedsDiagnostics = true
			v.Assignments = append(v.Assignments, fmt.Sprintf(
				"item.%s, d = %s\ndiags.Append(d...)",
				child.Wire.SDKField, convertExpr(call, "m."+child.GoField)))
			continue
		}
		v.Assignments = append(v.Assignments, fmt.Sprintf("item.%s = %s",
			child.Wire.SDKField, convertExpr(call, "m."+child.GoField)))
	}

	return v
}

// nestedFlattenView builds the helper converting SDK structs into a framework
// collection.
func nestedFlattenView(s nestedShape) NestedFuncView {
	v := NestedFuncView{
		FuncName:      s.nested.FlattenFunc,
		FrameworkType: frameworkModelType[s.attr.Type.Kind],
		SDKType:       s.nested.SDKType,
		ObjectTypeVar: s.nested.ObjectTypeVar,
		ModelType:     s.nested.GoTypeName,
		IsCollection:  s.attr.Type.Kind.IsNestedCollection(),
	}

	for _, child := range s.nested.Attributes {
		if child.Drop || child.Wire.SkipFlatten || child.Wire.Flatten == nil {
			continue
		}

		call := *child.Wire.Flatten
		if call.ReturnsError {
			v.NeedsDiagnostics = true
			v.Assignments = append(v.Assignments, fmt.Sprintf(
				"m.%s, d = %s\ndiags.Append(d...)",
				child.GoField, convertExpr(call, "item."+child.Wire.SDKField)))
			continue
		}
		v.Assignments = append(v.Assignments, fmt.Sprintf("m.%s = %s",
			child.GoField, convertExpr(call, "item."+child.Wire.SDKField)))
	}

	return v
}
