package blueprint

import (
	"strings"
	"testing"
)

// validAction returns an action that passes validation, and lets a test deviate from it in
// exactly one way.
func validAction(mutate func(*Action)) Blueprint {
	b := validBlueprint()

	a := Action{
		Key:            "disable_agent",
		Name:           "disable_agent",
		GoPackage:      "disable_agent",
		GoPackageAlias: "v7DisableAgent",
		GoTypeName:     "DisableAgentAction",
		ModelTypeName:  "DisableAgentConfig",
		Schema: Schema{
			Attributes: []Attribute{{
				Name:                     "agent_id",
				GoField:                  "AgentID",
				Type:                     AttrType{Kind: KindString},
				ComputedOptionalRequired: Required,
				Wire:                     WireBinding{JSONPath: "agentId", SDKField: "AgentID"},
			}},
		},
		Binding: ActionBinding{
			Service: ServiceRef{ImportPath: "p", TypeName: "Agents", Accessor: "a.client.Agents"},
			Invoke: &Operation{
				Style: CallStyleMethod, Method: "DisableAgent",
				Return: ReturnResultTransportError, ResultType: "agents.Agent",
			},
		},
	}
	if mutate != nil {
		mutate(&a)
	}

	b.Actions = []Action{a}

	return b
}

// TestUnit_Blueprint_AnActionNeitherExpandsNorFlattens is the correction the first real
// action forced.
//
// BlockKind.Expands returned true for an action, on the reasoning that an action sends values
// to the API. It does -- but as *call arguments*, and the emitter generates no construct
// function for an action, so there is no body for an expand to build. And nothing flattens:
// InvokeResponse has no field to carry a result, so a flatten conversion would convert into
// somewhere that does not exist.
func TestUnit_Blueprint_AnActionNeitherExpandsNorFlattens(t *testing.T) {
	t.Parallel()

	if BlockAction.Expands() {
		t.Error("an action sends arguments, not a request body, so it does not expand")
	}
	if BlockAction.Flattens() {
		t.Error("an action writes nothing back, so it does not flatten")
	}

	// The kinds that do, still do.
	if !BlockResource.Expands() {
		t.Error("a resource expands")
	}
	for _, k := range []BlockKind{BlockResource, BlockDataSource, BlockEphemeral, BlockList} {
		if !k.Flattens() {
			t.Errorf("%s reads values back, so it flattens", k)
		}
	}

	// An action attribute with neither conversion is valid, which is the whole point: the
	// value reaches the API as a call argument.
	if err := validAction(nil).Validate(); err != nil {
		t.Errorf("an action attribute needs neither expand nor flatten: %v", err)
	}

	// And a flatten on one is refused rather than silently ignored.
	b := validAction(func(a *Action) {
		a.Schema.Attributes[0].Wire.Flatten = &ConvertCall{Func: "convert.PtrStringToFramework"}
	})
	err := b.Validate()
	if err == nil {
		t.Fatal("a flatten on an action attribute must be refused")
	}
	if !strings.Contains(err.Error(), "reads nothing back") {
		t.Errorf("the error should say why: %v", err)
	}
}

// TestUnit_Blueprint_ActionRefusesWhatItsSchemaCannotHold.
func TestUnit_Blueprint_ActionRefusesWhatItsSchemaCannotHold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutate   func(*Action)
		wantPath string
		wantMsg  string
	}{
		{
			name:     "no attributes",
			mutate:   func(a *Action) { a.Schema.Attributes = nil },
			wantPath: "schema.attributes",
			wantMsg:  "nothing to act on",
		},
		{
			name:     "no invoke",
			mutate:   func(a *Action) { a.Binding.Invoke = nil },
			wantPath: "binding.invoke",
			wantMsg:  "calls nothing does nothing",
		},
		{
			// There is no state for a computed value to live in.
			name: "a computed attribute",
			mutate: func(a *Action) {
				a.Schema.Attributes[0].ComputedOptionalRequired = Computed
			},
			wantPath: "computedOptionalRequired",
			wantMsg:  "required and optional",
		},
		{
			// And no stored value to mark sensitive.
			name:     "a sensitive attribute",
			mutate:   func(a *Action) { a.Schema.Attributes[0].Sensitive = true },
			wantPath: "sensitive",
			wantMsg:  "cannot be sensitive",
		},
		{
			name:     "a plan modifier",
			mutate:   func(a *Action) { a.Schema.Attributes[0].PlanModifiers = []CustomCode{{}} },
			wantPath: "planModifiers",
			wantMsg:  "no plan to modify",
		},
		{
			name:     "no service accessor",
			mutate:   func(a *Action) { a.Binding.Service.Accessor = "" },
			wantPath: "binding.service.accessor",
			wantMsg:  "required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validAction(tc.mutate).Validate()
			if err == nil {
				t.Fatal("expected the action to be refused")
			}
			if !strings.Contains(err.Error(), tc.wantPath) {
				t.Errorf("error should name %q: %v", tc.wantPath, err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error should explain %q: %v", tc.wantMsg, err)
			}
		})
	}
}

// TestUnit_Blueprint_ActionsShareTheAliasNamespace.
//
// Every block's Go package alias appears in a registration file, and all four registration
// files are in one package -- so an alias colliding across kinds would not compile.
func TestUnit_Blueprint_ActionsShareTheAliasNamespace(t *testing.T) {
	t.Parallel()

	b := validAction(func(a *Action) {
		// The alias the pilot-shaped resource already uses.
		a.GoPackageAlias = "v7Tag"
	})

	err := b.Validate()
	if err == nil {
		t.Fatal("an action reusing a resource's import alias must be refused")
	}
	if !strings.Contains(err.Error(), "import alias") {
		t.Errorf("the error should name the collision: %v", err)
	}
}
