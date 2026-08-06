package openapi

import (
	"fmt"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/naming"

	base "github.com/pb33f/libopenapi/datamodel/high/base"
)

// ErrNotADataSource marks a candidate the data-source inference refuses.
var ErrNotADataSource = fmt.Errorf("not a data source candidate")

// InferDataSource turns a readable-but-not-creatable candidate into a
// selector-shaped data source blueprint.
//
// The shape is the one a practitioner actually uses: one data source per
// family, looked up by exactly one of its selectors. The list operation is the
// resolver -- selectors narrow it to exactly one element, predictably, with
// zero and several matches both refused -- and where the family also offers a
// direct by-identifier read, the matched element only supplies the identifier
// and the direct read does the fetching, so state always maps from one
// response shape.
//
// Implemented for the kiotaFluent dialect only: the resty pilot's data
// sources were hand-authored, and no resty consumer is asking.
func (d *Document) InferDataSource(c Candidate, opts InferOptions) (blueprint.DataSource, []Caveat, error) {
	// A creatable family gets a data source too. Being manageable does not stop
	// a practitioner needing to look one up they did not create -- that is the
	// most useful lookup there is -- and the resource and the data source are
	// separate Terraform namespaces, so both may carry the family's name. What
	// a data source needs is a resolver and a shape to map, which is the list
	// operation, whatever else the family offers.
	kind, why := c.Classify()
	if kind != CandidateKindDataSource && kind != CandidateKindResource {
		return blueprint.DataSource{}, nil, fmt.Errorf("%w: %s: %s", ErrNotADataSource, c.Key, why)
	}
	if opts.SDKDialect != blueprint.DialectKiotaFluent {
		return blueprint.DataSource{}, nil, fmt.Errorf(
			"%w: %s: dataSource inference exists for the kiotaFluent dialect only", ErrNotADataSource, c.Key)
	}
	if c.List == nil {
		return blueprint.DataSource{}, nil, fmt.Errorf(
			"%w: %s: no list operation; a lookup with no resolver would be a bare item read, which curation can author directly",
			ErrNotADataSource, c.Key)
	}
	if strings.Contains(c.List.Path, "{") {
		return blueprint.DataSource{}, nil, fmt.Errorf(
			"%w: %s: the list path %s rides under a parent identifier, and the selector shape has "+
				"no input to carry one; the generated chain would read state that does not exist",
			ErrNotADataSource, c.Key, c.List.Path)
	}

	goType := namingOpts.GoTypeName(c.Key) + "DataSource"

	ds := blueprint.DataSource{
		Key:            c.Key,
		Name:           naming.TerraformName(c.Key),
		GoPackage:      naming.SnakeDirName(c.Key),
		GoPackageAlias: namingOpts.PackageAlias(opts.APIVersionDir, c.Key+"_ds"),
		GoTypeName:     goType,
		ModelTypeName:  goType + "Model",
		ServiceGroup:   naming.SnakeDirName(c.Tag),
		APIVersionDir:  opts.APIVersionDir,
	}
	if s := summaryOf(c); s != "" {
		ds.Schema.MarkdownDescription = s
	}

	ctxArg := blueprint.Argument{Kind: blueprint.ArgContext}
	nilCfg := blueprint.Argument{Kind: blueprint.ArgLiteral, Expr: "nil"}
	cfgID := blueprint.Argument{Kind: blueprint.ArgConfigField, Field: "ID"}

	// The list resolver: response type, the field holding the elements, and
	// the element type itself.
	listRespName := schemaNameOf(d.operationResponseProxy(c.List))
	collectionJSON, elementName := d.collectionOf(c.List)
	if listRespName == "" || collectionJSON == "" || elementName == "" {
		return blueprint.DataSource{}, nil, fmt.Errorf(
			"%w: %s: the list response declares no element collection this can resolve through",
			ErrNotADataSource, c.Key)
	}
	elementType := "models." + kiotaModelName(elementName) + "able"

	binding := blueprint.DataSourceBinding{
		Service: blueprint.ServiceRef{
			ImportPath: strings.TrimSuffix(opts.SDKModelsImport, "/"),
			Alias:      "models",
			Accessor:   "d.client",
		},
		List: &blueprint.Operation{
			Style:  blueprint.CallStyleFluent,
			Chain:  kiotaChain(c.List.Path, "Get", []blueprint.Argument{ctxArg, nilCfg}),
			Return: blueprint.ReturnResultError, ResultType: "models." + kiotaModelName(listRespName) + "able",
			HTTPMethod: c.List.Method, PathTemplate: c.List.Path,
		},
		CollectionField: "Get" + kiotaAccessorBase(collectionJSON) + "()",
		ElementType:     elementType,
	}

	// Attributes come from whichever schema state will map from: the direct
	// read's response when one exists, the list element otherwise.
	var stateSchema *base.Schema
	if c.Read != nil {
		stateSchema = responseSchema(d.operation(c.Read))
		respName := schemaNameOf(d.operationResponseProxy(c.Read))
		if respName == "" {
			return blueprint.DataSource{}, nil, fmt.Errorf(
				"%w: %s: the read declares no response schema", ErrNotADataSource, c.Key)
		}
		binding.Read = &blueprint.Operation{
			Style:  blueprint.CallStyleFluent,
			Chain:  kiotaChainWith(c.Read.Path, "Get", cfgID, []blueprint.Argument{ctxArg, nilCfg}),
			Return: blueprint.ReturnResultError, ResultType: "models." + kiotaModelName(respName) + "able",
			HTTPMethod: c.Read.Method, PathTemplate: c.Read.Path,
		}
		binding.Response = blueprint.ResponseModel{
			Type: "models." + kiotaModelName(respName) + "able", AccessStyle: blueprint.AccessMethod,
		}
	} else {
		stateSchema = d.elementSchemaOf(c.List)
		binding.Response = blueprint.ResponseModel{
			Type: elementType, AccessStyle: blueprint.AccessMethod,
		}
	}

	ictx := newInferCtx(c.Key, "models", opts.SDKDialect)

	fields := Fields(stateSchema)
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })

	var attrs []blueprint.Attribute
	for _, f := range fields {
		if skip, why := skipField(f); skip {
			ictx.note(f.Name, why)
			continue
		}
		attr, why := ictx.attributeOf(f, f.Name, false)
		if why != "" {
			ictx.note(f.Name, why)
			continue
		}
		attr.ComputedOptionalRequired = blueprint.Computed
		stripExpand(&attr)
		attrs = append(attrs, attr)
	}
	if len(attrs) == 0 {
		return blueprint.DataSource{}, ictx.notes, fmt.Errorf(
			"%w: %s: nothing usable in its schemas", ErrNotADataSource, c.Key)
	}

	// Selectors: the direct identifier when a by-id read exists, plus every
	// name-shaped string field on the element -- the fields a practitioner
	// actually knows an object by. Each selector attribute is configuration as
	// well as output, so it flips to optional-and-computed.
	elementFields := Fields(d.elementSchemaOf(c.List))
	selectorSet := map[string]bool{}
	for _, f := range elementFields {
		if f.Kind != blueprint.KindString || f.Deprecated {
			continue
		}
		lower := strings.ToLower(f.Name)
		if lower == "name" || strings.HasSuffix(lower, "name") {
			selectorSet[f.Name] = true
		}
	}

	// The identifier takes the conventional name whatever the API spells it,
	// exactly as a resource's does -- a practitioner reaching for
	// data.<type>.<name>.id should find it, and the read chain the emitter
	// renders passes the configuration's ID field by that name.
	idJSON, adopted := adoptIDAttribute(attrs, c)

	var selectors []blueprint.Selector
	if c.Read != nil {
		if !adopted {
			return blueprint.DataSource{}, ictx.notes, fmt.Errorf(
				"%w: %s: no identifier attribute to feed the direct read", ErrNotADataSource, c.Key)
		}
		selectors = append(selectors, blueprint.Selector{
			Attribute: "id", GoField: goFieldOf(attrs, "id"), ViaRead: true,
		})
		markSelector(attrs, "id")

		// The element's identifier is the same field the id attribute maps, so
		// it takes the same conversion. Assuming a plain string here was wrong
		// the moment an API keyed a family by uuid: the SDK hands back a
		// uuid.UUID, and a string conversion against it does not compile.
		binding.ElementIDField = kiotaAccessorBase(idJSON)
		binding.ElementIDFlatten = &blueprint.ConvertCall{Func: "convert.PtrStringToFramework"}
		for _, a := range attrs {
			if a.Name == "id" && a.Wire.Flatten != nil {
				call := *a.Wire.Flatten
				binding.ElementIDFlatten = &call
			}
		}
	}

	for _, f := range elementFields {
		if !selectorSet[f.Name] {
			continue
		}
		attrName := naming.TerraformName(f.Name)
		if !hasAttribute(attrs, attrName) {
			continue
		}
		selectors = append(selectors, blueprint.Selector{
			Attribute: attrName,
			GoField:   goFieldOf(attrs, attrName),
			SDKField:  kiotaAccessorBase(f.Name),
		})
		markSelector(attrs, attrName)
	}

	if len(selectors) == 0 {
		return blueprint.DataSource{}, ictx.notes, fmt.Errorf(
			"%w: %s: no identifier and no name-shaped field; there is nothing predictable to look this up by",
			ErrNotADataSource, c.Key)
	}

	binding.Selectors = selectors
	ds.Binding = binding
	ds.Schema.Attributes = attrs

	return ds, ictx.notes, nil
}

// stripExpand removes the write direction from an attribute and its children:
// a data source sends no body, so only flatten survives.
func stripExpand(a *blueprint.Attribute) {
	a.Wire.Expand = nil
	a.Wire.UpdateExpand = nil
	if a.Type.NestedObject != nil {
		for i := range a.Type.NestedObject.Attributes {
			stripExpand(&a.Type.NestedObject.Attributes[i])
		}
	}
}

// markSelector flips a selector attribute to optional-and-computed: settable
// as the lookup key, filled from the response either way.
func markSelector(attrs []blueprint.Attribute, name string) {
	for i := range attrs {
		if attrs[i].Name == name {
			attrs[i].ComputedOptionalRequired = blueprint.ComputedOptional
		}
	}
}

func goFieldOf(attrs []blueprint.Attribute, name string) string {
	for _, a := range attrs {
		if a.Name == name {
			return a.GoField
		}
	}
	return ""
}

// operationResponseProxy is responseProxy for one operation.
func (d *Document) operationResponseProxy(op *Operation) *base.SchemaProxy {
	o := d.operation(op)
	if o == nil || o.Responses == nil || o.Responses.Codes == nil {
		return nil
	}
	for pair := o.Responses.Codes.First(); pair != nil; pair = pair.Next() {
		if !strings.HasPrefix(pair.Key(), "2") || pair.Value() == nil || pair.Value().Content == nil {
			continue
		}
		if p := proxyFromContent(pair.Value().Content); p != nil {
			return p
		}
	}
	return nil
}

// collectionOf finds the array-of-objects property inside the list response:
// its JSON name reaches the elements, its item schema names their type.
func (d *Document) collectionOf(list *Operation) (jsonName, elementSchema string) {
	s := responseSchema(d.operation(list))
	if s == nil || s.Properties == nil {
		return "", ""
	}
	for pair := s.Properties.First(); pair != nil; pair = pair.Next() {
		prop := pair.Value()
		if prop == nil {
			continue
		}
		ps := prop.Schema()
		if ps == nil || len(ps.Type) == 0 || ps.Type[0] != "array" || ps.Items == nil || ps.Items.A == nil {
			continue
		}
		if name := refTypeName(ps.Items.A); name != "" {
			return pair.Key(), name
		}
	}
	return "", ""
}

// elementSchemaOf resolves the list element's schema.
func (d *Document) elementSchemaOf(list *Operation) *base.Schema {
	s := responseSchema(d.operation(list))
	if s == nil || s.Properties == nil {
		return nil
	}
	for pair := s.Properties.First(); pair != nil; pair = pair.Next() {
		prop := pair.Value()
		if prop == nil {
			continue
		}
		ps := prop.Schema()
		if ps == nil || len(ps.Type) == 0 || ps.Type[0] != "array" || ps.Items == nil || ps.Items.A == nil {
			continue
		}
		return ps.Items.A.Schema()
	}
	return nil
}
