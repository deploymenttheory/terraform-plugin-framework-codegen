package generate

import (
	"fmt"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// The packages a generated test helper needs.
const (
	pkgTFTesting = "github.com/hashicorp/terraform-plugin-testing/terraform"
	accSubdir    = "internal/acceptance"
)

// TestHelperView is what the per-resource test helper template needs.
//
// Small on purpose. The helper answers one question -- is this object in the API -- and the
// answer is the resource's own read call. Everything else about existence checking lives in
// the hand-written acceptance packages, so there is nothing here to get subtly wrong per
// resource.
type TestHelperView struct {
	Header  string
	Package string
	Imports string

	// GoTypeName is the test helper type, e.g. "TagTestResource".
	GoTypeName string
	// Subject names the thing in prose, for the doc comment.
	Subject string
	// ClientType is the SDK client the read call hangs off, e.g. "*thousandeyes.Client".
	ClientType string

	// Assign is the left-hand side matching the read call's arity, e.g. "_, _, err :=".
	Assign string
	// Call is the read call with its receiver rebased onto the helper's client.
	Call string
}

// TestHelper builds the view for one resource's acceptance test helper.
func TestHelper(
	bp blueprint.Blueprint,
	r blueprint.Resource,
	opts Options,
) (TestHelperView, error) {
	if r.Binding.Read == nil {
		return TestHelperView{}, &ErrUnsupported{
			What: fmt.Sprintf("test helper for resource %q", r.Key),
			Why: "it has no read operation, so there is no way to ask the API whether the " +
				"object exists",
		}
	}

	op := *r.Binding.Read

	accessor, err := rebaseAccessor(r.Binding.Service.Accessor, r.Key)
	if err != nil {
		return TestHelperView{}, err
	}

	var call string
	switch op.Style {
	case blueprint.CallStyleMethod:
		args, err := helperArgs(r, op)
		if err != nil {
			return TestHelperView{}, err
		}
		call = fmt.Sprintf("%s.%s(%s)", accessor, op.Method, strings.Join(args, ", "))

	case blueprint.CallStyleFluent:
		call, err = chainCall(accessor, op, func(a blueprint.Argument) (string, error) {
			return helperArgExpr(r, a)
		})
		if err != nil {
			return TestHelperView{}, err
		}

	default:
		return TestHelperView{}, &ErrUnsupported{
			What: fmt.Sprintf("test helper for resource %q", r.Key),
			Why:  fmt.Sprintf("call style %q is not implemented", op.Style),
		}
	}

	imports := newImportSet()
	imports.add(pkgContext, "")
	imports.add(pkgTFTesting, "")
	imports.add(bp.Provider.SDK.ClientImport.Path, bp.Provider.SDK.ClientImport.Alias)
	imports.add(bp.Provider.GoModule+"/"+accSubdir+"/exists", "")

	// A literal argument's expression can name packages of its own -- an aliased
	// request option for an expansion-gated read is the live case, aliased because
	// the helper's own client variable shadows the SDK's client package name.
	for _, a := range op.Args {
		for _, imp := range a.Imports {
			imports.add(imp.Path, imp.Alias)
		}
	}
	for _, seg := range op.Chain {
		for _, a := range seg.Args {
			for _, imp := range a.Imports {
				imports.add(imp.Path, imp.Alias)
			}
		}
	}

	v := TestHelperView{
		Header:     GeneratedHeader(opts.BlueprintPath, opts.BlueprintSHA256),
		Package:    r.GoPackage,
		GoTypeName: testResourceTypeName(r.GoTypeName),
		Subject:    bp.Provider.TerraformType(r.Name),
		ClientType: bp.Provider.SDK.ClientType,
		Assign:     discardingAssign(op.Return),
		Call:       call,
		Imports:    imports.render(bp.Provider.GoModule),
	}

	return v, nil
}

// rebaseAccessor turns a resource-receiver accessor into one on the helper's client parameter.
//
// The blueprint records the accessor as the generated resource uses it -- "r.client.API.Tags",
// off the resource struct's own field. A test helper has no such struct: it is handed a client
// directly, so the same chain has to hang off that instead.
//
// Textual, and deliberately narrow: only the leading "r.client" is replaced, and an accessor
// not shaped that way is refused rather than rewritten by guesswork. A silent mis-rebase would
// produce code that compiles against the wrong value.
func rebaseAccessor(accessor, key string) (string, error) {
	const receiverPrefix = "r.client"

	switch {
	case accessor == receiverPrefix:
		return "client", nil
	case strings.HasPrefix(accessor, receiverPrefix+"."):
		return "client" + strings.TrimPrefix(accessor, receiverPrefix), nil
	default:
		return "", &ErrUnsupported{
			What: fmt.Sprintf("test helper for resource %q", key),
			Why: fmt.Sprintf(
				"its service accessor %q does not begin with %q, so it cannot be rebased onto "+
					"the helper's client without guessing",
				accessor, receiverPrefix,
			),
		}
	}
}

// helperArgs renders the read call's arguments for the helper's scope.
//
// The helper has a *terraform.InstanceState rather than a decoded model, so a state field is
// read out of the attribute map instead of off a framework value. Every other argument kind is
// refused: a read taking a request body or a plan field is not a read this can reproduce, and
// producing something that merely compiles would give a false answer about existence.
func helperArgs(r blueprint.Resource, op blueprint.Operation) ([]string, error) {
	out := make([]string, 0, len(op.Args))

	for _, a := range op.Args {
		expr, err := helperArgExpr(r, a)
		if err != nil {
			return nil, err
		}
		out = append(out, expr)
	}

	return out, nil
}

// helperArgExpr renders one argument in the helper's scope, which has a
// *terraform.InstanceState rather than a decoded model.
func helperArgExpr(r blueprint.Resource, a blueprint.Argument) (string, error) {
	switch a.Kind {
	case blueprint.ArgContext:
		return "ctx", nil

	case blueprint.ArgStateField:
		name, ok := attributeNameForField(r, a.Field)
		if !ok {
			return "", &ErrUnsupported{
				What: fmt.Sprintf("test helper for resource %q", r.Key),
				Why: fmt.Sprintf(
					"its read call takes model field %q, which no declared attribute maps to",
					a.Field,
				),
			}
		}

		// The identifier is state.ID rather than an attribute lookup, because
		// terraform.InstanceState exposes it directly and every resource has one.
		if name == r.Binding.ID.Attribute {
			return "state.ID", nil
		}

		return fmt.Sprintf("state.Attributes[%s]", goStringLit(name)), nil

	case blueprint.ArgLiteral:
		// A literal with an expression is a constant -- a request option like an
		// expansion query needs no source, only repeating. Without one there is
		// nothing to repeat, which stays a refusal below.
		if a.Expr != "" {
			return a.Expr, nil
		}

		fallthrough

	default:
		return "", &ErrUnsupported{
			What: fmt.Sprintf("test helper for resource %q", r.Key),
			Why: fmt.Sprintf(
				"its read call takes a %q argument, which an acceptance check has no "+
					"source for", a.Kind,
			),
		}
	}
}

// testResourceTypeName derives the helper type's name from the resource type's.
//
// The trailing "Resource" is dropped first, so a TagResource yields a TagTestResource rather than
// a TagResourceTestResource. Cosmetic, but this identifier is written by hand in every generated
// acceptance test that references it, and the reference provider spells it the same way.
//
// Trimmed rather than assumed: a resource type not ending in "Resource" keeps its whole name, so
// the result is always the type name plus a suffix and never a truncation.
func testResourceTypeName(goTypeName string) string {
	return strings.TrimSuffix(goTypeName, "Resource") + "TestResource"
}

// attributeNameForField maps a model field back to its Terraform attribute name.
func attributeNameForField(r blueprint.Resource, field string) (string, bool) {
	for _, a := range r.Schema.Attributes {
		if a.GoField == field && !a.Drop {
			return a.Name, true
		}
	}

	return "", false
}

// discardingAssign is the left-hand side that keeps only the error.
//
// The helper wants the error and nothing else -- whether the read succeeded is the whole
// question -- so every other return is discarded. Blank identifiers rather than named
// variables, because Go rejects a declared-and-unused one.
func discardingAssign(arity blueprint.ReturnArity) string {
	var blanks []string

	if arity.HasResult() {
		blanks = append(blanks, "_")
	}
	if arity.HasTransport() {
		blanks = append(blanks, "_")
	}

	blanks = append(blanks, "err")

	return strings.Join(blanks, ", ") + " :="
}
