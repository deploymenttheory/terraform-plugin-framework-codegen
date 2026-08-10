package emit

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/fixturespec"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/sdkbind"
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
	IDParamDecl string
	// ReadItem renders the by-id read's payload as one list element:
	// "remote", or "*remote" when the read answers with a pointer to the
	// element type.
	ReadItem    string
	ReadPlan    callPlan
	ListPlan    callPlan
	Collection  string
	ItemModel   string
	ElementType string
	MapItemBody string
	// Lookup fields.
	ReadModel string
	StateBody string

	// Test and mock fields.
	RegistryName    string
	CollectionURL   string
	ItemPattern     string
	HasItemMock     bool
	ResponseMaximal string
	ListWrap        string
	UnitChecks      string
}

// datasource renders one datasource's complete file set.
func (e *entityRenderer) datasource(ds *ir.Datasource, db *sdkbind.DatasourceBinding) ([]File, error) {
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

	var spec fixturespec.Spec
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
		f, ferr := e.renderEntityFile("datasource/"+tmpl, path.Join(dir, out), ds.Names.Key, d)
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

	fixtures, err := e.datasourceFixtures(ds, spec, dir)
	if err != nil {
		return nil, err
	}
	files = append(files, fixtures...)
	return files, nil
}

// keyAttrName is the terraform spelling of a lookup datasource's key
// parameter.
func keyAttrName(ds *ir.Datasource) string {
	return ir.SnakeName(ds.KeyParameter)
}

// lookupDatasource fills the render context for a lookup-by-key
// datasource: the key parameter is the single required argument and the
// entity's object comes back.
func (e *entityRenderer) lookupDatasource(d *datasourceData, ds *ir.Datasource, db *sdkbind.DatasourceBinding) (fixturespec.Spec, error) {
	if ds.Ops.Read == nil || db.Read == nil {
		return fixturespec.Spec{}, fmt.Errorf("a lookup-by-key datasource needs a bound read call")
	}

	nodes := joinTree(ds.Schema, db.Fields)
	key := keyAttrName(ds)
	if !hasNode(nodes, key) {
		// The SDK model does not carry the key parameter as a field; the
		// schema still must, so the caller has somewhere to put it.
		nodes = append([]node{{attr: ir.Attribute{
			Name: key, WireName: ds.KeyParameter,
			Kind: ir.TypeString, Presence: ir.PresenceRequired,
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
	description := "Reads one " + ds.Names.Key + " by its " + key + "."
	if ds.CoManagementNote != "" {
		description += " " + ds.CoManagementNote
	}
	d.SchemaDescription = strconv.Quote(description)
	d.Imports = imports.render()

	decls, err := buildModels(d.Type+"Model", d.Pascal+"Lookup", nodes,
		[]string{"Timeouts timeouts.Value `tfsdk:\"timeouts\"`"})
	if err != nil {
		return fixturespec.Spec{}, err
	}
	d.Models = renderModelDecls(decls)
	d.ModelImports = e.datasourceModelImports(d.Models)

	plan, err := buildCallPlan(db.Read, "remote", nodes, "data")
	if err != nil {
		return fixturespec.Spec{}, fmt.Errorf("read: %w", err)
	}
	if plan.Payload == "" {
		return fixturespec.Spec{}, fmt.Errorf("read: the bound read call yields no payload to map from")
	}
	d.ReadPlan = plan

	readImports := newImportSet(e.pc.Module)
	readImports.add("", "context")
	readImports.add("", "github.com/hashicorp/terraform-plugin-framework/datasource")
	readImports.add("", e.pc.Module+"/internal/services/common/crud")
	readImports.add("", e.pc.Module+"/internal/services/common/errors")
	e.addSDKImports(readImports, plan.Assign)
	d.ReadImports = readImports.render()

	stateBody, err := stateLines(nodes, d.Pascal+"Lookup", "remote", "data", 1)
	if err != nil {
		return fixturespec.Spec{}, err
	}
	d.StateBody = stateBody
	stateImports := newImportSet(e.pc.Module)
	stateImports.add("", "context")
	if strings.Contains(stateBody, "convert.") {
		stateImports.add("", e.pc.Module+"/internal/services/common/convert")
	}
	e.addSDKImports(stateImports, stateBody, d.ReadModel)
	d.StateImports = stateImports.render()

	spec := deriveFixtures(ds.Schema, nodes)
	e.datasourceMocks(d, ds, spec)
	e.datasourceChecks(d, spec)
	return spec, nil
}

// companionDatasource fills the render context for the
// filter_type/filter_value/items pattern.
func (e *entityRenderer) companionDatasource(d *datasourceData, ds *ir.Datasource, db *sdkbind.DatasourceBinding) (fixturespec.Spec, error) {
	if ds.Ops.List == nil || db.List == nil {
		return fixturespec.Spec{}, fmt.Errorf("a companion datasource needs a bound list call")
	}
	itemTree := companionItemTree(ds)
	if itemTree == nil {
		return fixturespec.Spec{}, fmt.Errorf("the companion schema carries no items attribute")
	}
	itemNodes := joinTree(itemTree, db.Fields)

	// The id filter needs the by-id read's payload to bridge to one list
	// element: identical types, or a pointer the element type sits behind.
	// A read shaped any other way keeps the filter off rather than guessed.
	d.HasIDFilter = ds.Ops.Read != nil && db.Read != nil && len(db.Read.Params) == 1 &&
		db.Read.Params[0].GoType == "string" && itemPayloadExpr(db) != ""

	// Schema: the two filter inputs, then the computed items list.
	imports := newImportSet(e.pc.Module)
	imports.add("", "context")
	imports.add("", "fmt")
	imports.add("", "time")
	imports.add("", "github.com/hashicorp/terraform-plugin-framework/datasource")
	imports.add("schema", "github.com/hashicorp/terraform-plugin-framework/datasource/schema")
	imports.add("", "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator")
	imports.add("", "github.com/hashicorp/terraform-plugin-framework/schema/validator")
	imports.add("sdk", e.bindings.SDK.ImportPath)
	imports.add("commonschema", e.pc.Module+"/internal/services/common/schema")
	sb := &schemaBuilder{kind: schemaDatasource, imports: imports}

	allowed := []string{`"all"`}
	if d.HasIDFilter {
		allowed = append(allowed, `"id"`)
	}
	var b strings.Builder
	b.WriteString("\t\t\t\"filter_type\": schema.StringAttribute{\n")
	b.WriteString("\t\t\t\tRequired: true,\n")
	fmt.Fprintf(&b, "\t\t\t\tDescription: %s,\n", strconv.Quote("Which objects to return: "+strings.ReplaceAll(strings.Join(allowed, " | "), `"`, "")+"."))
	fmt.Fprintf(&b, "\t\t\t\tValidators: []validator.String{stringvalidator.OneOf(%s)},\n", strings.Join(allowed, ", "))
	b.WriteString("\t\t\t},\n")
	b.WriteString("\t\t\t\"filter_value\": schema.StringAttribute{\n")
	b.WriteString("\t\t\t\tOptional: true,\n")
	b.WriteString("\t\t\t\tDescription: \"The value the filter matches. Not read when filter_type is all.\",\n")
	b.WriteString("\t\t\t},\n")
	b.WriteString("\t\t\t\"items\": schema.ListNestedAttribute{\n")
	b.WriteString("\t\t\t\tComputed: true,\n")
	b.WriteString("\t\t\t\tDescription: \"The objects the filter selected.\",\n")
	b.WriteString("\t\t\t\tNestedObject: schema.NestedAttributeObject{\n")
	b.WriteString("\t\t\t\t\tAttributes: map[string]schema.Attribute{\n")
	b.WriteString(sb.attributeDecls(itemNodes, 6))
	b.WriteString("\t\t\t\t\t},\n\t\t\t\t},\n\t\t\t},\n")
	d.SchemaAttributes = b.String()

	description := "Reads " + ds.Names.Key + " objects by filter."
	if ds.CoManagementNote != "" {
		description += " " + ds.CoManagementNote
	}
	d.SchemaDescription = strconv.Quote(description)
	d.Imports = imports.render()

	// Models: a fixed root plus the item element structs.
	d.ItemModel = d.Pascal + "ItemModel"
	itemDecls, err := buildModels(d.ItemModel, d.Pascal+"Item", itemNodes, nil)
	if err != nil {
		return fixturespec.Spec{}, err
	}
	root := "type " + d.Type + "Model struct {\n" +
		"\tFilterType types.String `tfsdk:\"filter_type\"`\n" +
		"\tFilterValue types.String `tfsdk:\"filter_value\"`\n" +
		"\tItems []" + d.ItemModel + " `tfsdk:\"items\"`\n" +
		"\tTimeouts timeouts.Value `tfsdk:\"timeouts\"`\n" +
		"}"
	d.Models = root + "\n\n" + renderModelDecls(itemDecls)
	d.ModelImports = e.datasourceModelImports(d.Models)

	// Calls.
	listPlan, err := buildCallPlan(db.List, "result", itemNodes, "data")
	if err != nil {
		return fixturespec.Spec{}, fmt.Errorf("list: %w", err)
	}
	if listPlan.Payload == "" {
		return fixturespec.Spec{}, fmt.Errorf("list: the bound list call yields no payload")
	}
	d.ListPlan = listPlan
	d.Collection = "result"
	if db.CollectionAccess != "" {
		d.Collection = "result." + db.CollectionAccess
	}
	d.ElementType = db.ElementType
	if d.ElementType == "" {
		return fixturespec.Spec{}, fmt.Errorf("list: the binding names no element type")
	}

	if d.HasIDFilter {
		p := db.Read.Params[0]
		d.IDParamDecl = p.Local + " := data.FilterValue.ValueString()"
		readPlan, rerr := readPlanWithoutParams(db.Read, "remote")
		if rerr != nil {
			return fixturespec.Spec{}, fmt.Errorf("read: %w", rerr)
		}
		d.ReadPlan = readPlan
		d.ReadItem = itemPayloadExpr(db)
	}

	readImports := newImportSet(e.pc.Module)
	readImports.add("", "context")
	readImports.add("", "fmt")
	readImports.add("", "github.com/hashicorp/terraform-plugin-framework/datasource")
	readImports.add("", e.pc.Module+"/internal/services/common/crud")
	readImports.add("", e.pc.Module+"/internal/services/common/errors")
	e.addSDKImports(readImports, d.ListPlan.Assign, d.ReadPlan.Assign)
	d.ReadImports = readImports.render()

	// Item mapping.
	mapBody, err := stateLines(itemNodes, d.Pascal+"Item", "remote", "item", 1)
	if err != nil {
		return fixturespec.Spec{}, err
	}
	d.MapItemBody = mapBody
	stateImports := newImportSet(e.pc.Module)
	stateImports.add("", "context")
	if strings.Contains(mapBody, "convert.") {
		stateImports.add("", e.pc.Module+"/internal/services/common/convert")
	}
	e.addSDKImports(stateImports, mapBody, d.ElementType)
	d.StateImports = stateImports.render()

	spec := deriveFixtures(itemTree, itemNodes)
	e.datasourceMocks(d, ds, spec)
	e.datasourceChecks(d, spec)
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

// readPlanWithoutParams renders a call plan whose parameter locals are
// declared by the surrounding code rather than from model fields.
func readPlanWithoutParams(call *sdkbind.Call, payloadName string) (callPlan, error) {
	stripped := *call
	stripped.Params = nil
	return buildCallPlan(&stripped, payloadName, nil, "data")
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
func (e *entityRenderer) datasourceModelImports(models string) string {
	imports := newImportSet(e.pc.Module)
	imports.add("", "github.com/hashicorp/terraform-plugin-framework-timeouts/datasource/timeouts")
	if strings.Contains(models, "types.") {
		imports.add("", "github.com/hashicorp/terraform-plugin-framework/types")
	}
	return imports.render()
}

// datasourceMocks fills the responder context.
func (e *entityRenderer) datasourceMocks(d *datasourceData, ds *ir.Datasource, spec fixturespec.Spec) {
	d.RegistryName = ds.Names.TerraformType + ".data"
	d.ResponseMaximal = string(spec.WireJSON(fixturespec.ResponseMaximal))
	d.ListWrap = "value"
	if ds.Ops.List != nil {
		d.CollectionURL = mockURL(ds.Ops.List.PathTemplate)
	}
	if ds.Ops.Read != nil {
		d.ItemPattern = mockPattern(ds.Ops.Read.PathTemplate)
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
func (e *entityRenderer) datasourceChecks(d *datasourceData, spec fixturespec.Spec) {
	address := "data." + d.TerraformType + ".test"
	var b strings.Builder
	prefix := ""
	if !d.LookupByKey {
		fmt.Fprintf(&b, "\t\t\t\t\tresource.TestCheckResourceAttr(%q, %q, %q),\n", address, "items.#", "1")
		prefix = "items.0."
	}
	for _, v := range spec.Values {
		if v.Nested != nil || v.Kind == ir.TypeList {
			continue
		}
		if d.LookupByKey && v.Presence == ir.PresenceRequired {
			continue // the key argument, echoed from config, not the answer
		}
		fmt.Fprintf(&b, "\t\t\t\t\tresource.TestCheckResourceAttr(%q, %q, %s),\n",
			address, prefix+v.Name, strconv.Quote(checkValue(v.Scalar)))
	}
	d.UnitChecks = b.String()
}

// datasourceFixtures emits the terraform fixture, the response fixture and
// the example.
func (e *entityRenderer) datasourceFixtures(ds *ir.Datasource, spec fixturespec.Spec, dir string) ([]File, error) {
	source := ds.Names.Key
	blockHeader := fmt.Sprintf("data %q %q", ds.Names.TerraformType, "test")

	var body string
	if ds.LookupByKey {
		body = spec.HCL(fixturespec.ConfigMinimal)
	} else {
		body = "  filter_type = \"all\"\n"
	}
	fixture, err := hclBlock(source, blockHeader, body, nil)
	if err != nil {
		return nil, err
	}

	var response []byte
	if ds.LookupByKey {
		response = spec.WireJSON(fixturespec.ResponseMaximal)
	} else {
		item := strings.TrimSuffix(string(spec.WireJSON(fixturespec.ResponseMaximal)), "\n")
		response = []byte("{\n  \"value\": [\n" + reindentJSON(item, "    ") + "\n  ]\n}\n")
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
