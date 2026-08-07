package sdkbind

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

const kiotaModels = "example.com/kiotasdk/models"

// nestedTagDetail is a drafted nested object whose every field is spelled the
// way inference derives it from a document: plain strings, plain string sets,
// and the accessor names a reserved-word list predicts. The SDK disagrees with
// all of them, one way or another.
func nestedTagDetail() *blueprint.NestedAttributeObject {
	str := func(name, sdkField string) blueprint.Attribute {
		return blueprint.Attribute{
			Name: name, GoField: "X", Type: blueprint.AttrType{Kind: blueprint.KindString},
			Wire: blueprint.WireBinding{
				SDKField: sdkField, SDKGoType: "*string",
				Flatten: &blueprint.ConvertCall{Func: "convert.PtrStringToFramework"},
				Expand:  &blueprint.ConvertCall{Func: "convert.FrameworkToPtrString"},
			},
		}
	}

	labels := blueprint.Attribute{
		Name: "labels", GoField: "Labels",
		Type: blueprint.AttrType{
			Kind:        blueprint.KindSet,
			ElementType: &blueprint.AttrType{Kind: blueprint.KindString},
		},
		Wire: blueprint.WireBinding{
			SDKField: "Labels", SDKGoType: "[]string",
			Flatten: &blueprint.ConvertCall{Func: "convert.StringSliceToFrameworkSet"},
			Expand:  &blueprint.ConvertCall{Func: "convert.FrameworkSetToStringSlice"},
		},
	}

	return &blueprint.NestedAttributeObject{
		GoTypeName:      "TagDetailModel",
		SDKType:         "models.TagDetailable",
		ConstructorExpr: "models.NewTagDetail()",
		Attributes: []blueprint.Attribute{
			str("kind", "Kind"),
			labels,
			str("vendor", "Vendor"),
			str("make", "MakeEscaped"),
			str("ghost", "Ghost"),
		},
	}
}

func nestedAttr(t *testing.T, n *blueprint.NestedAttributeObject, name string) blueprint.Attribute {
	t.Helper()
	for _, a := range n.Attributes {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("attribute %q did not survive reconciliation; survivors: %v", name, nestedNames(n))
	return blueprint.Attribute{}
}

func nestedNames(n *blueprint.NestedAttributeObject) []string {
	out := make([]string, 0, len(n.Attributes))
	for _, a := range n.Attributes {
		out = append(out, a.Name)
	}
	return out
}

// TestUnit_SDKBind_NestedFieldsAreReconciledAgainstTheirOwnModel.
//
// The top-level loop holds an attribute against a field on the body model, and
// a nested attribute's field lives on the model its parent resolves to. Nothing
// visited those, so an enumeration the document declared inline two levels down
// kept the string spelling inference implied for it and the generated flatten
// did not compile.
func TestUnit_SDKBind_NestedFieldsAreReconciledAgainstTheirOwnModel(t *testing.T) {
	t.Parallel()

	n := nestedTagDetail()

	removals, emptied := reconcileNested(
		kiotaLoader, blueprint.AccessMethod, kiotaModels, "resource", "tag", "detail", n)

	if emptied {
		t.Fatal("four of five fields are resolvable, so the object is not emptied")
	}

	// An inline enumeration: read through its Stringer, written through Parse.
	kind := nestedAttr(t, n, "kind")
	if kind.Wire.SDKGoType != "*models.TagDetail_kind" {
		t.Errorf("kind.sdkGoType = %q", kind.Wire.SDKGoType)
	}
	if kind.Wire.Flatten.Func != "convert.PtrStringerToFramework" {
		t.Errorf("kind.flatten = %q", kind.Wire.Flatten.Func)
	}
	if kind.Wire.Expand.Func != "convert.FrameworkToKiotaEnum" ||
		len(kind.Wire.Expand.TypeArgs) != 1 || kind.Wire.Expand.TypeArgs[0] != "models.TagDetail_kind" {
		t.Errorf("kind.expand = %+v", kind.Wire.Expand)
	}

	// A slice of that enumeration: the plain string-slice conversion does not
	// compile against it.
	labels := nestedAttr(t, n, "labels")
	if labels.Wire.SDKGoType != "[]models.TagDetail_kind" {
		t.Errorf("labels.sdkGoType = %q", labels.Wire.SDKGoType)
	}
	if labels.Wire.Flatten.Func != "convert.KiotaEnumSliceToFrameworkSet" {
		t.Errorf("labels.flatten = %q", labels.Wire.Flatten.Func)
	}
	if labels.Wire.Expand.Func != "convert.FrameworkSetToKiotaEnumSlice" {
		t.Errorf("labels.expand = %q", labels.Wire.Expand.Func)
	}

	// A field the model does not carry at all costs that field and nothing more.
	if len(removals) != 1 {
		t.Fatalf("want exactly one removal, got %d: %v", len(removals), removals)
	}
	if removals[0].Attribute != "detail.ghost" {
		t.Errorf("the removal must name the field by its path: %q", removals[0].Attribute)
	}
	if !strings.Contains(removals[0].Reason, "Ghost") {
		t.Errorf("the reason must name the accessor the SDK lacks: %q", removals[0].Reason)
	}
	for _, a := range n.Attributes {
		if a.Name == "ghost" {
			t.Error("ghost was reported as removed but is still in the object")
		}
	}
}

// TestUnit_SDKBind_AccessorManglingIsRepairedInBothDirections.
//
// Inference guesses kiota's reserved-word list from the document alone, and gets
// it wrong both ways: kiota escapes "vendor", which Go reserves as a directory
// name and no word list of identifiers predicts, and does not escape "make",
// which is a predeclared function inference does list. Both spellings are
// repaired off the SDK, because the SDK is the release actually in use.
func TestUnit_SDKBind_AccessorManglingIsRepairedInBothDirections(t *testing.T) {
	t.Parallel()

	n := nestedTagDetail()

	if _, emptied := reconcileNested(
		kiotaLoader, blueprint.AccessMethod, kiotaModels, "resource", "tag", "detail", n); emptied {
		t.Fatal("the object is not emptied")
	}

	if got := nestedAttr(t, n, "vendor").Wire.SDKField; got != "VendorEscaped" {
		t.Errorf("vendor.sdkField = %q, want the escaped spelling the SDK carries", got)
	}
	if got := nestedAttr(t, n, "make").Wire.SDKField; got != "Make" {
		t.Errorf("make.sdkField = %q, want the plain spelling the SDK carries", got)
	}
}

// TestUnit_SDKBind_ANestedObjectWithNothingLeftIsReportedEmptied: the level
// above owns removing the attribute, because the object itself has no say in
// whether its parent survives.
func TestUnit_SDKBind_ANestedObjectWithNothingLeftIsReportedEmptied(t *testing.T) {
	t.Parallel()

	n := nestedTagDetail()
	n.Attributes = n.Attributes[len(n.Attributes)-1:] // ghost alone

	removals, emptied := reconcileNested(
		kiotaLoader, blueprint.AccessMethod, kiotaModels, "resource", "tag", "detail", n)

	if !emptied {
		t.Error("an object whose only field went is emptied")
	}
	if len(removals) != 1 || len(n.Attributes) != 0 {
		t.Errorf("removals = %v, survivors = %v", removals, nestedNames(n))
	}
}

// TestUnit_SDKBind_FromCreateMustAgreeWithTheIdentifierAttribute.
//
// The create result is stored through the identifier attribute's own flatten,
// because that is what the emitter renders. Jamf's Venafi CA is where the two
// part company: the POST answers with an href object whose id is a string,
// while the record's own id is an integer, and the converter drafted for one
// does not compile against the other.
func TestUnit_SDKBind_FromCreateMustAgreeWithTheIdentifierAttribute(t *testing.T) {
	t.Parallel()

	resource := func(idSDKType, createResult string) blueprint.Resource {
		return blueprint.Resource{
			Key: "tag",
			Binding: blueprint.ResourceBinding{
				Service: blueprint.ServiceRef{ImportPath: kiotaModels, Alias: "models"},
				Create: &blueprint.Operation{
					Style: blueprint.CallStyleFluent, ResultType: createResult,
					Chain:  []blueprint.ChainSegment{{Method: "Post"}},
					Return: blueprint.ReturnResultError,
				},
				ID: blueprint.IDBinding{
					Attribute: "id", GoField: "ID", FromCreate: "created.GetId()",
				},
			},
			Schema: blueprint.Schema{Attributes: []blueprint.Attribute{{
				Name: "id", GoField: "ID",
				Type: blueprint.AttrType{Kind: blueprint.KindString},
				Wire: blueprint.WireBinding{SDKField: "Id", SDKGoType: idSDKType},
			}}},
		}
	}

	// The href's id is a *string and so is the attribute's: nothing to report.
	var agreeing Report
	verifyFromCreate(kiotaLoader, resource("*string", "models.HrefResponseable"), &agreeing)
	if len(agreeing.Problems) != 0 {
		t.Errorf("an agreeing pair must pass: %v", agreeing.Problems)
	}

	var diverging Report
	verifyFromCreate(kiotaLoader, resource("*int32", "models.HrefResponseable"), &diverging)
	if len(diverging.Problems) != 1 {
		t.Fatalf("want one problem, got %v", diverging.Problems)
	}
	detail := diverging.Problems[0].Detail
	if !strings.Contains(detail, "*string") || !strings.Contains(detail, "*int32") {
		t.Errorf("the problem must name both types: %q", detail)
	}
}

// TestUnit_SDKBind_DropUnbuildable: what survives reconciliation still has to be
// generatable. crud.go calls constructResource and mapRemoteStateToTerraform
// unconditionally, while the emitter skips those files when there is nothing to
// put in them -- so an entity with no live conversion one way is a package that
// does not compile, and a resource whose whole schema is its computed
// identifier is one no HCL can configure.
func TestUnit_SDKBind_DropUnbuildable(t *testing.T) {
	t.Parallel()

	both := blueprint.Attribute{
		Name: "name", GoField: "Name",
		Wire: blueprint.WireBinding{
			SDKField: "Name",
			Flatten:  &blueprint.ConvertCall{Func: "convert.PtrStringToFramework"},
			Expand:   &blueprint.ConvertCall{Func: "convert.FrameworkToPtrString"},
		},
	}
	// A server-assigned identifier: read back, never sent.
	idOnly := blueprint.Attribute{
		Name: "id", GoField: "ID",
		Wire: blueprint.WireBinding{
			SDKField: "Id", SkipExpand: true,
			Flatten: &blueprint.ConvertCall{Func: "convert.PtrStringToFramework"},
		},
	}

	bp := &blueprint.Blueprint{
		Resources: []blueprint.Resource{
			{Key: "keeps", Schema: blueprint.Schema{Attributes: []blueprint.Attribute{both, idOnly}}},
			{Key: "computed_only", Schema: blueprint.Schema{Attributes: []blueprint.Attribute{idOnly}}},
		},
		// A data source sends no body, so the identifier alone is enough for it.
		DataSources: []blueprint.DataSource{
			{Key: "lookup", Schema: blueprint.Schema{Attributes: []blueprint.Attribute{idOnly}}},
		},
	}

	removals := dropUnbuildable(bp)

	if len(removals) != 1 || removals[0].Key != "computed_only" {
		t.Fatalf("want the resource with nothing to send removed, got %v", removals)
	}
	if !strings.Contains(removals[0].Reason, "request body") {
		t.Errorf("the reason must name the missing direction: %q", removals[0].Reason)
	}
	if bp.Resources[0].Drop || bp.DataSources[0].Drop {
		t.Error("the buildable entities must survive")
	}
	if !bp.Resources[1].Drop {
		t.Error("the unbuildable resource must be dropped")
	}
}
