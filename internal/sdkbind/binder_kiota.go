package sdkbind

import (
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// kiotaBinder drafts bindings against a kiota-generated Go SDK: fluent
// request-builder chains synthesised from the path template, models behind
// Get/Set accessor pairs with pointer-typed scalars, read-side interfaces
// and concrete structs with constructors on the write side.
type kiotaBinder struct{}

func (kiotaBinder) Name() string { return config.BackendKiota }

func (b kiotaBinder) Bind(m *ir.Model, info SDKInfo) (*Bindings, error) {
	return bindModel(m, info, b), nil
}

// call synthesises the request-builder chain for one operation from its
// path template: a literal segment is a builder method, a parameter
// segment a typed indexer, and the HTTP verb the final hop.
//
//	/tags/{tagId} + GET  ->  client.Tags().ByTagId(tagId).Get(ctx, nil)
//
// The trailing nil is the per-request configuration, which generated
// provider code never customises.
func (kiotaBinder) call(op *ir.Operation, n ir.Names, hasBody bool, info SDKInfo) *Call {
	var segs []Segment
	params := callParams(op)
	next := 0
	for _, seg := range pathSegments(op.PathTemplate) {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			local := "param_"
			if next < len(params) {
				local = params[next].Local
			}
			next++
			segs = append(segs, Segment{
				Name: "By" + exportedName(strings.Trim(seg, "{}")),
				Call: true,
				Args: []string{local},
			})
			continue
		}
		segs = append(segs, Segment{Name: exportedName(seg), Call: true})
	}

	verb := exportedName(strings.ToLower(op.Method))
	args := []string{"ctx"}
	if hasBody {
		args = append(args, "body")
	}
	args = append(args, "nil")
	segs = append(segs, Segment{Name: verb, Call: true, Args: args})

	c := &Call{
		Segments: segs,
		Imports:  []string{info.ImportPath},
		Params:   params,
	}

	// Drafted payload types, spelled from the entity name the way kiota
	// spells a component schema's interface; Prune settles them from the
	// real method signatures. A delete answers with error alone.
	able := "models." + exportedName(n.Key) + "able"
	switch op.Kind {
	case ir.OperationDelete:
		c.Results = []string{"error"}
	case ir.OperationList:
		// The collection response's name is not derivable from the
		// intermediate representation; Prune reads it off the SDK.
		c.Results = []string{"", "error"}
	default:
		c.ResponseType = able
		c.Results = []string{able, "error"}
	}
	if hasBody {
		c.RequestType = able
	}
	if c.ResponseType != "" || hasBody {
		c.Imports = append(c.Imports, info.ModelsImportPath)
	}

	c.rerender()
	return c
}

// models drafts the entity's read interface, concrete write struct and
// constructor. kiota deserialises responses into "<Model>able" interfaces
// and constructs bodies through New<Model>().
func (kiotaBinder) models(n ir.Names, _ SDKInfo) (string, string, string) {
	base := exportedName(n.Key)
	return "models." + base + "able", "models." + base, "models.New" + base + "()"
}

// access drafts one attribute's accessor pair and conversions. kiota
// scalars are pointer-typed behind Get/Set, and a property whose name Go
// reserves is reached through an "Escaped" accessor.
func (kiotaBinder) access(a ir.Attribute, mode accessMode) FieldAccess {
	base := exportedName(a.WireName)
	if goReservedWords[strings.ToLower(a.WireName)] {
		base += "Escaped"
	}

	fa := FieldAccess{}
	if readable(mode) {
		fa.Get = "Get" + base
	}
	if writable(a, mode) {
		fa.Set = "Set" + base
	}

	switch a.Kind {
	case ir.TypeString:
		fa.SDKType, fa.ConvertGet, fa.ConvertSet = "*string", "FromPtrString", "ToPtrString"
	case ir.TypeBool:
		fa.SDKType, fa.ConvertGet, fa.ConvertSet = "*bool", "FromPtrBool", "ToPtrBool"
	case ir.TypeInt64:
		fa.SDKType, fa.ConvertGet, fa.ConvertSet = "*int64", "FromPtrInt64", "ToPtrInt64"
	case ir.TypeFloat64:
		fa.SDKType, fa.ConvertGet, fa.ConvertSet = "*float64", "FromPtrFloat64", "ToPtrFloat64"
	case ir.TypeList:
		if a.Nested == nil {
			fa.SDKType = "[]" + strings.TrimPrefix(goTypeOf(a.ElementType), "*")
			shape := exportedName(string(a.ElementType)) + "Slice"
			fa.ConvertGet, fa.ConvertSet = "From"+shape, "To"+shape
		}
	case ir.TypeMap:
		if a.Nested == nil {
			fa.SDKType = "map[string]" + strings.TrimPrefix(goTypeOf(a.ElementType), "*")
			shape := exportedName(string(a.ElementType)) + "Map"
			fa.ConvertGet, fa.ConvertSet = "From"+shape, "To"+shape
		}
	case ir.TypeObject:
		// A nested object's model is reached through the parent getter's
		// result type, which only the loaded SDK can name; Prune fills
		// NestedModel and the write-side twin.
	}

	if !readable(mode) {
		fa.ConvertGet = ""
	}
	if fa.Set == "" {
		fa.ConvertSet = ""
	}

	return fa
}
