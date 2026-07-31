package blueprint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validResource returns a minimal resource that passes validation. Tests mutate
// a copy of it, so each case states exactly one deviation from valid and the
// reason it fails is unambiguous.
func ptrBool(b bool) *bool { return &b }

func validResource() Resource {
	return Resource{
		Key:            "tag",
		Name:           "tag",
		GoPackage:      "tag",
		GoPackageAlias: "v7Tag",
		GoTypeName:     "TagResource",
		ModelTypeName:  "TagResourceModel",
		Schema: Schema{
			Attributes: []Attribute{
				{
					Name:                     "id",
					GoField:                  "ID",
					Type:                     AttrType{Kind: KindString},
					ComputedOptionalRequired: Computed,
					Wire: WireBinding{
						JSONPath:   "id",
						SDKField:   "ID",
						SDKGoType:  "*string",
						SkipExpand: true,
						Flatten:    &ConvertCall{Func: "convert.PtrStringToFramework"},
					},
				},
				{
					Name:                     "key",
					GoField:                  "Key",
					Type:                     AttrType{Kind: KindString},
					ComputedOptionalRequired: Required,
					Wire: WireBinding{
						JSONPath:  "key",
						SDKField:  "Key",
						SDKGoType: "*string",
						Expand:    &ConvertCall{Func: "convert.FrameworkToPtrString"},
						Flatten:   &ConvertCall{Func: "convert.PtrStringToFramework"},
					},
				},
			},
		},
		Binding: ResourceBinding{
			Service: ServiceRef{
				ImportPath: "example.com/sdk/tags",
				TypeName:   "Tags",
				Accessor:   "r.client.Tags",
			},
			Create: &Operation{
				Style: CallStyleMethod, Method: "CreateTag",
				Return: ReturnResultTransportError, ResultType: "tags.Tag",
			},
			Read: &Operation{
				Style: CallStyleMethod, Method: "GetTag",
				Return: ReturnResultTransportError, ResultType: "tags.Tag",
			},
			Update: &Operation{
				Style: CallStyleMethod, Method: "UpdateTag",
				Return: ReturnResultTransportError, ResultType: "tags.Tag",
			},
			Delete: &Operation{
				Style: CallStyleMethod, Method: "DeleteTag",
				Return: ReturnTransportError,
			},
			ID: IDBinding{
				Attribute: "id", GoField: "ID",
				FromCreate: "created.ID", FromCreateIsPointer: true,
			},
			Body: BodyModels{
				RequestType:     "tags.TagInfo",
				ResponseType:    "tags.Tag",
				ConstructorExpr: "&tags.TagInfo{}",
				AccessStyle:     AccessStructField,
			},
		},
		Policy: ResourcePolicy{UpdateStyle: UpdatePutFull},
		Import: ImportPolicy{Style: ImportPassthroughID, Attribute: "id"},
	}
}

func validEphemeral() Ephemeral {
	return Ephemeral{
		Key:            "credential",
		Name:           "credential",
		GoPackage:      "credential",
		GoPackageAlias: "v7Credential",
		GoTypeName:     "CredentialEphemeral",
		ModelTypeName:  "CredentialEphemeralModel",
		Schema: Schema{
			Attributes: []Attribute{
				{
					Name:                     "id",
					GoField:                  "ID",
					Type:                     AttrType{Kind: KindString},
					ComputedOptionalRequired: Required,
					Wire: WireBinding{
						JSONPath: "id", SDKField: "ID", SDKGoType: "*string",
						SkipFlatten: true,
					},
				},
				{
					Name:                     "value",
					GoField:                  "Value",
					Type:                     AttrType{Kind: KindString},
					ComputedOptionalRequired: Computed,
					Sensitive:                true,
					Wire: WireBinding{
						JSONPath: "value", SDKField: "Value", SDKGoType: "*string",
						Flatten: &ConvertCall{Func: "convert.PtrStringToFramework"},
					},
				},
			},
		},
		Binding: EphemeralBinding{
			Service: ServiceRef{
				ImportPath: "example.com/sdk/credentials",
				TypeName:   "Credentials",
				Accessor:   "e.client.Credentials",
			},
			Open: &Operation{
				Style: CallStyleMethod, Method: "GetCredential",
				Return: ReturnResultTransportError, ResultType: "credentials.Credential",
			},
			Response: ResponseModel{
				Type:        "credentials.Credential",
				AccessStyle: AccessStructField,
			},
		},
	}
}

// addListFacet gives the fixture resource an identity and a minimal valid list facet, so
// a test can break exactly one thing on top of them.
func addListFacet(r *Resource) {
	r.Identity = &ResourceIdentity{
		GoTypeName: "TagResourceIdentity",
		Attributes: []IdentityAttribute{{
			Name: "id", GoField: "ID", Kind: KindString,
			RequiredForImport: true, FromAttribute: "id",
		}},
	}
	r.List = &ListFacet{
		GoTypeName: "TagListResource",
		Service:    ServiceRef{ImportPath: "p", TypeName: "Tags", Accessor: "l.client.Tags"},
		Read: &Operation{
			Style: CallStyleMethod, Method: "GetTags",
			Return: ReturnResultTransportError, ResultType: "tags.ResourceTags",
		},
		Response:    ResponseModel{Type: "tags.ResourceTags", AccessStyle: AccessStructField},
		ElementType: "tags.Tag",
		IdentityFrom: []ListIdentityMapping{
			{GoField: "ID", FromSDKField: "ID", IsPointer: true},
		},
	}
}

func validDataSourceWithSeed(seedKey string) DataSource {
	return DataSource{
		Key: "tag", Name: "tag", GoPackage: "tag", GoPackageAlias: "dsV7Tag",
		GoTypeName: "TagDataSource", ModelTypeName: "TagDataSourceModel",
		Schema: Schema{Attributes: []Attribute{{
			Name: "id", GoField: "ID", Type: AttrType{Kind: KindString},
			ComputedOptionalRequired: Required,
			Wire: WireBinding{
				JSONPath: "id", SDKField: "ID", SDKGoType: "*string",
				Flatten: &ConvertCall{Func: "convert.PtrStringToFramework"},
			},
		}}},
		Binding: DataSourceBinding{
			Service: ServiceRef{
				ImportPath: "example.com/sdk/tags", TypeName: "Tags",
				Accessor: "d.client.Tags",
			},
			Read: &Operation{
				Style: CallStyleMethod, Method: "GetTag",
				Return: ReturnResultTransportError, ResultType: "tags.Tag",
			},
			Response: ResponseModel{Type: "tags.Tag", AccessStyle: AccessStructField},
		},
		AccTest: &AccSeed{
			SeedResourceKey: seedKey,
			Args:            []SeedArg{{Attr: "id", FromSeedAttr: "id"}},
		},
	}
}

func validBlueprint() Blueprint {
	return Blueprint{
		FormatVersion: FormatVersion,
		Provider: Provider{
			Name:       "thousandeyes",
			GoModule:   "example.com/terraform-provider-thousandeyes",
			TypePrefix: "thousandeyes",
			SDK: SDKModule{
				Dialect:    DialectRestyService,
				ModulePath: "example.com/sdk",
				ClientType: "*thousandeyes.Client",
			},
		},
		Resources: []Resource{validResource()},
	}
}

func TestUnit_Blueprint_Validate_AcceptsAValidDocument(t *testing.T) {
	t.Parallel()

	if err := validBlueprint().Validate(); err != nil {
		t.Fatalf("the fixture must be valid, otherwise every other test here is meaningless: %v", err)
	}
}

func TestUnit_Blueprint_Validate_RejectsStructuralProblems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// mutate breaks the valid fixture in exactly one way.
		mutate func(*Blueprint)
		// wantPath is a fragment of the expected error, which asserts the message
		// names the offending node rather than merely failing.
		wantPath string
	}{
		{
			name:     "wrong format version",
			mutate:   func(b *Blueprint) { b.FormatVersion = "99" },
			wantPath: "formatVersion",
		},
		{
			name:     "missing provider name",
			mutate:   func(b *Blueprint) { b.Provider.Name = "" },
			wantPath: "provider.name",
		},
		{
			name:     "unknown SDK dialect",
			mutate:   func(b *Blueprint) { b.Provider.SDK.Dialect = "carrierPigeon" },
			wantPath: "provider.sdk.dialect",
		},
		{
			// Reserved-but-unimplemented must fail loudly rather than emit
			// silently wrong code.
			name:     "kiota dialect is rejected as unimplemented",
			mutate:   func(b *Blueprint) { b.Provider.SDK.Dialect = DialectKiotaFluent },
			wantPath: "not yet implemented",
		},
		{
			name:     "resource with no attributes",
			mutate:   func(b *Blueprint) { b.Resources[0].Schema.Attributes = nil },
			wantPath: "attributes",
		},
		{
			name: "duplicate attribute name",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].Name = "id"
			},
			wantPath: "attribute name",
		},
		{
			// The subtler duplicate: the schema is fine and the model silently
			// loses an attribute.
			name: "duplicate model field",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].GoField = "ID"
			},
			wantPath: "model field",
		},
		{
			name: "duplicate name across resources",
			mutate: func(b *Blueprint) {
				second := validResource()
				second.Key = "tag2"
				second.GoPackageAlias = "v7Tag2"
				b.Resources = append(b.Resources, second)
			},
			wantPath: "name",
		},
		{
			name: "duplicate import alias across resources",
			mutate: func(b *Blueprint) {
				second := validResource()
				second.Key = "tag2"
				second.Name = "tag2"
				b.Resources = append(b.Resources, second)
			},
			wantPath: "import alias",
		},
		{
			name: "collection kind without an element type",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].Type = AttrType{Kind: KindSet}
			},
			wantPath: "elem",
		},
		{
			name: "scalar kind with an element type",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].Type.ElementType = &AttrType{Kind: KindString}
			},
			wantPath: "elem",
		},
		{
			name: "unknown type kind",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].Type = AttrType{Kind: "octopus"}
			},
			wantPath: "kind",
		},
		{
			name: "ID binding naming an attribute that does not exist",
			mutate: func(b *Blueprint) {
				b.Resources[0].Binding.ID.Attribute = "nope"
			},
			wantPath: "which the resource does not declare",
		},
		{
			name: "ID binding naming a model field that does not exist",
			mutate: func(b *Blueprint) {
				b.Resources[0].Binding.ID.GoField = "Nope"
			},
			wantPath: "which the resource does not declare",
		},
		{
			name: "import naming an attribute that does not exist",
			mutate: func(b *Blueprint) {
				b.Resources[0].Import.Attribute = "nope"
			},
			wantPath: "which the resource does not declare",
		},
		{
			name: "no read operation",
			mutate: func(b *Blueprint) {
				b.Resources[0].Binding.Read = nil
			},
			wantPath: "cannot refresh state",
		},
		{
			// The dangerous gap: PUT clears omitted fields and PATCH preserves
			// them, so an unstated style would silently erase attributes.
			name: "update operation with no declared update style",
			mutate: func(b *Blueprint) {
				b.Resources[0].Policy.UpdateStyle = ""
			},
			wantPath: "policy.updateStyle",
		},
		{
			name: "update style declared but no update operation",
			mutate: func(b *Blueprint) {
				b.Resources[0].Binding.Update = nil
			},
			wantPath: "policy.updateStyle",
		},
		{
			name: "result type set on a call that returns no result",
			mutate: func(b *Blueprint) {
				b.Resources[0].Binding.Delete.ResultType = "tags.Tag"
			},
			wantPath: "yields no result",
		},
		{
			name: "result type missing on a call that returns one",
			mutate: func(b *Blueprint) {
				b.Resources[0].Binding.Read.ResultType = ""
			},
			wantPath: "resultType",
		},
		{
			name: "unknown return arity",
			mutate: func(b *Blueprint) {
				b.Resources[0].Binding.Read.Return = "maybe"
			},
			wantPath: "return",
		},
		{
			name: "literal argument with no expression",
			mutate: func(b *Blueprint) {
				b.Resources[0].Binding.Read.Args = []Argument{{Kind: ArgLiteral}}
			},
			wantPath: "expr",
		},
		{
			// A writable attribute whose value can never reach the API is inert,
			// and inert is worse than broken because nothing complains.
			name: "writable attribute with skipExpand",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].Wire.SkipExpand = true
			},
			wantPath: "would never reach the API",
		},
		{
			name: "writable attribute with no expand conversion",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].Wire.Expand = nil
			},
			wantPath: "wire.expand",
		},
		{
			name: "attribute with neither flatten nor skipFlatten",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[0].Wire.Flatten = nil
			},
			wantPath: "wire.flatten",
		},
		{
			// A default on a non-computed attribute is silently dead config.
			name: "default on a non-computed attribute",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].Default = &Default{
					Static: &Literal{Kind: KindString, Raw: `"x"`},
				}
			},
			wantPath: "default",
		},
		{
			name: "default setting both static and custom",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[0].Default = &Default{
					Static: &Literal{Kind: KindString, Raw: `"x"`},
					Custom: &CustomCode{SchemaDefinition: "x()"},
				}
			},
			wantPath: "mutually exclusive",
		},
		{
			name: "unknown access style",
			mutate: func(b *Blueprint) {
				b.Resources[0].Binding.Body.AccessStyle = "telepathy"
			},
			wantPath: "access style",
		},
		{
			// A variant with no precondition is the unconditional Behaviour wearing a
			// costume: it would read as conditional while applying always.
			name: "behaviour variant with no precondition",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].Behaviour.Conditional = []BehaviourVariant{
					{Behaviour: Behaviour{RequiredByAPI: ptrBool(true)}},
				}
			},
			wantPath: "conditional[0].when",
		},
		{
			// Absence of a gate value is a different observation from the gate holding
			// the empty string, and nothing distinguishes them once written.
			name: "condition with no value",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].Behaviour.Conditional = []BehaviourVariant{
					{
						When:      []Condition{{JSONPath: "id", Equals: ""}},
						Behaviour: Behaviour{RequiredByAPI: ptrBool(true)},
					},
				}
			},
			wantPath: "when[0].equals",
		},
		{
			name: "condition naming one path twice",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].Behaviour.Conditional = []BehaviourVariant{
					{
						When: []Condition{
							{JSONPath: "id", Equals: "a"},
							{JSONPath: "id", Equals: "b"},
						},
						Behaviour: Behaviour{RequiredByAPI: ptrBool(true)},
					},
				}
			},
			wantPath: "appears twice",
		},
		{
			name: "variant observing nothing",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].Behaviour.Conditional = []BehaviourVariant{
					{When: []Condition{{JSONPath: "id", Equals: "x"}}},
				}
			},
			wantPath: "observes nothing",
		},
		{
			// Conditions are conjunctive, so nesting expresses nothing a flat when
			// cannot -- it could only introduce ambiguity about which level wins.
			name: "variant nesting a variant",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].Behaviour.Conditional = []BehaviourVariant{
					{
						When: []Condition{{JSONPath: "id", Equals: "x"}},
						Behaviour: Behaviour{Conditional: []BehaviourVariant{
							{
								When:      []Condition{{JSONPath: "id", Equals: "y"}},
								Behaviour: Behaviour{RequiredByAPI: ptrBool(true)},
							},
						}},
					},
				}
			},
			wantPath: "must not nest",
		},
		{
			// Two variants for one branch would race: which one a consumer honours
			// would depend on iteration order.
			name: "two variants for one branch",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].Behaviour.Conditional = []BehaviourVariant{
					{
						When:      []Condition{{JSONPath: "id", Equals: "x"}},
						Behaviour: Behaviour{RequiredByAPI: ptrBool(true)},
					},
					{
						When:      []Condition{{JSONPath: "id", Equals: "x"}},
						Behaviour: Behaviour{ReturnedOnRead: ptrBool(false)},
					},
				}
			},
			wantPath: "duplicates another variant",
		},
		{
			// A condition on a path nothing has would never hold and never fail: the
			// variant would sit in the committed file constraining nothing.
			name: "condition gated on a path the schema lacks",
			mutate: func(b *Blueprint) {
				b.Resources[0].Schema.Attributes[1].Behaviour.Conditional = []BehaviourVariant{
					{
						When:      []Condition{{JSONPath: "objectType", Equals: "static"}},
						Behaviour: Behaviour{RequiredByAPI: ptrBool(true)},
					},
				}
			},
			wantPath: "not a wire path",
		},
		{
			// Opening the value is the one thing an ephemeral does.
			name: "ephemeral with no open operation",
			mutate: func(b *Blueprint) {
				e := validEphemeral()
				e.Binding.Open = nil
				b.Ephemerals = []Ephemeral{e}
			},
			wantPath: "binding.open",
		},
		{
			// Modelled so a blueprint written today needs no reshaping; refused so
			// nothing claims support the emitter does not have.
			name: "ephemeral declaring renew before it renders",
			mutate: func(b *Blueprint) {
				e := validEphemeral()
				e.Binding.Renew = &Operation{
					Style: CallStyleMethod, Method: "RenewCredential",
					Return: ReturnResultTransportError, ResultType: "credentials.Credential",
				}
				b.Ephemerals = []Ephemeral{e}
			},
			wantPath: "binding.renew",
		},
		{
			// BlockEphemeral has no Default field, so emitting one would not compile.
			name: "ephemeral attribute carrying a default",
			mutate: func(b *Blueprint) {
				e := validEphemeral()
				e.Schema.Attributes[1].Default = &Default{
					Static: &Literal{Kind: KindString, Raw: `"x"`},
				}
				b.Ephemerals = []Ephemeral{e}
			},
			wantPath: "default",
		},
		{
			// BlockList refuses Computed: there is nowhere to put a result, which is
			// the structural reason the schema is filter-only.
			name: "list filter attribute that is computed",
			mutate: func(b *Blueprint) {
				addListFacet(&b.Resources[0])
				b.Resources[0].List.Schema = &Schema{Attributes: []Attribute{{
					Name: "state", GoField: "State",
					Type:                     AttrType{Kind: KindString},
					ComputedOptionalRequired: Computed,
					Wire:                     WireBinding{JSONPath: "state", SkipFlatten: true},
				}}}
			},
			wantPath: "computedOptionalRequired",
		},
		{
			// An ArgConfigField naming a model field no filter attribute has would
			// generate a read that does not compile.
			name: "list read arg naming an undeclared filter field",
			mutate: func(b *Blueprint) {
				addListFacet(&b.Resources[0])
				b.Resources[0].List.Read.Args = []Argument{
					{Kind: ArgContext},
					{Kind: ArgConfigField, Field: "Missing"},
				}
			},
			wantPath: "read.args[1].field",
		},
		{
			name: "acceptance seed naming an unknown resource",
			mutate: func(b *Blueprint) {
				b.DataSources = []DataSource{validDataSourceWithSeed("octopus")}
			},
			wantPath: "accTest.seedResourceKey",
		},
		{
			name: "acceptance seed arg naming an attribute the seed lacks",
			mutate: func(b *Blueprint) {
				ds := validDataSourceWithSeed("tag")
				ds.AccTest.Args = []SeedArg{{Attr: "id", FromSeedAttr: "nonexistent"}}
				b.DataSources = []DataSource{ds}
			},
			wantPath: "fromSeedAttr",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b := validBlueprint()
			tc.mutate(&b)

			err := b.Validate()
			if err == nil {
				t.Fatalf("expected a validation error mentioning %q, got nil", tc.wantPath)
			}
			if !errors.Is(err, ErrInvalid) && !strings.Contains(err.Error(), tc.wantPath) {
				t.Fatalf("error should wrap ErrInvalid: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantPath) {
				t.Errorf("error does not mention %q:\n%v", tc.wantPath, err)
			}
		})
	}
}

// TestUnit_Blueprint_Validate_AcceptsTheNewKinds is the control for the refusal cases
// above: a well-formed ephemeral, list filter schema and acceptance seeds all validate,
// or every refusal could be passing for the wrong reason.
func TestUnit_Blueprint_Validate_AcceptsTheNewKinds(t *testing.T) {
	t.Parallel()

	b := validBlueprint()
	b.Ephemerals = []Ephemeral{validEphemeral()}
	b.DataSources = []DataSource{validDataSourceWithSeed("tag")}

	addListFacet(&b.Resources[0])
	b.Resources[0].List.Schema = &Schema{Attributes: []Attribute{{
		Name: "state", GoField: "State",
		Type:                     AttrType{Kind: KindString},
		ComputedOptionalRequired: Optional,
		Wire:                     WireBinding{JSONPath: "state", SkipFlatten: true},
	}}}
	b.Resources[0].List.Read.Args = []Argument{
		{Kind: ArgContext},
		{Kind: ArgConfigField, Field: "State"},
	}

	if err := b.Validate(); err != nil {
		t.Fatalf("a well-formed document with the new kinds must validate: %v", err)
	}
}

// TestUnit_Blueprint_Validate_SweepNaming holds an override to the same rules the
// prober's own inference follows: a field that could not carry the prefix live must be
// refused here, where the message names the blueprint node to fix.
func TestUnit_Blueprint_Validate_SweepNaming(t *testing.T) {
	t.Parallel()

	valid := validBlueprint()
	valid.Resources[0].Sweep = &SweepNaming{NameField: "key", ReadNameField: "keyName"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a well-formed sweep override must validate: %v", err)
	}

	tests := []struct {
		name     string
		sweep    *SweepNaming
		wantPath string
	}{
		{
			name:     "empty name field",
			sweep:    &SweepNaming{},
			wantPath: "sweep.nameField",
		},
		{
			name:     "nested name field",
			sweep:    &SweepNaming{NameField: "meta.name"},
			wantPath: "nested",
		},
		{
			name:     "unknown wire path",
			sweep:    &SweepNaming{NameField: "nope"},
			wantPath: "no attribute has the wire path",
		},
		{
			// id is Computed with SkipExpand in the fixture: a create could
			// never carry the prefix there.
			name:     "unwritable name field",
			sweep:    &SweepNaming{NameField: "id"},
			wantPath: "not writable",
		},
		{
			name:     "nested read field",
			sweep:    &SweepNaming{NameField: "key", ReadNameField: "meta.name"},
			wantPath: "sweep.readNameField",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b := validBlueprint()
			b.Resources[0].Sweep = tc.sweep

			err := b.Validate()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantPath) {
				t.Errorf("error omits %q:\n%v", tc.wantPath, err)
			}
		})
	}
}

// TestUnit_Blueprint_Validate_ReportsEveryProblem matters because fixing a
// blueprint one error per run is miserable, and collecting them costs nothing.
func TestUnit_Blueprint_Validate_ReportsEveryProblem(t *testing.T) {
	t.Parallel()

	b := validBlueprint()
	b.Provider.Name = ""
	b.Provider.GoModule = ""
	b.Resources[0].GoTypeName = ""

	err := b.Validate()
	if err == nil {
		t.Fatal("expected an error")
	}

	for _, want := range []string{"provider.name", "provider.goModule", "goTypeName"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error omits %q:\n%v", want, err)
		}
	}
}

// TestUnit_Blueprint_Validate_SkipsDroppedNodes confirms a dropped resource is
// excluded rather than validated, so a deliberately-excluded incomplete resource
// does not block the whole run.
func TestUnit_Blueprint_Validate_SkipsDroppedNodes(t *testing.T) {
	t.Parallel()

	b := validBlueprint()
	broken := Resource{Key: "broken", Drop: true}
	b.Resources = append(b.Resources, broken)

	if err := b.Validate(); err != nil {
		t.Errorf("a dropped resource must not be validated: %v", err)
	}
}

func TestUnit_Blueprint_RoundTrip_IsByteStable(t *testing.T) {
	t.Parallel()

	original := validBlueprint()

	first, err := Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	parsed, err := Unmarshal(first)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	second, err := Marshal(parsed)
	if err != nil {
		t.Fatalf("re-Marshal: %v", err)
	}

	// Byte equality, not deep equality. CI regenerates blueprints and fails on
	// any diff, so an encoding that merely round-trips semantically would still
	// make the drift check fire on a run that changed nothing.
	if string(first) != string(second) {
		t.Errorf("round trip is not byte-stable\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestUnit_Blueprint_Marshal_IsDeterministic guards the property the drift gate
// depends on most directly.
func TestUnit_Blueprint_Marshal_IsDeterministic(t *testing.T) {
	t.Parallel()

	b := validBlueprint()

	first, err := Marshal(b)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	for i := 0; i < 20; i++ {
		again, err := Marshal(b)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if string(again) != string(first) {
			t.Fatalf("Marshal is not deterministic; run %d differed", i)
		}
	}
}

// TestUnit_Blueprint_Unmarshal_RejectsUnknownFields is the guard against the
// worst failure mode for a hand-authored document: a mistyped key silently
// ignored, leaving the author with a provider that behaves unlike what they wrote.
func TestUnit_Blueprint_Unmarshal_RejectsUnknownFields(t *testing.T) {
	t.Parallel()

	// The version is interpolated rather than written out: the version check runs
	// before the strict decode, so a stale literal here would turn this into a second
	// test of the version check instead of the unknown-field check.
	data := fmt.Sprintf(`{
	  "formatVersion": %q,
	  "provider": {"name": "x", "goModule": "m", "typePrefix": "x", "updateStile": "putFull",
	               "sdk": {"dialect": "restyService", "modulePath": "m", "clientType": "*c"}}
	}`, FormatVersion)

	_, err := Unmarshal([]byte(data))
	if err == nil {
		t.Fatal("expected a typo'd key to be rejected")
	}
	if !strings.Contains(err.Error(), "updateStile") {
		t.Errorf("error should name the unknown field: %v", err)
	}
}

func TestUnit_Blueprint_Unmarshal_RejectsUnsupportedFormatVersion(t *testing.T) {
	t.Parallel()

	_, err := Unmarshal([]byte(`{"formatVersion": "999"}`))
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("error = %v, want it to wrap ErrUnsupportedFormat", err)
	}

	// An unknown version gets no migration note rather than a wrong one.
	if strings.Contains(err.Error(), "renamed") {
		t.Errorf("a version with no known migration should describe none: %v", err)
	}
}

// TestUnit_Blueprint_MigrationNoteMatchesTheVersionFound guards a message that silently
// goes stale.
//
// The note used to be one hardcoded sentence about the format 2 move, which stopped being
// the whole story the moment format 3 landed. A reader of the error acts on it, so a
// description of the wrong migration is worse than none.
func TestUnit_Blueprint_MigrationNoteMatchesTheVersionFound(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		found string
		want  []string
	}{
		// From 1 there are two migrations to describe, not one.
		{"1", []string{"schema", "terraformType", "allowedValues"}},
		{"2", []string{"allowedValues"}},
	} {
		_, err := Unmarshal([]byte(`{"formatVersion": "` + tc.found + `"}`))
		if err == nil {
			t.Fatalf("format %s should be refused", tc.found)
		}
		for _, want := range tc.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the note for format %s should mention %q: %v", tc.found, want, err)
			}
		}
	}

	// And the note for 2 must not describe the format 2 migration, which has already
	// happened by then.
	_, err := Unmarshal([]byte(`{"formatVersion": "2"}`))
	if strings.Contains(err.Error(), "terraformType") {
		t.Errorf("a format 2 document has already had that migration applied: %v", err)
	}
}

func TestUnit_Blueprint_SaveAndLoad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "tag"+Ext)

	want := validBlueprint()
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Provider.Name != want.Provider.Name || len(got.Resources) != len(want.Resources) {
		t.Errorf("loaded blueprint differs from the saved one")
	}

	// Written files end in exactly one newline, so a blueprint is a well-formed
	// text file and git does not report a missing terminator.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.HasSuffix(string(data), "}\n") || strings.HasSuffix(string(data), "\n\n") {
		t.Errorf("file should end with exactly one newline, got %q", tail(string(data), 8))
	}
}

func TestUnit_Blueprint_LoadDir_MergesAndSorts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// The provider block lives in its own file, and resources in theirs, which
	// is how a real provider is laid out.
	providerOnly := Blueprint{FormatVersion: FormatVersion, Provider: validBlueprint().Provider}
	if err := Save(filepath.Join(dir, "provider"+Ext), providerOnly); err != nil {
		t.Fatalf("Save provider: %v", err)
	}

	// Written in an order that is not the sorted order, so the sort is actually
	// exercised.
	zebra := validResource()
	zebra.Key = "zebra"
	zebra.Name = "zebra"
	zebra.GoPackageAlias = "v7Zebra"
	if err := Save(filepath.Join(dir, "resources", "zebra"+Ext),
		Blueprint{FormatVersion: FormatVersion, Resources: []Resource{zebra}}); err != nil {
		t.Fatalf("Save zebra: %v", err)
	}
	if err := Save(filepath.Join(dir, "resources", "tag"+Ext),
		Blueprint{FormatVersion: FormatVersion, Resources: []Resource{validResource()}}); err != nil {
		t.Fatalf("Save tag: %v", err)
	}

	got, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	if len(got.Resources) != 2 {
		t.Fatalf("got %d resources, want 2", len(got.Resources))
	}
	if got.Resources[0].Key != "tag" || got.Resources[1].Key != "zebra" {
		t.Errorf("resources are not sorted by key: %s, %s", got.Resources[0].Key, got.Resources[1].Key)
	}
	if got.Provider.Name != "thousandeyes" {
		t.Errorf("provider block was not merged in")
	}
}

// TestUnit_Blueprint_LoadDir_RejectsTwoProviderBlocks guards against the emitter
// silently picking one, with the choice depending on filename ordering.
func TestUnit_Blueprint_LoadDir_RejectsTwoProviderBlocks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	bp := validBlueprint()

	if err := Save(filepath.Join(dir, "a"+Ext), bp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	second := validBlueprint()
	second.Resources = nil
	if err := Save(filepath.Join(dir, "b"+Ext), second); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, err := LoadDir(dir)
	if err == nil {
		t.Fatal("expected two provider blocks to be rejected")
	}
	if !strings.Contains(err.Error(), "provider block") {
		t.Errorf("error should explain the conflict: %v", err)
	}
}

// TestUnit_Blueprint_LoadDir_RejectsAnEmptyDirectory exists because returning
// success with nothing to emit makes a mistyped path look like a working run.
func TestUnit_Blueprint_LoadDir_RejectsAnEmptyDirectory(t *testing.T) {
	t.Parallel()

	_, err := LoadDir(t.TempDir())
	if !errors.Is(err, ErrNoBlueprint) {
		t.Errorf("error = %v, want it to wrap ErrNoBlueprint", err)
	}
}

// TestUnit_Blueprint_LoadDir_ValidatesAcrossFiles is the reason validation runs
// on the merged document: a cross-resource collision is invisible to a per-file
// check, and it is exactly what stops a provider from starting.
func TestUnit_Blueprint_LoadDir_ValidatesAcrossFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if err := Save(filepath.Join(dir, "provider"+Ext),
		Blueprint{FormatVersion: FormatVersion, Provider: validBlueprint().Provider}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Two files, each individually fine, colliding on Terraform type.
	for _, name := range []string{"one", "two"} {
		r := validResource()
		r.Key = name
		r.GoPackageAlias = "alias" + name
		if err := Save(filepath.Join(dir, name+Ext),
			Blueprint{FormatVersion: FormatVersion, Resources: []Resource{r}}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	_, err := LoadDir(dir)
	if err == nil {
		t.Fatal("expected the cross-file type collision to be caught")
	}
	if !strings.Contains(err.Error(), "used more than once") {
		t.Errorf("error should name the collision: %v", err)
	}
}

func TestUnit_Blueprint_Presence_Predicates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		p                              ComputedOptionalRequired
		computed, optional, isRequired bool
	}{
		{Required, false, false, true},
		{Optional, false, true, false},
		{Computed, true, false, false},
		{ComputedOptional, true, true, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.p), func(t *testing.T) {
			t.Parallel()
			if got := tc.p.IsComputed(); got != tc.computed {
				t.Errorf("IsComputed() = %v, want %v", got, tc.computed)
			}
			if got := tc.p.IsOptional(); got != tc.optional {
				t.Errorf("IsOptional() = %v, want %v", got, tc.optional)
			}
			if got := tc.p.IsRequired(); got != tc.isRequired {
				t.Errorf("IsRequired() = %v, want %v", got, tc.isRequired)
			}
		})
	}
}

func TestUnit_Blueprint_ReturnArity_Predicates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		r                     ReturnArity
		hasResult, hasTranspt bool
	}{
		{ReturnResultTransportError, true, true},
		{ReturnTransportError, false, true},
		{ReturnResultError, true, false},
		{ReturnError, false, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.r), func(t *testing.T) {
			t.Parallel()
			if got := tc.r.HasResult(); got != tc.hasResult {
				t.Errorf("HasResult() = %v, want %v", got, tc.hasResult)
			}
			if got := tc.r.HasTransport(); got != tc.hasTranspt {
				t.Errorf("HasTransport() = %v, want %v", got, tc.hasTranspt)
			}
		})
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
