package render

import (
	"fmt"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/naming"
)

// ActionAccTestView is what the action acceptance test template needs.
type ActionAccTestView struct {
	Header  string
	Package string
	Imports string

	TestName string
	// ActionType is the Terraform action type, e.g. "thousandeyes_disable_endpoint_agent".
	ActionType string

	// EnvArgs fill config variables from the environment at run time. The generated test
	// skips, naming the variable, when one is unset: an action whose subject Terraform
	// cannot create must not fail the run for a missing fixture.
	EnvArgs []ActionEnvArg

	// HasCleanup, CleanupAssign and CleanupCall reverse the action after the test --
	// re-enabling what was disabled -- directly against the SDK, rebased onto the
	// acceptance harness's client. Without a declared cleanup the test still runs, and
	// the fixture's comment says the action is not reversed.
	HasCleanup    bool
	CleanupAssign string
	CleanupCall   string
}

// ActionEnvArg is one config variable filled from one environment variable.
type ActionEnvArg struct {
	// Attr is the Terraform attribute and configuration variable name.
	Attr string
	// EnvVar is the environment variable the test reads.
	EnvVar string
	// GoVar is the Go identifier holding the value in the generated test.
	GoVar string
}

// ActionFixtureView drives testdata/action.tf: variables, the action block, and the
// terraform_data trigger that invokes it.
type ActionFixtureView struct {
	Header string
	// ActionType and Label name the action block.
	ActionType string
	Label      string
	// Args are the config block's arguments, each referencing a variable.
	Args []fixtureValue
	// Vars are the variable names declared at the top.
	Vars []string
	// Reversed is false when no cleanup is declared, and the fixture says so.
	Reversed bool
}

// ActionAccTest builds the acceptance test view for one action.
//
// The assertion is the apply itself: an invoke error fails the step, and that exercises
// the whole generated path -- config decode, argument passing, timeout, error mapping.
// A postcondition against the API is deliberately not generated; what "the action worked"
// looks like is knowledge no blueprint field carries.
func ActionAccTest(
	bp blueprint.Blueprint,
	a blueprint.Action,
	opts Options,
) (ActionAccTestView, ActionFixtureView, error) {
	if a.AccTest == nil {
		return ActionAccTestView{}, ActionFixtureView{}, &ErrUnsupported{
			What: fmt.Sprintf("acceptance test for action %q", a.Key),
			Why: "no accTest is declared; an action's subject frequently cannot be created " +
				"by Terraform, so where its identifier comes from is judgement the " +
				"blueprint must state",
		}
	}

	actionType := bp.Provider.TerraformType(a.Name)

	v := ActionAccTestView{
		Header:     GeneratedHeader(opts.BlueprintPath, opts.BlueprintSHA256),
		Package:    a.GoPackage + "_test",
		TestName:   naming.AccTestName("Action", trimActionSuffix(a.GoTypeName), 1, "Invoke"),
		ActionType: actionType,
	}

	byField := map[string]string{}

	for _, env := range a.AccTest.EnvArgs {
		attr, ok := actionAttr(a, env.Attr)
		if !ok {
			return ActionAccTestView{}, ActionFixtureView{}, &ErrUnsupported{
				What: fmt.Sprintf("acceptance test for action %q", a.Key),
				Why:  fmt.Sprintf("envArgs names attribute %q, which the action does not declare", env.Attr),
			}
		}

		goVar := lowerCamel(attr.GoField)
		byField[attr.GoField] = goVar
		v.EnvArgs = append(v.EnvArgs, ActionEnvArg{
			Attr: env.Attr, EnvVar: env.EnvVar, GoVar: goVar,
		})
	}

	if cleanup := a.AccTest.Cleanup; cleanup != nil {
		assign, call, err := cleanupCall(a, cleanup, byField)
		if err != nil {
			return ActionAccTestView{}, ActionFixtureView{}, err
		}
		v.HasCleanup = true
		v.CleanupAssign, v.CleanupCall = assign, call
	}

	fixture := ActionFixtureView{
		Header:     GeneratedHeaderHCL(opts.BlueprintPath, opts.BlueprintSHA256),
		ActionType: actionType,
		Label:      fixtureLabel,
		Reversed:   v.HasCleanup,
	}
	for _, env := range v.EnvArgs {
		fixture.Vars = append(fixture.Vars, env.Attr)
		fixture.Args = append(fixture.Args, fixtureValue{
			Name: env.Attr, HCL: "var." + env.Attr,
		})
	}
	alignNames(fixture.Args)

	imports := newImportSet()
	imports.add(pkgTesting, "")
	imports.add("os", "")
	imports.add("path/filepath", "")
	imports.add(pkgContext, "")
	imports.add(pkgTFTestResource, "")
	// Aliased: the template declares its own config() fixture reader, and the
	// plugin-testing package of the same name must not collide with it.
	imports.add("github.com/hashicorp/terraform-plugin-testing/config", "tfconfig")
	imports.add("github.com/hashicorp/terraform-plugin-testing/tfversion", "")

	org := bp.Provider.GoModule
	imports.add(org+"/"+accSubdir, "")
	if v.HasCleanup {
		imports.add(pkgTime, "")
		imports.add(org+"/"+accSubdir+"/exists", "")
	}

	v.Imports = imports.render(org)

	return v, fixture, nil
}

// cleanupCall renders the declared cleanup operation against the acceptance harness's
// client, with configField arguments bound to the env-arg variables by Go field name.
func cleanupCall(
	a blueprint.Action,
	op *blueprint.Operation,
	byField map[string]string,
) (assign, call string, err error) {
	if op.Style != blueprint.CallStyleMethod {
		return "", "", &ErrUnsupported{
			What: fmt.Sprintf("cleanup for action %q", a.Key),
			Why:  fmt.Sprintf("call style %q is not implemented", op.Style),
		}
	}

	// The blueprint records the accessor off the action struct's own receiver --
	// "a.client.API.EndpointAgents" -- and the test hangs the same chain off the
	// harness's client. The receiver letter is the action template's, so only the
	// shape "<receiver>.client..." is rebased; anything else is refused rather than
	// rewritten by guesswork.
	receiver, rest, found := strings.Cut(a.Binding.Service.Accessor, ".")
	if !found || receiver == "" || (rest != "client" && !strings.HasPrefix(rest, "client.")) {
		return "", "", &ErrUnsupported{
			What: fmt.Sprintf("cleanup for action %q", a.Key),
			Why: fmt.Sprintf("accessor %q is not shaped <receiver>.client..., so it cannot "+
				"be rebased onto the test client", a.Binding.Service.Accessor),
		}
	}
	accessor := rest

	args := make([]string, 0, len(op.Args))
	for i, arg := range op.Args {
		switch arg.Kind {
		case blueprint.ArgContext:
			args = append(args, "ctx")
		case blueprint.ArgConfigField:
			goVar, ok := byField[arg.Field]
			if !ok {
				return "", "", &ErrUnsupported{
					What: fmt.Sprintf("cleanup for action %q", a.Key),
					Why: fmt.Sprintf("args[%d] reads config field %q, which no envArg fills",
						i, arg.Field),
				}
			}
			args = append(args, goVar)
		case blueprint.ArgLiteral:
			args = append(args, arg.Expr)
		default:
			return "", "", &ErrUnsupported{
				What: fmt.Sprintf("cleanup for action %q", a.Key),
				Why:  fmt.Sprintf("args[%d] has kind %q, which a test cannot supply", i, arg.Kind),
			}
		}
	}

	// Plain assignment, not a declaration: the generated cleanup already holds an err
	// from acquiring the client, and := would shadow-refuse to compile.
	assign = strings.TrimSuffix(discardingAssign(op.Return), ":=") + "="

	return assign,
		fmt.Sprintf("%s.%s(%s)", accessor, op.Method, strings.Join(args, ", ")), nil
}

// actionAttr finds a declared attribute by name.
func actionAttr(a blueprint.Action, name string) (blueprint.Attribute, bool) {
	for _, attr := range a.Schema.Attributes {
		if !attr.Drop && attr.Name == name {
			return attr, true
		}
	}
	return blueprint.Attribute{}, false
}

// lowerCamel lowers a Go field's first rune run for a local variable: AgentID -> agentID.
func lowerCamel(field string) string {
	if field == "" {
		return field
	}
	// Lower the leading upper-case run except its last letter when followed by more
	// word: "AgentID" -> "agentID", "ID" -> "id", "URL" -> "url".
	i := 0
	for i < len(field) && field[i] >= 'A' && field[i] <= 'Z' {
		i++
	}
	switch {
	case i == 0:
		return field
	case i == len(field):
		return strings.ToLower(field)
	case i == 1:
		return strings.ToLower(field[:1]) + field[1:]
	default:
		return strings.ToLower(field[:i-1]) + field[i-1:]
	}
}

// trimActionSuffix strips the conventional type suffix for a test name.
func trimActionSuffix(goTypeName string) string {
	if trimmed, ok := strings.CutSuffix(goTypeName, "Action"); ok && trimmed != "" {
		return trimmed
	}
	return goTypeName
}
