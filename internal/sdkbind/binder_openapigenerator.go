package sdkbind

import (
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// openAPIGeneratorBinder drafts bindings against an openapi-generator go
// client: flat "<Tag>API" service structs hanging off the client, one
// request-builder struct per operation with fluent parameter setters, and
// an Execute() that returns (payload, *http.Response, error). Models are
// flat structs in the same package with Get/Set helper pairs.
type openAPIGeneratorBinder struct{}

func (openAPIGeneratorBinder) Name() string { return config.BackendOpenAPIGenerator }

func (b openAPIGeneratorBinder) Bind(m *ir.Model, info SDKInfo) (*Bindings, error) {
	return bindModel(m, info, b), nil
}

// opMethodName renders the service method for one operation: the
// operationId camelised the way the generator does it, or, when the
// document declares none, the generator's own synthesis from the path and
// method — "/tags/{tagId}" + GET becomes TagsTagIdGet.
func opMethodName(operation *ir.Operation) string {
	if operation.OperationID != "" {
		return exportedName(operation.OperationID)
	}
	stripped := strings.NewReplacer("{", "", "}", "").Replace(operation.PathTemplate)
	var b strings.Builder
	for _, seg := range pathSegments(stripped) {
		b.WriteString(exportedName(seg))
	}
	b.WriteString(exportedName(strings.ToLower(operation.Method)))
	return b.String()
}

// call drafts one operation's invocation:
//
//	client.TagsAPI.CreateTag(ctx).Tag(*body).Execute()
//	client.TagsAPI.GetTag(ctx, tagId).Execute()
//
// The service field is drafted from the entity's service area and the
// body setter from its type name — both spellings the document cannot
// fully determine (the generator names services after spec tags and body
// setters after parameter names), so Prune settles them against the real
// client, repairing only where the SDK admits exactly one answer.
func (openAPIGeneratorBinder) call(operation *ir.Operation, n ir.Names, hasBody bool, info SDKInfo) *Call {
	args := []string{"ctx"}
	for _, p := range callParameters(operation) {
		args = append(args, p.Local)
	}

	segs := []Segment{
		{Name: exportedName(n.Service) + "API"},
		{Name: opMethodName(operation), Call: true, Args: args},
	}
	if hasBody {
		// The generator's body setter takes the model by value; body is
		// the constructed pointer, so the argument dereferences it.
		segs = append(segs, Segment{Name: exportedName(n.Key), Call: true, Args: []string{"*body"}})
	}
	segs = append(segs, Segment{Name: "Execute", Call: true})

	c := &Call{
		Segments:   segs,
		Imports:    []string{info.ImportPath, "net/http"},
		Parameters: callParameters(operation),
	}

	model := "sdk." + exportedName(n.Key)
	switch operation.Kind {
	case ir.OperationDelete:
		c.Results = []string{"*http.Response", "error"}
	case ir.OperationList:
		// A list may answer with a bare slice or a paged envelope; only
		// the SDK knows which, so Prune fills the payload type.
		c.Results = []string{"", "*http.Response", "error"}
	default:
		c.ResponseType = "*" + model
		c.Results = []string{"*" + model, "*http.Response", "error"}
	}
	if hasBody {
		c.RequestType = model
	}

	c.rerender()
	return c
}

// models drafts the entity's model type: one flat struct serves both
// directions, constructed through the generator's NewXWithDefaults.
func (openAPIGeneratorBinder) models(n ir.Names, _ SDKInfo) (string, string, string) {
	model := "sdk." + exportedName(n.Key)
	return "*" + model, model, "sdk.New" + exportedName(n.Key) + "WithDefaults()"
}

// access drafts one attribute's accessor pair. The generator's model
// helpers deref on the way out — GetName() returns string whatever the
// field's optionality — so the drafted SDK types are values, not pointers.
func (openAPIGeneratorBinder) access(a ir.Attribute, mode accessMode) FieldAccess {
	base := exportedName(a.WireName)

	fa := FieldAccess{}
	if readable(mode) {
		fa.Get = "Get" + base
	}
	if writable(a, mode) {
		fa.Set = "Set" + base
	}

	switch a.Type {
	case ir.TypeString:
		fa.SDKType, fa.ConvertGet, fa.ConvertSet = "string", "FromString", "ToString"
	case ir.TypeBool:
		fa.SDKType, fa.ConvertGet, fa.ConvertSet = "bool", "FromBool", "ToBool"
	case ir.TypeInt64:
		fa.SDKType, fa.ConvertGet, fa.ConvertSet = "int64", "FromInt64", "ToInt64"
	case ir.TypeFloat64:
		fa.SDKType, fa.ConvertGet, fa.ConvertSet = "float64", "FromFloat64", "ToFloat64"
	case ir.TypeList:
		if a.CollectionNestingDepth() > 1 && a.NestedAttributes == nil {
			fa.SDKType = "[]" + nestedCollectionGoType(a.NestedCollectionElementTypes[:len(a.NestedCollectionElementTypes)-1], goTypeOf(a.ElementType))
			fa.ConvertGet, fa.ConvertSet = nestedCollectionShorthand(ir.TypeList)
			break
		}
		if a.NestedAttributes == nil {
			fa.SDKType = "[]" + goTypeOf(a.ElementType)
			shape := exportedName(string(a.ElementType)) + "Slice"
			fa.ConvertGet, fa.ConvertSet = "From"+shape, "To"+shape
		}
	case ir.TypeMap:
		if a.CollectionNestingDepth() > 1 && a.NestedAttributes == nil {
			fa.SDKType = "map[string]" + nestedCollectionGoType(a.NestedCollectionElementTypes[:len(a.NestedCollectionElementTypes)-1], goTypeOf(a.ElementType))
			fa.ConvertGet, fa.ConvertSet = nestedCollectionShorthand(ir.TypeMap)
			break
		}
		if a.NestedAttributes == nil {
			fa.SDKType = "map[string]" + goTypeOf(a.ElementType)
			shape := exportedName(string(a.ElementType)) + "Map"
			fa.ConvertGet, fa.ConvertSet = "From"+shape, "To"+shape
		}
	case ir.TypeObject:
		// Prune fills the nested model from the getter's result type.
	}

	if !readable(mode) {
		fa.ConvertGet = ""
	}
	if fa.Set == "" {
		fa.ConvertSet = ""
	}

	return fa
}
