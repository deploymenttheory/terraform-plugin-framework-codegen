package sdkbind

import (
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// dialect is what a backend contributes to the shared binding walk: how a
// call is spelled, how a field is reached, and which model types an entity
// drafts. Everything a dialect returns is finished strings; the walk in
// bindModel is the only structure.
type dialect interface {
	call(operation *ir.Operation, n ir.Names, hasBody bool, info SDKInfo) *Call
	access(a ir.Attribute, mode accessMode) FieldAccess
	// models drafts the entity's read model, write model and write
	// constructor. Drafts, because the document-derived name and the
	// SDK's canonical one can differ; Prune settles them from the SDK.
	models(n ir.Names, info SDKInfo) (readModel, writeModel, writeConstructor string)
}

// accessMode is which directions a kind's fields travel.
type accessMode int

const (
	accessReadWrite accessMode = iota
	accessReadOnly
	accessWriteOnly
)

// bindModel is the backend-independent walk from the intermediate
// representation to a drafted binding set.
func bindModel(m *ir.Model, info SDKInfo, d dialect) *Bindings {
	b := &Bindings{
		SDK:           info,
		Resources:     map[string]*ResourceBinding{},
		Datasources:   map[string]*DatasourceBinding{},
		ListResources: map[string]*ListResourceBinding{},
		Actions:       map[string]*ActionBinding{},
	}

	for _, r := range m.Resources {
		read, write, constructor := d.models(r.Names, info)
		rb := &ResourceBinding{
			Key:              r.Names.Key,
			ReadModel:        read,
			WriteModel:       write,
			WriteConstructor: constructor,
			Fields:           fieldBindings(d, r.Schema, accessReadWrite),
		}
		if r.Operations.Create != nil {
			rb.Create = d.call(r.Operations.Create, r.Names, true, info)
		}
		if r.Operations.Read != nil {
			rb.Read = d.call(r.Operations.Read, r.Names, false, info)
		}
		if r.Operations.Update != nil {
			rb.Update = d.call(r.Operations.Update, r.Names, true, info)
		}
		if r.Operations.Delete != nil {
			rb.Delete = d.call(r.Operations.Delete, r.Names, false, info)
		}
		b.Resources[r.Names.Key] = rb
	}

	for _, ds := range m.Datasources {
		read, _, _ := d.models(ds.Names, info)
		db := &DatasourceBinding{Key: ds.Names.Key, ReadModel: read, ListWrapperKey: ds.ListWrapperKey}
		if ds.Operations.Read != nil {
			db.Read = d.call(ds.Operations.Read, ds.Names, false, info)
		}
		if ds.Operations.List != nil {
			db.List = d.call(ds.Operations.List, ds.Names, false, info)
		}
		db.Fields = fieldBindings(d, datasourceElementTree(ds), accessReadOnly)
		b.Datasources[ds.Names.Key] = db
	}

	for _, lr := range m.ListResources {
		listOp := lr.ListOperation
		b.ListResources[lr.Names.Key] = &ListResourceBinding{
			Key:         lr.Names.Key,
			List:        d.call(&listOp, lr.Names, false, info),
			EnvelopeKey: lr.ListWrapperKey,
			Fields:      fieldBindings(d, lr.Schema, accessReadOnly),
		}
	}

	for _, a := range m.Actions {
		invokeOp := a.InvokeOperation
		hasBody := a.RequestSchema != nil
		ab := &ActionBinding{
			Key:    a.Names.Key,
			Invoke: d.call(&invokeOp, a.Names, hasBody, info),
			Fields: fieldBindings(d, a.RequestSchema, accessWriteOnly),
		}
		if hasBody {
			_, write, constructor := d.models(a.Names, info)
			ab.WriteModel = write
			ab.WriteConstructor = constructor
		}
		b.Actions[a.Names.Key] = ab
	}

	return b
}

// datasourceElementTree is the tree a datasource's fields bind against:
// the whole schema for a lookup-by-key datasource, and the items element
// for a companion — the filter attributes are toolkit vocabulary with no
// SDK side, so they take no bindings.
func datasourceElementTree(ds ir.Datasource) *ir.AttributeTree {
	if ds.LookupByKey {
		return ds.Schema
	}
	if ds.Schema == nil {
		return nil
	}
	for _, a := range ds.Schema.Attributes {
		if a.Name == "items" {
			return a.Nested
		}
	}
	return ds.Schema
}

// fieldBindings drafts the field accesses of one attribute tree,
// preserving attribute order. Unsupported attributes take no binding —
// derivation already refused to guess their shape, and a binding for a
// shape nothing models would be an invention.
func fieldBindings(d dialect, t *ir.AttributeTree, mode accessMode) []FieldBinding {
	if t == nil {
		return nil
	}
	out := make([]FieldBinding, 0, len(t.Attributes))
	for _, a := range t.Attributes {
		if a.Unsupported {
			continue
		}
		fb := FieldBinding{
			Attr: a.Name, Wire: a.WireName,
			Kind: a.Kind, ElementType: a.ElementType,
			Access: d.access(a, mode),
		}
		// A property the document declares write-only is never read back,
		// whatever the response model offers: an API that declares one
		// answers it masked or not at all, and neither is the value sent.
		// Its members are write-only with it — a member only a response
		// carries can never be read through a root that is not.
		nestedMode := mode
		if a.WriteOnly && fb.Access.Set != "" {
			fb.Access.Get = ""
			fb.KeptFromPlan = true
			nestedMode = accessWriteOnly
		}
		if a.Nested != nil {
			fb.Nested = fieldBindings(d, a.Nested, nestedMode)
		}
		out = append(out, fb)
	}
	return out
}

// writable reports whether an attribute travels in a request body: an
// attribute the server alone fills never does.
func writable(a ir.Attribute, mode accessMode) bool {
	return mode != accessReadOnly && a.ComputedOptionalRequired != ir.Computed
}

// readable reports whether an attribute is mapped back from a response.
func readable(mode accessMode) bool {
	return mode != accessWriteOnly
}

// goReservedWords are the identifiers kiota mangles with an "Escaped"
// suffix: Go keywords plus the predeclared names ("error" is the live
// case — kiota generates GetErrorEscaped).
var goReservedWords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
	"any": true, "bool": true, "byte": true, "comparable": true, "complex64": true,
	"complex128": true, "error": true, "float32": true, "float64": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"rune": true, "string": true, "uint": true, "uint8": true, "uint16": true,
	"uint32": true, "uint64": true, "uintptr": true, "true": true, "false": true,
	"iota": true, "nil": true, "append": true, "cap": true, "clear": true,
	"close": true, "complex": true, "copy": true, "delete": true, "imag": true,
	"len": true, "make": true, "max": true, "min": true, "new": true,
	"panic": true, "print": true, "println": true, "real": true, "recover": true,
}

// exportedName renders a name the way both generators' Go emitters do:
// each word's first letter upper-cased and the rest kept — no initialism
// table, so accountGroupId becomes AccountGroupId, never AccountGroupID.
// This is deliberately not the toolkit's own acronym-aware casing: the
// binding must spell what the generated SDK spells.
func exportedName(name string) string {
	var b strings.Builder
	upperNext := true
	for _, r := range name {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ':
			upperNext = true
		case upperNext:
			b.WriteString(strings.ToUpper(string(r)))
			upperNext = false
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// localFor renders a path parameter's wire name as the Go local the call
// expression references: lower-first camel, with a trailing underscore
// when the result would collide with a keyword or predeclared name.
func localFor(parameterName string) string {
	name := exportedName(parameterName)
	if name == "" {
		return "parameter_"
	}
	local := strings.ToLower(name[:1]) + name[1:]
	if goReservedWords[local] {
		local += "_"
	}
	return local
}

// goTypeOf is the Go type a path-parameter local is declared with.
func goTypeOf(k ir.AttributeType) string {
	switch k {
	case ir.TypeBool:
		return "bool"
	case ir.TypeInt64:
		return "int64"
	case ir.TypeFloat64:
		return "float64"
	default:
		return "string"
	}
}

// callParameters renders an operation's path parameters as call locals, in
// path-template order — the order every expression takes them.
func callParameters(operation *ir.Operation) []CallParameter {
	if len(operation.PathParameters) == 0 {
		return nil
	}
	out := make([]CallParameter, 0, len(operation.PathParameters))
	for _, p := range operation.PathParameters {
		out = append(out, CallParameter{Local: localFor(p.Name), GoType: goTypeOf(p.Type), Wire: p.Name})
	}
	return out
}

// pathSegments splits a path template into its non-empty segments.
func pathSegments(pathTemplate string) []string {
	var out []string
	for _, seg := range strings.Split(strings.Trim(pathTemplate, "/"), "/") {
		if seg != "" {
			out = append(out, seg)
		}
	}
	return out
}
