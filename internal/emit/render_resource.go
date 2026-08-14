package emit

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/fixtures"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// resourceData is the render context every resource template consumes.
// Every field is a finished expression, declaration or presence boolean.
type resourceData struct {
	Source        string
	Package       string
	PackagePath   string
	Key           string
	Pascal        string
	Type          string
	TerraformType string
	ClientType    string

	Imports          string
	ModelImports     string
	ConstructImports string
	StateImports     string
	CRUDImports      string
	TestImports      string
	AccImports       string
	ValidatorImports string
	MocksImports     string

	TimeoutCreate string
	TimeoutRead   string
	TimeoutUpdate string
	TimeoutDelete string

	HasImport  bool
	ImportAttr string
	// IdentityAttributes is the identity schema's attribute declarations,
	// empty for a resource nothing lists.
	IdentityAttributes string

	SchemaDescription string
	SchemaAttributes  string
	Models            string

	ConstructReturnType string
	WriteConstructor    string
	ConstructBody       string
	// The update's own request body, when the API declares one the create's
	// cannot serve.
	HasUpdateBody             bool
	UpdateConstructReturnType string
	UpdateWriteConstructor    string
	UpdateConstructBody       string
	HasUpdate                 bool
	// Singleton marks the resource as one object at a fixed path: created
	// by writing, destroyed by forgetting.
	Singleton bool
	// SingletonID is the constant a singleton publishes as its id, since
	// there is no collection member to take one from.
	SingletonID string

	ReadModel string
	StateBody string

	CreatePlan         callPlan
	ReadPlan           callPlan
	UpdatePlan         callPlan
	DeletePlan         callPlan
	CreateMapsResponse bool
	UpdateMapsResponse bool
	UpdateParamCopies  string
	MissingUpdate      bool

	HasEC      bool
	ECDuration string

	HasValidators        bool
	ConfigValidatorExprs string
	CustomValidatorDecls string
	MinimalChecks        string
	MaximalChecks        string
	ProviderModule       string

	// Mock responder fields.
	RegistryName    string
	CollectionURL   string
	ItemPattern     string
	IDSegmentIndex  int
	IDWire          string
	ResponseMinimal string
	ResponseMaximal string
	CreateStatus    int
	UpdateStatus    int
	UpdateMethod    string
	DeleteStatus    int
	HasList         bool
	ListPayload     string
	HasDelete       bool
}

// resource renders one resource's complete file set.
func (e *serviceRenderer) resource(r *ir.Resource, rb *sdkbind.ResourceBinding) ([]File, error) {
	// A singleton has neither: it is written through its update and
	// forgotten on destroy, so a read and an update are all it needs.
	if r.Singleton {
		if r.Operations.Read == nil || rb.Read == nil || r.Operations.Update == nil || rb.Update == nil {
			return nil, unrenderable("a singleton resource needs bound read and update calls")
		}
	} else if r.Operations.Create == nil || rb.Create == nil || r.Operations.Read == nil || rb.Read == nil ||
		r.Operations.Delete == nil || rb.Delete == nil {
		return nil, unrenderable("a resource needs bound create, read and delete calls")
	}

	nodes := e.joinTree(bindingKindResource, r.Names.Key, r.Schema, rb.Fields, addressingNames(
		r.Operations.Read, r.Operations.Create, r.Operations.Update, r.Operations.Delete))
	d := &resourceData{
		Package:        r.Names.Package,
		PackagePath:    e.packagePath(kindResources, r.Names),
		Key:            r.Names.Key,
		Pascal:         r.Names.Pascal,
		Type:           r.Names.Pascal + "Resource",
		TerraformType:  r.Names.TerraformType,
		ClientType:     "*sdk." + e.bindings.SDK.ClientTypeName,
		TimeoutCreate:  goDuration(int64(r.Timeouts.Create)),
		TimeoutRead:    goDuration(int64(r.Timeouts.Read)),
		TimeoutUpdate:  goDuration(int64(r.Timeouts.Update)),
		TimeoutDelete:  goDuration(int64(r.Timeouts.Delete)),
		MissingUpdate:  r.MissingUpdate || r.Operations.Update == nil || rb.Update == nil,
		ReadModel:      rb.ReadModel,
		ProviderModule: e.pc.Module,
	}
	d.HasUpdate = !d.MissingUpdate
	d.Singleton = r.Singleton
	if r.Singleton {
		// One object at a fixed path has exactly one address, so its id is
		// a constant rather than anything the API answers with.
		d.SingletonID = r.Names.TerraformType
	}
	if r.EventualConsistency > 0 {
		d.HasEC = true
		d.ECDuration = goDuration(int64(r.EventualConsistency))
	}

	if err := e.resourceCode(d, r, rb, nodes); err != nil {
		return nil, err
	}
	if err := e.resourceCRUD(d, rb, nodes); err != nil {
		return nil, err
	}
	spec := deriveFixtures(r.Schema, nodes)
	if !d.Singleton {
		if err := e.resourceMocks(d, r, rb, spec); err != nil {
			return nil, err
		}
	}
	e.resourceChecks(d, spec)

	dir := e.dir(kindResources, r.Names)
	var files []File

	renderGo := func(tmpl, out string) error {
		d.Source = "entity/resource/" + tmpl
		f, err := e.renderServiceFile("resource/"+tmpl, path.Join(dir, out), r.Names.Key, d)
		if err != nil {
			return err
		}
		files = append(files, f)
		return nil
	}

	goFiles := []struct{ tmpl, out string }{
		{"resource.go.tmpl", "resource.go"},
		{"model.go.tmpl", "model.go"},
		{"construct.go.tmpl", "construct.go"},
		{"state.go.tmpl", "state.go"},
		{"crud.go.tmpl", "crud.go"},
		{"modify_plan.go.tmpl", "modify_plan.go"},
		{"resource_acceptance_test.go.tmpl", "resource_acceptance_test.go"},
	}
	// The stateful mock keys on a collection URL and an item path carrying an
	// identifier segment, and a singleton has neither: one object, one fixed
	// path, no create and no delete. Its unit test and responders are
	// therefore not emitted — the acceptance test still is. Giving the mock a
	// singleton shape is worth doing; guessing a collection for it is not.
	if !d.Singleton {
		goFiles = append(goFiles,
			struct{ tmpl, out string }{"resource_test.go.tmpl", "resource_test.go"},
			struct{ tmpl, out string }{"responders.go.tmpl", "mocks/responders.go"})
	}
	for _, gf := range goFiles {
		if err := renderGo(gf.tmpl, gf.out); err != nil {
			return nil, err
		}
	}
	if d.HasValidators {
		if err := renderGo("conditional_validators.go.tmpl", "conditional_validators.go"); err != nil {
			return nil, err
		}
	}

	fixtures, err := e.resourceFixtures(r, spec, dir)
	if err != nil {
		return nil, err
	}
	files = append(files, fixtures...)

	return files, nil
}

// resourceCode builds the schema, model, construct and state contexts.
func (e *serviceRenderer) resourceCode(d *resourceData, r *ir.Resource, rb *sdkbind.ResourceBinding, nodes []node) error {
	// Schema.
	imports := newImportSet(e.pc.Module)
	imports.add("", "context")
	imports.add("", "fmt")
	imports.add("", "time")
	imports.add("", "github.com/hashicorp/terraform-plugin-framework/resource")
	imports.add("schema", "github.com/hashicorp/terraform-plugin-framework/resource/schema")
	imports.add("sdk", e.bindings.SDK.ImportPath)
	imports.add("commonschema", e.pc.Module+"/internal/services/common/schema")

	byName := map[string]node{}
	for _, n := range nodes {
		byName[n.attr.Name] = n
	}
	deps, err := dependencyMap(r.Schema, byName)
	if err != nil {
		return err
	}

	sb := &schemaBuilder{kind: schemaResource, imports: imports, deps: deps, rootDepth: 3}
	d.SchemaAttributes = sb.attributeDecls(nodes, 3)

	// A resource declares an identity when it is listed: a list resource's
	// results are identities, and the framework reads the schema they
	// conform to off the resource. Recorded so the list resource emits
	// results in exactly this shape.
	if e.listed[r.Names.Key] {
		if identity := resourceIdentity(r); len(identity) > 0 {
			e.identities[r.Names.Key] = identity
			d.IdentityAttributes = identitySchemaDecls(identity, 3)
			imports.add("identityschema", "github.com/hashicorp/terraform-plugin-framework/resource/identityschema")
		}
	}

	description := entityDescription(r.Schema, "Manages the "+r.Names.Key+" entity.")
	if r.CoManagementNote != "" {
		description += " " + r.CoManagementNote
	}
	d.SchemaDescription = strconv.Quote(description)

	if d.HasImport = importAttr(r, nodes) != ""; d.HasImport {
		d.ImportAttr = importAttr(r, nodes)
		imports.add("", "github.com/hashicorp/terraform-plugin-framework/path")
	}
	d.Imports = imports.render()

	// Model.
	decls := buildModels(d.Type+"Model", d.Pascal, nodes,
		[]string{"Timeouts timeouts.Value `tfsdk:\"timeouts\"`"})
	d.Models = renderModelDecls(decls)
	modelImports := newImportSet(e.pc.Module)
	modelImports.add("", "github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts")
	if strings.Contains(d.Models, "types.") {
		modelImports.add("", "github.com/hashicorp/terraform-plugin-framework/types")
	}
	d.ModelImports = modelImports.render()

	// Construct.
	d.ConstructReturnType = "*" + rb.WriteModel
	d.WriteConstructor = rb.WriteConstructor
	body, usesFmt, err := constructLines(nodes, "data", "body", "", 1, true)
	if err != nil {
		return err
	}
	d.ConstructBody = body

	// The update's own body, where the API declares one the create's cannot
	// serve. It is built from the same plan against the update's own model,
	// so a field only one of them carries lands only where it belongs.
	updateBody := ""
	updateUsesFmt := false
	if rb.UpdateWriteModel != "" {
		updateNodes := e.joinTree(bindingKindResource, r.Names.Key, r.Schema, rb.UpdateFields, addressingNames(
			r.Operations.Read, r.Operations.Create, r.Operations.Update, r.Operations.Delete))
		updateBody, updateUsesFmt, err = constructLines(updateNodes, "data", "body", "", 1, false)
		if err != nil {
			return err
		}
		d.HasUpdateBody = true
		d.UpdateConstructReturnType = "*" + rb.UpdateWriteModel
		d.UpdateWriteConstructor = rb.UpdateWriteConstructor
		d.UpdateConstructBody = updateBody
	}

	constructImports := newImportSet(e.pc.Module)
	constructImports.add("", "context")
	if usesFmt || updateUsesFmt {
		constructImports.add("", "fmt")
	}
	if strings.Contains(body+updateBody, "convert.") {
		constructImports.add("", e.pc.Module+"/internal/services/common/convert")
	}
	e.addSDKImports(constructImports, body, d.ConstructReturnType, d.WriteConstructor,
		updateBody, d.UpdateConstructReturnType, d.UpdateWriteConstructor)
	d.ConstructImports = constructImports.render()

	// State.
	stateBody, err := stateLines(nodes, d.Pascal, "remote", "data", 1)
	if err != nil {
		return err
	}
	d.StateBody = stateBody
	stateImports := newImportSet(e.pc.Module)
	stateImports.add("", "context")
	if strings.Contains(stateBody, "convert.") {
		stateImports.add("", e.pc.Module+"/internal/services/common/convert")
	}
	e.addSDKImports(stateImports, stateBody, d.ReadModel)
	d.StateImports = stateImports.render()

	// Config validators. Dependencies are already realized as attribute-level
	// AlsoRequires in the schema above; this file carries the resource's
	// ConfigValidators method — the stock Conflicting for mutually-exclusive
	// groups and the named custom validators for the value-conditional edges.
	if treeHasConfigValidators(r.Schema) {
		exprs, decls, err := configValidators(d.Type, r.Schema, nodes)
		if err != nil {
			return err
		}
		d.HasValidators = true
		d.ConfigValidatorExprs = exprs
		d.CustomValidatorDecls = decls
		validatorImports := newImportSet(e.pc.Module)
		validatorImports.add("", "context")
		validatorImports.add("", "github.com/hashicorp/terraform-plugin-framework/path")
		validatorImports.add("", "github.com/hashicorp/terraform-plugin-framework/resource")
		if len(r.Schema.MutuallyExclusiveGroups) > 0 {
			validatorImports.add("", "github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator")
		}
		d.ValidatorImports = validatorImports.render()
	}
	return nil
}

// resourceCRUD builds the four lifecycle call plans and the crud imports.
func (e *serviceRenderer) resourceCRUD(d *resourceData, rb *sdkbind.ResourceBinding, nodes []node) error {
	var err error

	// A singleton creates by writing: its create call is its update, and it
	// has no delete call at all.
	createCall := rb.Create
	if d.Singleton {
		createCall = rb.Update
	}

	d.CreateMapsResponse = createCall.ResponseType != "" && createCall.ResponseType == rb.ReadModel
	createPayload := ""
	if d.CreateMapsResponse {
		createPayload = "created"
	}
	if d.CreatePlan, err = buildCallPlan(createCall, createPayload, nodes, "data", respDiagnostics()); err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if d.ReadPlan, err = buildCallPlan(rb.Read, "remote", nodes, "data", respDiagnostics()); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if d.ReadPlan.Payload == "" {
		return unrenderable("read: the bound read call yields no payload to map state from")
	}
	if !d.Singleton {
		if d.DeletePlan, err = buildCallPlan(rb.Delete, "", nodes, "data", respDiagnostics()); err != nil {
			return fmt.Errorf("delete: %w", err)
		}
	}
	if d.HasUpdate {
		d.UpdateMapsResponse = rb.Update.ResponseType != "" && rb.Update.ResponseType == rb.ReadModel
		updatePayload := ""
		if d.UpdateMapsResponse {
			updatePayload = "updated"
		}
		if d.UpdatePlan, err = buildCallPlan(rb.Update, updatePayload, nodes, "prior", respDiagnostics()); err != nil {
			return fmt.Errorf("update: %w", err)
		}
		var copies []string
		for position, p := range rb.Update.Params {
			field, ferr := paramField(p, nodes, position == len(rb.Update.Params)-1)
			if ferr != nil {
				return fmt.Errorf("update: %w", ferr)
			}
			copies = append(copies, "data."+field+" = prior."+field)
		}
		d.UpdateParamCopies = strings.Join(copies, "\n\t")
	}

	imports := newImportSet(e.pc.Module)
	imports.add("", "context")
	imports.add("", "github.com/hashicorp/terraform-plugin-framework/resource")
	imports.add("", e.pc.Module+"/internal/services/common/crud")
	imports.add("", e.pc.Module+"/internal/services/common/errors")
	if d.HasEC {
		imports.add("", "time")
		imports.add("", "github.com/hashicorp/terraform-plugin-framework/tfsdk")
	}
	if d.Singleton {
		imports.add("", "github.com/hashicorp/terraform-plugin-framework/types")
	}
	e.addSDKImports(imports, d.CreatePlan.Assign, d.ReadPlan.Assign, d.DeletePlan.ClosureBody, d.UpdatePlan.Assign)
	addPlanImports(imports, d.CreatePlan, d.ReadPlan, d.UpdatePlan, d.DeletePlan)
	d.CRUDImports = imports.render()
	return nil
}

// addSDKImports adds the SDK root and models imports to a set when any of
// the rendered snippets references their package qualifiers.
func (e *serviceRenderer) addSDKImports(s *importSet, snippets ...string) {
	joined := strings.Join(snippets, "\n")
	if strings.Contains(joined, "models.") {
		s.add("", e.bindings.SDK.ModelsImportPath)
	}
	if strings.Contains(joined, "sdk.") {
		s.add("sdk", e.bindings.SDK.ImportPath)
	}
	// A generator puts the model of an inline request body in the package of
	// the operation that takes it, so a rendered expression can name a
	// package that is neither the root nor models. Prune recorded where each
	// one resolved; a snippet that names none adds none.
	for _, name := range sortedKeys(e.bindings.OperationPackages) {
		if strings.Contains(joined, name+".") {
			s.add("", e.bindings.OperationPackages[name])
		}
	}
}

// sortedKeys answers a map's keys in a fixed order, so an import set is
// built the same way on every run.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// importAttr is the attribute ImportState passes the import identifier
// through: the read call's single path parameter, when it maps to one.
func importAttr(r *ir.Resource, nodes []node) string {
	if r.Operations.Read == nil || len(r.Operations.Read.PathParameters) != 1 {
		return ""
	}
	p := r.Operations.Read.PathParameters[0]
	for _, n := range nodes {
		if n.attr.WireName == p.Name || n.attr.Name == ir.TerraformName(p.Name) {
			return n.attr.Name
		}
	}
	for _, n := range nodes {
		if n.attr.Name == "id" {
			return "id"
		}
	}
	return ""
}

// resourceMocks builds the stateful responder context from the operations
// and the fixture derivation.
func (e *serviceRenderer) resourceMocks(d *resourceData, r *ir.Resource, rb *sdkbind.ResourceBinding, spec fixtures.Fixture) error {
	d.RegistryName = r.Names.TerraformType
	d.CollectionURL = mockURL(r.Operations.Create.PathTemplate)
	d.ItemPattern = mockPattern(r.Operations.Read.PathTemplate)
	d.IDSegmentIndex = paramSegmentIndex(r.Operations.Read.PathTemplate)
	if d.IDSegmentIndex < 0 {
		return unrenderable("the read path %s declares no parameter segment for the mock to key on", r.Operations.Read.PathTemplate)
	}
	d.IDWire = idWire(rb.Fields)
	d.ResponseMinimal = string(spec.WireJSON(fixtures.ResponseMinimal))
	d.ResponseMaximal = string(spec.WireJSON(fixtures.ResponseMaximal))
	d.CreateStatus = successStatus(r.Operations.Create, 201)
	d.DeleteStatus = successStatus(r.Operations.Delete, 204)
	d.HasDelete = true
	if d.HasUpdate {
		d.UpdateMethod = r.Operations.Update.Method
		d.UpdateStatus = successStatus(r.Operations.Update, 200)
	}
	if r.Operations.List != nil {
		d.HasList = true
		d.ListPayload = listPayloadExpr(r.ListEnvelopeKey, "items")
	}

	imports := newImportSet(e.pc.Module)
	imports.add("", "encoding/json")
	imports.add("", "net/http")
	imports.add("", "strings")
	imports.add("", "sync")
	if d.HasList {
		imports.add("", "sort")
	}
	imports.add("", "github.com/jarcoal/httpmock")
	imports.add("providermocks", e.pc.Module+"/internal/mocks")
	d.MocksImports = imports.render()
	return nil
}

// successStatus is an operation's declared success code, or the
// conventional one.
func successStatus(op *ir.Operation, fallback int) int {
	if op != nil && op.SuccessCode > 0 {
		return op.SuccessCode
	}
	return fallback
}

// unitEndpoint mirrors the provider core's mocks.UnitEndpoint; the mock
// URLs are finished strings, so the spelling lives here too.
const unitEndpoint = "https://unit.invalid"

// mockURL is the literal URL one collection operation answers on.
func mockURL(pathTemplate string) string {
	return unitEndpoint + pathTemplate
}

// mockPattern is the httpmock regex matching one item operation's URL,
// each path parameter matching one segment.
func mockPattern(pathTemplate string) string {
	segments := strings.Split(strings.Trim(pathTemplate, "/"), "/")
	out := make([]string, len(segments))
	for i, seg := range segments {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			out[i] = "([^/]+)"
			continue
		}
		out[i] = regexp.QuoteMeta(seg)
	}
	return "=~^" + regexp.QuoteMeta(unitEndpoint) + "/" + strings.Join(out, "/") + "$"
}

// paramSegmentIndex is the position of the first parameter segment in a
// path template, for id extraction from a request URL.
func paramSegmentIndex(pathTemplate string) int {
	for i, seg := range strings.Split(strings.Trim(pathTemplate, "/"), "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			return i
		}
	}
	return -1
}

// idWire is the wire name of the id attribute, "id" when none is bound.
func idWire(fbs []sdkbind.FieldBinding) string {
	for _, fb := range fbs {
		if fb.Attr == "id" {
			return fb.Wire
		}
	}
	return "id"
}

// resourceChecks builds the terraform test check lines from the fixture
// derivation: one attribute-set check for the id, one value check per
// top-level scalar.
func (e *serviceRenderer) resourceChecks(d *resourceData, spec fixtures.Fixture) {
	address := d.TerraformType + ".test"
	minimal := checkLines(address, spec, fixtures.ConfigMinimal)
	maximal := checkLines(address, spec, fixtures.ConfigMaximal)
	idCheck := ""
	for _, v := range spec.Entries {
		if v.Name == "id" {
			idCheck = fmt.Sprintf("\t\t\t\t\tresource.TestCheckResourceAttrSet(%q, %q),\n", address, "id")
			break
		}
	}
	d.MinimalChecks = idCheck + minimal
	d.MaximalChecks = idCheck + maximal

	// Test imports are shared between the unit and acceptance suites.
	testImports := newImportSet(e.pc.Module)
	testImports.add("", "context")
	testImports.add("_", "embed")
	testImports.add("", "regexp")
	testImports.add("", "testing")
	testImports.add("fwresource", "github.com/hashicorp/terraform-plugin-framework/resource")
	testImports.add("", "github.com/hashicorp/terraform-plugin-testing/helper/resource")
	testImports.add("", e.pc.Module+"/internal/mocks")
	testImports.add(d.Package, d.PackagePath)
	testImports.add("_", d.PackagePath+"/mocks")
	d.TestImports = testImports.render()

	accImports := newImportSet(e.pc.Module)
	accImports.add("_", "embed")
	accImports.add("", "testing")
	accImports.add("", "github.com/hashicorp/terraform-plugin-testing/helper/resource")
	accImports.add("", e.pc.Module+"/internal/acceptance")
	d.AccImports = accImports.render()
}

// checkLines renders value checks for the audience's top-level scalars.
func checkLines(address string, spec fixtures.Fixture, a fixtures.Form) string {
	var b strings.Builder
	for _, v := range spec.Entries {
		if !valueWanted(v, a) || v.Nested != nil || v.Kind == ir.TypeList {
			continue
		}
		fmt.Fprintf(&b, "\t\t\t\t\tresource.TestCheckResourceAttr(%q, %q, %s),\n",
			address, v.Name, strconv.Quote(checkValue(v.Scalar)))
	}
	return b.String()
}

// valueWanted mirrors the fixture audience selection for check building.
func valueWanted(v fixtures.Entry, a fixtures.Form) bool {
	switch a {
	case fixtures.ConfigMinimal:
		return v.ComputedOptionalRequired == ir.Required
	case fixtures.ConfigMaximal:
		return v.ComputedOptionalRequired != ir.Computed
	default:
		return true
	}
}

// checkValue renders a fixture scalar the way terraform state prints it.
func checkValue(scalar any) string {
	switch v := scalar.(type) {
	case bool:
		return strconv.FormatBool(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}
