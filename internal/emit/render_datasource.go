package emit

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/fixtures"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// datasourceData is the render context every datasource template consumes.
type datasourceData struct {
	Source        string
	Package       string
	PackagePath   string
	Key           string
	Pascal        string
	Type          string
	TerraformType string
	ClientType    string

	Imports      string
	ModelImports string
	ReadImports  string
	StateImports string
	TestImports  string
	MocksImports string

	TimeoutRead       string
	SchemaDescription string
	SchemaAttributes  string
	Models            string

	LookupByKey bool
	// Companion fields.
	HasIDFilter bool
	// IDField is the model field the id filter reads from, spelled the Go
	// way the model declares it.
	IDField string
	// FilterChecks is the body of the generated match: one early return per
	// filter the configuration set, and true when every one of them agreed.
	FilterChecks string
	// ReadItem renders the by-id read's payload as one list element:
	// "remote", or "*remote" when the read answers with a pointer to the
	// element type.
	ReadItem    string
	ReadPlan    callPlan
	ListPlan    callPlan
	Collection  string
	ItemModel   string
	ElementType string
	// AddressingHCL is the configuration the collection path's parameters
	// need, already rendered, with any value the call parses as an integer
	// pinned to digits.
	AddressingHCL string
	MapItemBody   string
	// Lookup fields.
	ReadModel string
	StateBody string

	// Test and mock fields.
	RegistryName    string
	CollectionURL   string
	ItemPattern     string
	HasItemMock     bool
	ResponseMaximal string
	ListPayload     string
	UnitChecks      string
}

// datasource renders one datasource's complete file set.
func (e *serviceRenderer) datasource(ds *ir.Datasource, db *sdkbind.DatasourceBinding) ([]File, error) {
	d := &datasourceData{
		Package:       ds.Names.Package,
		PackagePath:   e.packagePath(kindDatasources, ds.Names),
		Key:           ds.Names.Key,
		Pascal:        ds.Names.Pascal,
		Type:          ds.Names.Pascal + "Datasource",
		TerraformType: ds.Names.TerraformType,
		ClientType:    "*sdk." + e.bindings.SDK.ClientTypeName,
		TimeoutRead:   "5 * time.Minute",
		LookupByKey:   ds.LookupByKey,
		ReadModel:     db.ReadModel,
	}

	var spec fixtures.Fixture
	var err error
	if ds.LookupByKey {
		spec, err = e.lookupDatasource(d, ds, db)
	} else {
		spec, err = e.companionDatasource(d, ds, db)
	}
	if err != nil {
		return nil, err
	}

	dir := e.dir(kindDatasources, ds.Names)
	var files []File
	renderGo := func(tmpl, out string) error {
		d.Source = "entity/datasource/" + tmpl
		f, ferr := e.renderServiceFile("datasource/"+tmpl, path.Join(dir, out), ds.Names.Key, d)
		if ferr != nil {
			return ferr
		}
		files = append(files, f)
		return nil
	}
	for _, gf := range []struct{ tmpl, out string }{
		{"datasource.go.tmpl", "datasource.go"},
		{"model.go.tmpl", "model.go"},
		{"read.go.tmpl", "read.go"},
		{"state.go.tmpl", "state.go"},
		{"datasource_test.go.tmpl", "datasource_test.go"},
		{"responders.go.tmpl", "mocks/responders.go"},
	} {
		if err := renderGo(gf.tmpl, gf.out); err != nil {
			return nil, err
		}
	}

	fixtures, err := e.datasourceFixtures(ds, spec, d.AddressingHCL, dir)
	if err != nil {
		return nil, err
	}
	files = append(files, fixtures...)
	return files, nil
}

// keyAttrName is the terraform spelling of a lookup datasource's key
// parameter.
func keyAttrName(ds *ir.Datasource) string {
	return ir.TerraformName(ds.KeyParameter)
}

// lookupDatasource fills the render context for a lookup-by-key
// datasource: the key parameter is the single required argument and the
// entity's object comes back.
func (e *serviceRenderer) lookupDatasource(d *datasourceData, ds *ir.Datasource, db *sdkbind.DatasourceBinding) (fixtures.Fixture, error) {
	if ds.Operations.Read == nil || db.Read == nil {
		return fixtures.Fixture{}, unrenderable("a lookup-by-key datasource needs a bound read call")
	}

	nodes := e.joinTree(bindingKindDatasource, ds.Names.Key, ds.Schema, db.Fields, addressingNames(ds.Operations.Read, ds.Operations.List))
	key := keyAttrName(ds)
	if !hasNode(nodes, key) {
		// The SDK model does not carry the key parameter as a field; the
		// schema still must, so the caller has somewhere to put it.
		nodes = append([]node{{attr: ir.Attribute{
			Name: key, WireName: ds.KeyParameter,
			Kind: ir.TypeString, ComputedOptionalRequired: ir.Required,
		}}}, nodes...)
	}

	imports := newImportSet(e.pc.Module)
	imports.add("", "context")
	imports.add("", "fmt")
	imports.add("", "time")
	imports.add("", "github.com/hashicorp/terraform-plugin-framework/datasource")
	imports.add("schema", "github.com/hashicorp/terraform-plugin-framework/datasource/schema")
	imports.add("sdk", e.bindings.SDK.ImportPath)
	imports.add("commonschema", e.pc.Module+"/internal/services/common/schema")
	sb := &schemaBuilder{kind: schemaDatasource, imports: imports}
	d.SchemaAttributes = sb.attributeDecls(nodes, 3)
	description := entityDescription(ds.Schema, "Reads one "+ds.Names.Key+" by its "+key+".")
	if ds.CoManagementNote != "" {
		description += " " + ds.CoManagementNote
	}
	d.SchemaDescription = strconv.Quote(description)
	d.Imports = imports.render()

	decls := buildModels(d.Type+"Model", d.Pascal+"Lookup", nodes,
		[]string{"Timeouts timeouts.Value `tfsdk:\"timeouts\"`"})
	d.Models = renderModelDecls(decls)
	d.ModelImports = e.datasourceModelImports(d.Models)

	for _, p := range db.Read.Params {
		if !namesAnAttribute(p, nodes) {
			return fixtures.Fixture{}, unrenderable(
				"read: the path parameter %q matches no argument the caller supplies, and a datasource has no id of its own to fall back on",
				p.Wire)
		}
	}
	plan, err := buildCallPlan(db.Read, "remote", nodes, "data", respDiagnostics())
	if err != nil {
		return fixtures.Fixture{}, fmt.Errorf("read: %w", err)
	}
	if plan.Payload == "" {
		return fixtures.Fixture{}, unrenderable("read: the bound read call yields no payload to map from")
	}
	// A read that answers with a collection is not a lookup, whatever the
	// path shape suggests. The state mapper reads fields off one object and
	// was handed the slice instead — "remote.GetDeviceId undefined (type
	// []models.DeviceComplianceInformationable)". Which element it should map
	// is not something the document says, and taking the first would be a
	// guess dressed up as a lookup.
	if strings.HasPrefix(d.ReadModel, "[]") {
		return fixtures.Fixture{}, unrenderable(
			"read: the by-key read answers with a collection (%s), which is not one object to map into state", d.ReadModel)
	}
	d.ReadPlan = plan

	readImports := newImportSet(e.pc.Module)
	readImports.add("", "context")
	readImports.add("", "github.com/hashicorp/terraform-plugin-framework/datasource")
	readImports.add("", e.pc.Module+"/internal/services/common/crud")
	readImports.add("", e.pc.Module+"/internal/services/common/errors")
	e.addSDKImports(readImports, plan.Assign)
	addPlanImports(readImports, plan)
	d.ReadImports = readImports.render()

	stateBody, err := stateLines(nodes, d.Pascal+"Lookup", "remote", "data", 1)
	if err != nil {
		return fixtures.Fixture{}, err
	}
	d.StateBody = stateBody
	stateImports := newImportSet(e.pc.Module)
	stateImports.add("", "context")
	stateImports.add("", "github.com/hashicorp/terraform-plugin-framework/diag")
	if strings.Contains(stateBody, "types.") {
		stateImports.add("", "github.com/hashicorp/terraform-plugin-framework/types")
	}
	if strings.Contains(stateBody, "convert.") {
		stateImports.add("", e.pc.Module+"/internal/services/common/convert")
	}
	e.addSDKImports(stateImports, stateBody, d.ReadModel)
	d.StateImports = stateImports.render()

	spec := deriveFixtures(ds.Schema, nodes)
	spec.PinNumeric(integerParsedParams(db.Read, nodes))
	e.datasourceMocks(d, ds, spec)
	e.datasourceChecks(d, ds.Names.Key, spec)
	return spec, nil
}

// companionDatasource fills the render context for the filters-and-items
// pattern: one optional argument per scalar field of a listed object, and the
// computed list of the objects they selected.
func (e *serviceRenderer) companionDatasource(d *datasourceData, ds *ir.Datasource, db *sdkbind.DatasourceBinding) (fixtures.Fixture, error) {
	if ds.Operations.List == nil || db.List == nil {
		return fixtures.Fixture{}, unrenderable("a companion datasource needs a bound list call")
	}
	itemTree := companionItemTree(ds)
	if itemTree == nil {
		return fixtures.Fixture{}, unrenderable("the companion schema carries no items attribute")
	}
	itemNodes := e.joinTree(bindingKindDatasource, ds.Names.Key, itemTree, db.Fields)

	// A filter selects on a field of a listed object, so it survives exactly
	// where that field does. Binding deletes what the SDK cannot carry, and
	// the item model is built from what is left: a filter over a deleted
	// field would compare against a model field that does not exist.
	filters := make([]ir.Attribute, 0, len(ds.Schema.Attributes))
	for _, f := range companionFilters(ds) {
		if hasNode(itemNodes, f.Name) {
			filters = append(filters, f)
		}
	}
	d.FilterChecks = filterChecks(filters)

	// Filtering on the id is answered by the by-id read rather than by
	// listing the whole collection and discarding all but one of it. That
	// needs the read's payload to bridge to one list element: identical
	// types, or a pointer the element type sits behind. A read shaped any
	// other way leaves id an ordinary filter rather than a guessed call.
	idFilter, hasID := filterNamed(filters, "id")
	d.HasIDFilter = hasID &&
		ds.Operations.Read != nil && db.Read != nil && len(db.Read.Params) == 1 &&
		db.Read.Params[0].GoType == "string" && itemPayloadExpr(db) != ""

	// Schema: the two filter inputs, then the computed items list.
	imports := newImportSet(e.pc.Module)
	imports.add("", "context")
	imports.add("", "fmt")
	imports.add("", "time")
	imports.add("", "github.com/hashicorp/terraform-plugin-framework/datasource")
	imports.add("schema", "github.com/hashicorp/terraform-plugin-framework/datasource/schema")
	imports.add("sdk", e.bindings.SDK.ImportPath)
	imports.add("commonschema", e.pc.Module+"/internal/services/common/schema")
	sb := &schemaBuilder{kind: schemaDatasource, imports: imports}

	var b strings.Builder
	// The addressing a parent-scoped collection is reached through, ahead of
	// the filter inputs as the tree declares them. The model carries a field
	// for each, and terraform refuses to decode a model whose schema declares
	// fewer attributes than the struct has fields.
	addressingNodes := make([]node, 0, len(companionAddressing(ds)))
	for _, a := range companionAddressing(ds) {
		addressingNodes = append(addressingNodes, node{attr: a})
	}
	b.WriteString(sb.attributeDecls(addressingNodes, 3))
	filterNodes := make([]node, 0, len(filters))
	for _, a := range filters {
		filterNodes = append(filterNodes, node{attr: a})
	}
	b.WriteString(sb.attributeDecls(filterNodes, 3))
	b.WriteString("\t\t\t\"items\": schema.ListNestedAttribute{\n")
	b.WriteString("\t\t\t\tComputed: true,\n")
	b.WriteString("\t\t\t\tMarkdownDescription: \"The objects the filters selected.\",\n")
	b.WriteString("\t\t\t\tNestedObject: schema.NestedAttributeObject{\n")
	b.WriteString("\t\t\t\t\tAttributes: map[string]schema.Attribute{\n")
	b.WriteString(sb.attributeDecls(itemNodes, 6))
	b.WriteString("\t\t\t\t\t},\n\t\t\t\t},\n\t\t\t},\n")
	d.SchemaAttributes = b.String()

	description := entityDescription(itemTree, "Reads "+ds.Names.Key+" objects by filter.")
	if ds.CoManagementNote != "" {
		description += " " + ds.CoManagementNote
	}
	d.SchemaDescription = strconv.Quote(description)
	d.Imports = imports.render()

	// Models: a fixed root plus the item element structs.
	d.ItemModel = d.Pascal + "ItemModel"
	itemDecls := buildModels(d.ItemModel, d.Pascal+"Item", itemNodes, nil)
	// The root carries the two filter inputs and the item list, plus one
	// field per addressing attribute the schema declares — the path
	// parameters of a parent-scoped collection, which the read has to fill
	// from config and which no item model can hold.
	root := "type " + d.Type + "Model struct {\n"
	for _, a := range append(companionAddressing(ds), filters...) {
		root += "\t" + ir.GoName(a.Name) + " " + scalarSchemaType(a.Kind).ValueType +
			" `tfsdk:\"" + a.Name + "\"`\n"
	}
	root += "\tItems []" + d.ItemModel + " `tfsdk:\"items\"`\n" +
		"\tTimeouts timeouts.Value `tfsdk:\"timeouts\"`\n" +
		"}"
	d.Models = root + "\n\n" + renderModelDecls(itemDecls)
	d.ModelImports = e.datasourceModelImports(d.Models)

	// Calls. The list call's path parameters are filled from the companion's
	// own attributes, not from the item element's: a parent-scoped collection
	// is reached through org or owner, which sit on the datasource beside the
	// filter inputs and never on an item. Resolving them against the element
	// found its id instead and read the wrong field.
	listPlan, err := buildCallPlan(db.List, "result", addressingNodes, "data", respDiagnostics())
	if err != nil {
		return fixtures.Fixture{}, fmt.Errorf("list: %w", err)
	}
	if listPlan.Payload == "" {
		return fixtures.Fixture{}, unrenderable("list: the bound list call yields no payload")
	}
	d.ListPlan = listPlan
	d.Collection = "result"
	if db.CollectionAccess != "" {
		d.Collection = "result." + db.CollectionAccess
	}
	d.ElementType = db.ElementType
	if d.ElementType == "" {
		return fixtures.Fixture{}, unrenderable("list: the binding names no element type")
	}

	if d.HasIDFilter {
		d.IDField = ir.GoName("id")
		// The id filter takes the type of the field it selects on, which is
		// whatever the document declares — an integer key is as common as a
		// string one. Building the parameter through the shared plan is what
		// converts it, rather than assuming the attribute is a string.
		readPlan, rerr := buildCallPlan(db.Read, "remote", []node{{attr: idFilter}}, "data", respDiagnostics())
		if rerr != nil {
			return fixtures.Fixture{}, fmt.Errorf("read: %w", rerr)
		}
		d.ReadPlan = readPlan
		d.ReadItem = itemPayloadExpr(db)
	}

	readImports := newImportSet(e.pc.Module)
	readImports.add("", "context")
	readImports.add("", "github.com/hashicorp/terraform-plugin-framework/datasource")
	readImports.add("", e.pc.Module+"/internal/services/common/crud")
	readImports.add("", e.pc.Module+"/internal/services/common/errors")
	e.addSDKImports(readImports, d.ListPlan.Assign, d.ReadPlan.Assign)
	addPlanImports(readImports, d.ListPlan, d.ReadPlan)
	d.ReadImports = readImports.render()

	// Item mapping.
	mapBody, err := stateLines(itemNodes, d.Pascal+"Item", "remote", "item", 1)
	if err != nil {
		return fixtures.Fixture{}, err
	}
	d.MapItemBody = mapBody
	stateImports := newImportSet(e.pc.Module)
	stateImports.add("", "context")
	stateImports.add("", "github.com/hashicorp/terraform-plugin-framework/diag")
	if strings.Contains(mapBody, "types.") {
		stateImports.add("", "github.com/hashicorp/terraform-plugin-framework/types")
	}
	if strings.Contains(mapBody, "convert.") {
		stateImports.add("", e.pc.Module+"/internal/services/common/convert")
	}
	e.addSDKImports(stateImports, mapBody, d.ElementType)
	d.StateImports = stateImports.render()

	addressing := fixtures.Derive(&ir.AttributeTree{Attributes: companionAddressing(ds)})
	addressing.PinNumeric(integerParsedParams(db.List, addressingNodes))
	d.AddressingHCL = addressing.HCL(fixtures.ConfigMinimal)

	spec := deriveFixtures(itemTree, itemNodes)
	e.datasourceMocks(d, ds, spec)
	e.datasourceChecks(d, ds.Names.Key, spec)
	return spec, nil
}

// itemPayloadExpr renders the by-id read's payload — always the local
// "remote" — as one list element: as-is when the read answers with the
// element type itself (the kiota interface shape), dereferenced when it
// answers with a pointer to it (the openapi-generator struct shape), and
// empty when neither holds, which switches the id filter off.
func itemPayloadExpr(db *sdkbind.DatasourceBinding) string {
	switch {
	case db.Read == nil || db.ElementType == "":
		return ""
	case db.Read.ResponseType == db.ElementType:
		return "remote"
	case db.Read.ResponseType == "*"+db.ElementType:
		return "*remote"
	}
	return ""
}

// companionItemTree finds the items attribute's element tree.
func companionItemTree(ds *ir.Datasource) *ir.AttributeTree {
	if ds.Schema == nil {
		return nil
	}
	for _, a := range ds.Schema.Attributes {
		if a.Name == "items" {
			return a.Nested
		}
	}
	return nil
}

// hasNode reports whether a node level carries the named attribute.
func hasNode(nodes []node, name string) bool {
	for _, n := range nodes {
		if n.attr.Name == name {
			return true
		}
	}
	return false
}

// datasourceModelImports renders the model file's imports.
func (e *serviceRenderer) datasourceModelImports(models string) string {
	imports := newImportSet(e.pc.Module)
	imports.add("", "github.com/hashicorp/terraform-plugin-framework-timeouts/datasource/timeouts")
	if strings.Contains(models, "types.") {
		imports.add("", "github.com/hashicorp/terraform-plugin-framework/types")
	}
	if strings.Contains(models, "attr.") {
		imports.add("", "github.com/hashicorp/terraform-plugin-framework/attr")
	}
	return imports.render()
}

// datasourceMocks fills the responder context.
func (e *serviceRenderer) datasourceMocks(d *datasourceData, ds *ir.Datasource, spec fixtures.Fixture) {
	d.RegistryName = ds.Names.TerraformType + ".data"
	d.ResponseMaximal = string(spec.WireJSON(fixtures.ResponseMaximal))
	d.ListPayload = listPayloadExpr(ds.ListEnvelopeKey, "[]map[string]any{object()}")
	if ds.Operations.List != nil {
		// A parent-scoped collection is requested with its addressing
		// substituted in, so the mock matches the shape rather than the
		// template. httpmock reads the =~ prefix as a regular expression.
		d.CollectionURL = mockURL(ds.Operations.List.PathTemplate)
		if len(companionAddressing(ds)) > 0 {
			d.CollectionURL = mockPattern(ds.Operations.List.PathTemplate)
		}
	}
	if ds.Operations.Read != nil {
		d.ItemPattern = mockPattern(ds.Operations.Read.PathTemplate)
		d.HasItemMock = true
	}

	imports := newImportSet(e.pc.Module)
	imports.add("", "encoding/json")
	imports.add("", "github.com/jarcoal/httpmock")
	imports.add("providermocks", e.pc.Module+"/internal/mocks")
	d.MocksImports = imports.render()

	testImports := newImportSet(e.pc.Module)
	testImports.add("", "context")
	testImports.add("_", "embed")
	testImports.add("", "regexp")
	testImports.add("", "testing")
	testImports.add("fwdatasource", "github.com/hashicorp/terraform-plugin-framework/datasource")
	testImports.add("", "github.com/hashicorp/terraform-plugin-testing/helper/resource")
	testImports.add("", e.pc.Module+"/internal/mocks")
	testImports.add(d.Package, d.PackagePath)
	testImports.add("_", d.PackagePath+"/mocks")
	d.TestImports = testImports.render()
}

// datasourceChecks builds the unit-test checks for the read result.
func (e *serviceRenderer) datasourceChecks(d *datasourceData, key string, spec fixtures.Fixture) {
	address := "data." + d.TerraformType + ".test"
	var b strings.Builder
	prefix := ""
	if !d.LookupByKey {
		fmt.Fprintf(&b, "\t\t\t\t\tresource.TestCheckResourceAttr(%q, %q, %q),\n", address, "items.#", "1")
		prefix = "items.0."
	}
	for _, v := range spec.Entries {
		if v.Nested != nil || v.Kind == ir.TypeList {
			continue
		}
		// A map is addressed by its element count or by a key, never as one
		// value: terraform refuses a plain check on it and says so.
		if v.Kind == ir.TypeMap {
			fmt.Fprintf(&b, "\t\t\t\t\tresource.TestCheckResourceAttr(%q, %q, %q),\n",
				address, prefix+v.Name+".%", "1")
			continue
		}
		if d.LookupByKey && v.ComputedOptionalRequired == ir.Required {
			continue // the key argument, echoed from config, not the answer
		}
		// An attribute the join kept with no SDK field behind it reaches the
		// schema, and nothing fills it: a datasource maps what the SDK
		// answers with and has no path parameter to fall back on the way a
		// resource does. Asserting a value for it fails on a null.
		if e.keptUnbound[keptUnboundKey(bindingKindDatasource, key, v.Name)] {
			continue
		}
		fmt.Fprintf(&b, "\t\t\t\t\tresource.TestCheckResourceAttr(%q, %q, %s),\n",
			address, prefix+v.Name, strconv.Quote(checkValue(v.Scalar)))
	}
	d.UnitChecks = b.String()
}

// datasourceFixtures emits the terraform fixture, the response fixture and
// the example.
func (e *serviceRenderer) datasourceFixtures(ds *ir.Datasource, spec fixtures.Fixture, addressingHCL, dir string) ([]File, error) {
	source := ds.Names.Key
	blockHeader := fmt.Sprintf("data %q %q", ds.Names.TerraformType, "test")

	var body string
	if ds.LookupByKey {
		body = spec.HCL(fixtures.ConfigMinimal)
	} else {
		// The addressing the collection path requires, and no filter: every
		// filter is optional, and a fixture that set one would assert the
		// whole collection matched it.
		body = addressingHCL
	}
	fixture, err := hclBlock(source, blockHeader, body, nil)
	if err != nil {
		return nil, err
	}

	var response []byte
	if ds.LookupByKey {
		response = spec.WireJSON(fixtures.ResponseMaximal)
	} else {
		item := strings.TrimSuffix(string(spec.WireJSON(fixtures.ResponseMaximal)), "\n")
		// The committed list-response fixture wraps under the observed
		// envelope key, or is a bare array, so it and the unit-test mock
		// agree on the shape the API actually returns — never an assumed
		// "value".
		response = []byte(listResponseJSON(ds.ListEnvelopeKey, item))
	}

	exampleHeader := fmt.Sprintf("data %q %q", ds.Names.TerraformType, "example")
	example, err := hclBlock(source, exampleHeader, body, nil)
	if err != nil {
		return nil, err
	}

	return []File{
		rawFile(path.Join(dir, "tests/terraform/unit/datasource.tf"), source, fixture),
		rawFile(path.Join(dir, "tests/responses/datasource.json"), source, response),
		rawFile(path.Join("examples/data-sources", ds.Names.TerraformType, "data-source.tf"), source, example),
	}, nil
}

// reindentJSON shifts a rendered JSON block right by the given prefix.
func reindentJSON(block, prefix string) string {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// companionAddressing is the companion datasource's attributes that are
// neither of the filter inputs nor the item list: the path parameters
// derivation added so a parent-scoped collection can be reached.
func companionAddressing(ds *ir.Datasource) []ir.Attribute {
	if ds == nil || ds.Schema == nil {
		return nil
	}
	var out []ir.Attribute
	for _, a := range ds.Schema.Attributes {
		if a.Name == "items" || a.Filter {
			continue
		}
		if a.Nested == nil {
			out = append(out, a)
		}
	}
	return out
}

// filterNamed answers the named filter, and whether there is one.
func filterNamed(filters []ir.Attribute, name string) (ir.Attribute, bool) {
	for _, a := range filters {
		if a.Name == name {
			return a, true
		}
	}
	return ir.Attribute{}, false
}

// filterChecks renders the body of the generated match: one early return per
// filter the configuration set, comparing the terraform value the caller gave
// against the one the mapped item carries. Both sides are the same terraform
// type, so equality is the framework's own and needs no conversion.
//
// A filter the configuration left null is not a filter — it narrows nothing —
// which is what makes several of them combine and none of them mandatory.
func filterChecks(filters []ir.Attribute) string {
	var b strings.Builder
	for _, a := range filters {
		field := ir.GoName(a.Name)
		fmt.Fprintf(&b, "\tif !config.%s.IsNull() && !config.%s.Equal(item.%s) {\n\t\treturn false\n\t}\n",
			field, field, field)
	}
	b.WriteString("\treturn true")
	return b.String()
}

// companionFilters is the arguments that select which listed objects come
// back: one per scalar field at the root of a listed object.
func companionFilters(ds *ir.Datasource) []ir.Attribute {
	if ds == nil || ds.Schema == nil {
		return nil
	}
	var out []ir.Attribute
	for _, a := range ds.Schema.Attributes {
		if a.Filter {
			out = append(out, a)
		}
	}
	return out
}
