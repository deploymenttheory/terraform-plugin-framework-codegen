package emit

import (
	"fmt"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// How a nested attribute crosses between terraform and the generated API
// client, in both directions: a single object, a list of objects, or a map
// of objects. The three differ only in how the collection is walked and
// rebuilt, which is why they sit together.

// constructNested renders a nested object, list-of-objects or
// map-of-objects write.
func constructNested(namer *modelNamer, path string, n node, source, destination, attrPath string, depth int) (string, bool, error) {
	indent := strings.Repeat("\t", depth)
	field := source + "." + ir.GoName(n.attribute.Name)
	// Construction builds the type the setter takes. That is usually the
	// same as the getter's, and is not when the SDK emits one model per
	// direction.
	writeType := n.fb.Access.SDKType
	if n.fb.Access.SDKWriteType != "" {
		writeType = n.fb.Access.SDKWriteType
	}
	// A collection's element type is what the constructed value has to
	// become: the element of a list, the value of a map.
	elemType := strings.TrimPrefix(writeType, "[]")
	if index := strings.Index(elemType, "]"); strings.HasPrefix(elemType, "map[") && index > 0 {
		elemType = elemType[index+1:]
	}
	// A concrete element type takes the constructed value dereferenced;
	// an interface or pointer element takes the constructor's pointer.
	deref := ""
	if elemType == n.fb.NestedWriteModel {
		deref = "*"
	}

	if n.attribute.Kind == ir.TypeList {
		listVar := lowerCamel(n.attribute.Name) + "List" + depthSuffix(depth)
		indexVar := "index" + depthSuffix(depth)
		elemVar := lowerCamel(n.attribute.Name) + "Element" + depthSuffix(depth)

		modelsVar := lowerCamel(n.attribute.Name) + "Models" + depthSuffix(depth)
		inner, _, err := constructLines(namer, path, n.children, modelsVar+"["+indexVar+"]", elemVar, attrPath, depth+2, false)
		if err != nil {
			return "", false, err
		}
		// No child is written, so there is nothing to build. Rendering the
		// loop anyway declares an index nothing reads, which does not
		// compile.
		if strings.TrimSpace(inner) == "" {
			return "", false, nil
		}

		var b strings.Builder
		// A null or unknown list writes nothing: the plan has no elements to
		// build from, and unknown is what a computed list carries before the
		// API has answered.
		fmt.Fprintf(&b, "%sif !%s.IsNull() && !%s.IsUnknown() {\n", indent, field, field)
		fmt.Fprintf(&b, "%s\tvar %s []%s\n", indent, modelsVar, namer.name(path))
		fmt.Fprintf(&b, "%s\tif diags := %s.ElementsAs(ctx, &%s, false); diags.HasError() {\n", indent, field, modelsVar)
		fmt.Fprintf(&b, "%s\t\t%s\n%s\t}\n", indent, diagReturn(attrPath), indent)
		fmt.Fprintf(&b, "%s\t%s := make([]%s, 0, len(%s))\n", indent, listVar, elemType, modelsVar)
		fmt.Fprintf(&b, "%s\tfor %s := range %s {\n", indent, indexVar, modelsVar)
		fmt.Fprintf(&b, "%s\t\t%s := %s\n", indent, elemVar, n.fb.NestedConstructor)
		b.WriteString(inner)
		fmt.Fprintf(&b, "%s\t\t%s = append(%s, %s%s)\n", indent, listVar, listVar, deref, elemVar)
		fmt.Fprintf(&b, "%s\t}\n", indent)
		fmt.Fprintf(&b, "%s\t%s.%s(%s)\n", indent, destination, n.fb.Access.Set, listVar)
		fmt.Fprintf(&b, "%s}\n", indent)
		return b.String(), true, nil
	}

	if n.attribute.Kind == ir.TypeMap {
		mapVar := lowerCamel(n.attribute.Name) + "Map" + depthSuffix(depth)
		keyVar := "key" + depthSuffix(depth)
		elemVar := lowerCamel(n.attribute.Name) + "Element" + depthSuffix(depth)

		modelsVar := lowerCamel(n.attribute.Name) + "Models" + depthSuffix(depth)
		// A map value is not addressable, so the entry is copied to a local
		// the same way the state mapping copies one.
		entryVar := "entry" + depthSuffix(depth)
		inner, _, err := constructLines(namer, path, n.children, entryVar, elemVar, attrPath, depth+2, false)
		if err != nil {
			return "", false, err
		}
		// No child is written, so there is nothing to build. Rendering the
		// loop anyway declares a key nothing reads, which does not compile.
		if strings.TrimSpace(inner) == "" {
			return "", false, nil
		}

		var b strings.Builder
		// A null or unknown map writes nothing, for the same reason a list
		// does: neither carries entries to build from.
		fmt.Fprintf(&b, "%sif !%s.IsNull() && !%s.IsUnknown() {\n", indent, field, field)
		fmt.Fprintf(&b, "%s\tvar %s map[string]%s\n", indent, modelsVar, namer.name(path))
		fmt.Fprintf(&b, "%s\tif diags := %s.ElementsAs(ctx, &%s, false); diags.HasError() {\n", indent, field, modelsVar)
		fmt.Fprintf(&b, "%s\t\t%s\n%s\t}\n", indent, diagReturn(attrPath), indent)
		fmt.Fprintf(&b, "%s\t%s := make(map[string]%s, len(%s))\n", indent, mapVar, elemType, modelsVar)
		fmt.Fprintf(&b, "%s\tfor %s := range %s {\n", indent, keyVar, modelsVar)
		fmt.Fprintf(&b, "%s\t\t%s := %s[%s]\n", indent, entryVar, modelsVar, keyVar)
		fmt.Fprintf(&b, "%s\t\t%s := %s\n", indent, elemVar, n.fb.NestedConstructor)
		b.WriteString(inner)
		fmt.Fprintf(&b, "%s\t\t%s[%s] = %s%s\n", indent, mapVar, keyVar, deref, elemVar)
		fmt.Fprintf(&b, "%s\t}\n", indent)
		fmt.Fprintf(&b, "%s\t%s.%s(%s)\n", indent, destination, n.fb.Access.Set, mapVar)
		fmt.Fprintf(&b, "%s}\n", indent)
		return b.String(), true, nil
	}

	singleDeref := ""
	if writeType == n.fb.NestedWriteModel {
		singleDeref = "*"
	}
	nestedVar := lowerCamel(n.attribute.Name) + "Body" + depthSuffix(depth)
	modelVar := lowerCamel(n.attribute.Name) + "Model" + depthSuffix(depth)
	inner, _, err := constructLines(namer, path, n.children, modelVar, nestedVar, attrPath, depth+1, false)
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(inner) == "" {
		return "", false, nil
	}

	var b strings.Builder
	// A null or unknown object writes nothing: unknown is what a computed
	// object carries before the API has answered, and neither state has
	// fields to build from.
	fmt.Fprintf(&b, "%sif !%s.IsNull() && !%s.IsUnknown() {\n", indent, field, field)
	fmt.Fprintf(&b, "%s\tvar %s %s\n", indent, modelVar, namer.name(path))
	fmt.Fprintf(&b, "%s\tif diags := %s.As(ctx, &%s, basetypes.ObjectAsOptions{}); diags.HasError() {\n", indent, field, modelVar)
	fmt.Fprintf(&b, "%s\t\t%s\n%s\t}\n", indent, diagReturn(attrPath), indent)
	fmt.Fprintf(&b, "%s\t%s := %s\n", indent, nestedVar, n.fb.NestedConstructor)
	b.WriteString(inner)
	fmt.Fprintf(&b, "%s\t%s.%s(%s%s)\n", indent, destination, n.fb.Access.Set, singleDeref, nestedVar)
	fmt.Fprintf(&b, "%s}\n", indent)
	return b.String(), true, nil
}

// stateNested renders a nested object or list-of-objects read.
func stateNested(namer *modelNamer, path string, n node, source, destination string, depth int) (string, error) {
	indent := strings.Repeat("\t", depth)
	field := destination + "." + ir.GoName(n.attribute.Name)
	modelType := namer.name(path)

	if n.attribute.Kind == ir.TypeList {
		elementsVar := "elements" + depthSuffix(depth)
		indexVar := "index" + depthSuffix(depth)
		listVar := lowerCamel(n.attribute.Name) + "List" + depthSuffix(depth)
		elemVar := lowerCamel(n.attribute.Name) + "Element" + depthSuffix(depth)

		inner, err := stateLinesWith(namer, path, n.children, elementsVar+"["+indexVar+"]", elemVar, depth+2)
		if err != nil {
			return "", err
		}

		var b strings.Builder
		fmt.Fprintf(&b, "%sif %s := %s.%s(); %s != nil {\n", indent, elementsVar, source, n.fb.Access.Get, elementsVar)
		fmt.Fprintf(&b, "%s\t%s := make([]%s, 0, len(%s))\n", indent, listVar, modelType, elementsVar)
		fmt.Fprintf(&b, "%s\tfor %s := range %s {\n", indent, indexVar, elementsVar)
		fmt.Fprintf(&b, "%s\t\t%s := %s{}\n", indent, elemVar, modelType)
		b.WriteString(inner)
		fmt.Fprintf(&b, "%s\t\t%s = append(%s, %s)\n", indent, listVar, listVar, elemVar)
		fmt.Fprintf(&b, "%s\t}\n", indent)
		valueName := lowerCamel(n.attribute.Name) + "Value" + depthSuffix(depth)
		diagsName := lowerCamel(n.attribute.Name) + "Diags" + depthSuffix(depth)
		elemType := nestedObjectType(namer, path)
		fmt.Fprintf(&b, "%s\t%s, %s := types.ListValueFrom(ctx, %s, %s)\n",
			indent, valueName, diagsName, elemType, listVar)
		fmt.Fprintf(&b, "%s\tdiags.Append(%s...)\n", indent, diagsName)
		fmt.Fprintf(&b, "%s\t%s = %s\n", indent, field, valueName)
		fmt.Fprintf(&b, "%s} else {\n%s\t%s = types.ListNull(%s)\n%s}\n",
			indent, indent, field, elemType, indent)
		return b.String(), nil
	}

	if n.attribute.Kind == ir.TypeMap {
		entriesVar := "entries" + depthSuffix(depth)
		keyVar := "key" + depthSuffix(depth)
		mapVar := lowerCamel(n.attribute.Name) + "Map" + depthSuffix(depth)
		elemVar := lowerCamel(n.attribute.Name) + "Element" + depthSuffix(depth)

		// A map value is not addressable in Go, so a value-typed entry
		// cannot have a pointer method called on it where a slice element
		// could. The loop copies the entry to a local, which can.
		entryVar := "entry" + depthSuffix(depth)
		inner, err := stateLinesWith(namer, path, n.children, entryVar, elemVar, depth+2)
		if err != nil {
			return "", err
		}

		var b strings.Builder
		fmt.Fprintf(&b, "%sif %s := %s.%s(); %s != nil {\n", indent, entriesVar, source, n.fb.Access.Get, entriesVar)
		fmt.Fprintf(&b, "%s\t%s := make(map[string]%s, len(%s))\n", indent, mapVar, modelType, entriesVar)
		fmt.Fprintf(&b, "%s\tfor %s := range %s {\n", indent, keyVar, entriesVar)
		fmt.Fprintf(&b, "%s\t\t%s := %s[%s]\n", indent, entryVar, entriesVar, keyVar)
		fmt.Fprintf(&b, "%s\t\t%s := %s{}\n", indent, elemVar, modelType)
		b.WriteString(inner)
		fmt.Fprintf(&b, "%s\t\t%s[%s] = %s\n", indent, mapVar, keyVar, elemVar)
		fmt.Fprintf(&b, "%s\t}\n", indent)
		valueName := lowerCamel(n.attribute.Name) + "Value" + depthSuffix(depth)
		diagsName := lowerCamel(n.attribute.Name) + "Diags" + depthSuffix(depth)
		elemType := nestedObjectType(namer, path)
		fmt.Fprintf(&b, "%s\t%s, %s := types.MapValueFrom(ctx, %s, %s)\n",
			indent, valueName, diagsName, elemType, mapVar)
		fmt.Fprintf(&b, "%s\tdiags.Append(%s...)\n", indent, diagsName)
		fmt.Fprintf(&b, "%s\t%s = %s\n", indent, field, valueName)
		fmt.Fprintf(&b, "%s} else {\n%s\t%s = types.MapNull(%s)\n%s}\n",
			indent, indent, field, elemType, indent)
		return b.String(), nil
	}

	valueVar := "value" + depthSuffix(depth)
	nestedVar := lowerCamel(n.attribute.Name) + "State" + depthSuffix(depth)
	inner, err := stateLinesWith(namer, path, n.children, valueVar, nestedVar, depth+1)
	if err != nil {
		return "", err
	}

	// A value-typed nested accessor cannot be nil; interface and pointer
	// ones can, and nil maps to a null object. The binder decides this from
	// the accessor's real type — the SDK-type spelling alone cannot, because a
	// kiota interface return spells identically to its own model name yet is
	// nil-comparable.
	nilable := n.fb.Access.NestedNilable

	var b strings.Builder
	if nilable {
		fmt.Fprintf(&b, "%sif %s := %s.%s(); %s != nil {\n", indent, valueVar, source, n.fb.Access.Get, valueVar)
	} else {
		fmt.Fprintf(&b, "%s{\n%s\t%s := %s.%s()\n", indent, indent, valueVar, source, n.fb.Access.Get)
	}
	valueName := lowerCamel(n.attribute.Name) + "Value" + depthSuffix(depth)
	diagsName := lowerCamel(n.attribute.Name) + "Diags" + depthSuffix(depth)
	fmt.Fprintf(&b, "%s\t%s := %s{}\n", indent, nestedVar, modelType)
	b.WriteString(inner)
	fmt.Fprintf(&b, "%s\t%s, %s := types.ObjectValueFrom(ctx, %s(), %s)\n",
		indent, valueName, diagsName, attrTypesFuncName(modelType), nestedVar)
	fmt.Fprintf(&b, "%s\tdiags.Append(%s...)\n", indent, diagsName)
	fmt.Fprintf(&b, "%s\t%s = %s\n", indent, field, valueName)
	if nilable {
		fmt.Fprintf(&b, "%s} else {\n%s\t%s = types.ObjectNull(%s())\n%s}\n",
			indent, indent, field, attrTypesFuncName(modelType), indent)
	} else {
		fmt.Fprintf(&b, "%s}\n", indent)
	}
	return b.String(), nil
}
