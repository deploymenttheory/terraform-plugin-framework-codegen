package blueprint

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors. The house lint configuration enables err113, so every error
// originates from one of these and callers can branch on the class rather than
// on message text.
var (
	ErrInvalid           = errors.New("invalid blueprint")
	ErrUnsupportedFormat = errors.New("unsupported blueprint format version")
	ErrNoBlueprint       = errors.New("no blueprint found")
)

// problem is one validation failure, addressed by a path into the document so a
// message names the offending node rather than describing it.
type problem struct {
	path string
	msg  string
}

type problems []problem

func (p *problems) add(path, format string, args ...any) {
	*p = append(*p, problem{path: path, msg: fmt.Sprintf(format, args...)})
}

// err folds the accumulated problems into a single error.
//
// Every problem is reported, not just the first. A generator run that surfaces
// one error per invocation turns fixing a blueprint into a guessing game, and the
// cost of collecting them is nothing.
func (p problems) err() error {
	if len(p) == 0 {
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d problem(s):", len(p))
	for _, pr := range p {
		fmt.Fprintf(&b, "\n  %s: %s", pr.path, pr.msg)
	}

	return fmt.Errorf("%w: %s", ErrInvalid, b.String())
}

// Validate reports every structural problem in the blueprint.
//
// It checks what would otherwise fail as a Go compile error in emitted code or,
// worse, as a runtime surprise: a schema attribute with no model field, a call
// binding with no method, an attribute whose type needs an element type and has
// none. The emitter is entitled to assume a validated blueprint.
func (b Blueprint) Validate() error {
	var p problems

	if b.FormatVersion != FormatVersion {
		p.add("formatVersion", "is %q, want %q", b.FormatVersion, FormatVersion)
	}

	b.Provider.validate(&p)

	seenKeys := map[string]bool{}
	seenTypes := map[string]bool{}
	seenAliases := map[string]bool{}

	for i, r := range b.Resources {
		at := fmt.Sprintf("resources[%d]", i)
		if r.Key != "" {
			at = fmt.Sprintf("resources[%s]", r.Key)
		}

		if r.Drop {
			continue
		}

		r.validate(at, &p)

		// Duplicates here are not cosmetic. Two resources sharing a Terraform
		// type make the provider fail to start; two sharing an import alias make
		// the registration file fail to compile; two sharing a key make probe
		// facts and overrides land on the wrong one.
		dup(&p, seenKeys, r.Key, at+".key", "resource key")
		dup(&p, seenTypes, r.Name, at+".name", "resource name")
		dup(&p, seenAliases, r.GoPackageAlias, at+".goPackageAlias", "import alias")
	}

	// Data sources were not validated at all before format 2, which mattered less while none were
	// emitted. Their keys and type names live in their own namespaces -- a `tag` resource and a
	// `tag` data source are a normal pair -- but the import alias is shared, because both register
	// into the same generated provider package.
	seenDataKeys := map[string]bool{}
	seenDataTypes := map[string]bool{}
	seenActionKeys := map[string]bool{}
	seenActionTypes := map[string]bool{}

	for i, d := range b.DataSources {
		at := fmt.Sprintf("dataSources[%d]", i)
		if d.Key != "" {
			at = fmt.Sprintf("dataSources[%s]", d.Key)
		}

		if d.Drop {
			continue
		}

		d.validate(at, &p)

		dup(&p, seenDataKeys, d.Key, at+".key", "data source key")
		dup(&p, seenDataTypes, d.Name, at+".name", "data source name")
		dup(&p, seenAliases, d.GoPackageAlias, at+".goPackageAlias", "import alias")
	}

	for i, a := range b.Actions {
		at := fmt.Sprintf("actions[%d]", i)
		if a.Key != "" {
			at = fmt.Sprintf("actions[%s]", a.Key)
		}

		if a.Drop {
			continue
		}

		a.validate(at, &p)

		dup(&p, seenActionKeys, a.Key, at+".key", "action key")
		dup(&p, seenActionTypes, a.Name, at+".name", "action name")
		dup(&p, seenAliases, a.GoPackageAlias, at+".goPackageAlias", "import alias")
	}

	seenEphemeralKeys := map[string]bool{}
	seenEphemeralTypes := map[string]bool{}

	for i, e := range b.Ephemerals {
		at := fmt.Sprintf("ephemerals[%d]", i)
		if e.Key != "" {
			at = fmt.Sprintf("ephemerals[%s]", e.Key)
		}

		if e.Drop {
			continue
		}

		e.validate(at, &p)

		dup(&p, seenEphemeralKeys, e.Key, at+".key", "ephemeral key")
		dup(&p, seenEphemeralTypes, e.Name, at+".name", "ephemeral name")
		dup(&p, seenAliases, e.GoPackageAlias, at+".goPackageAlias", "import alias")
	}

	b.validateAccSeeds(&p)

	return p.err()
}

// validate checks one ephemeral resource.
//
// Structurally a data source whose result lives outside state, so this mirrors
// DataSource.validate; the differences are the binding's lifecycle operations and the
// attribute rules BlockEphemeral already encodes.
func (e Ephemeral) validate(at string, p *problems) {
	required(p, at+".key", e.Key)
	required(p, at+".name", e.Name)
	required(p, at+".goPackage", e.GoPackage)
	required(p, at+".goPackageAlias", e.GoPackageAlias)
	required(p, at+".goTypeName", e.GoTypeName)
	required(p, at+".modelTypeName", e.ModelTypeName)

	if len(e.Schema.Attributes) == 0 {
		p.add(at+".schema.attributes", "an ephemeral with no attributes cannot be emitted")
	}

	e.Binding.validate(at+".binding", p)

	seenNames := map[string]bool{}
	seenFields := map[string]bool{}

	for i, a := range e.Schema.Attributes {
		aat := fmt.Sprintf("%s.schema.attributes[%d]", at, i)
		if a.Name != "" {
			aat = fmt.Sprintf("%s.schema.attributes[%s]", at, a.Name)
		}

		if a.Drop {
			continue
		}

		a.validate(aat, p)
		a.validateForKind(BlockEphemeral, aat, p)

		dup(p, seenNames, a.Name, aat+".name", "attribute name")
		dup(p, seenFields, a.GoField, aat+".goField", "model field")
	}
}

func (b EphemeralBinding) validate(at string, p *problems) {
	required(p, at+".service.importPath", b.Service.ImportPath)
	required(p, at+".service.typeName", b.Service.TypeName)
	required(p, at+".service.accessor", b.Service.Accessor)

	required(p, at+".response.type", b.Response.Type)

	switch b.Response.AccessStyle {
	case AccessStructField:
	case AccessMethod:
		p.add(
			at+".response.accessStyle",
			"%q is reserved but not yet implemented by the emitter",
			b.Response.AccessStyle,
		)
	default:
		p.add(at+".response.accessStyle", "%q is not a known access style", b.Response.AccessStyle)
	}

	if b.Open == nil {
		p.add(at+".open", "is required: opening the value is the one thing an ephemeral does")
	} else {
		b.Open.validate(at+".open", p)
	}

	// Modelled so a blueprint written today needs no reshaping, refused so nothing claims
	// support the emitter does not have. The message names the field to remove.
	if b.Renew != nil {
		p.add(at+".renew", "is modelled but not yet rendered; remove it until the emitter supports renew")
	}
	if b.Close != nil {
		p.add(at+".close", "is modelled but not yet rendered; remove it until the emitter supports close")
	}
}

// validateAccSeeds checks the acceptance-seed declarations against the whole document.
//
// Checked at the blueprint level because a seed's whole job is to reference *another*
// block: a data source's test creates the seed resource's object and reads it back, so
// both ends of every mapping must exist or the generated test cannot compile.
// checkEnvName refuses a gate variable that could never be set.
//
// The generated skip is `os.Getenv(name) == ""`, so a name with a space or an equals
// sign is a test that skips forever while looking gated on purpose. Uppercase with
// underscores is the only shape worth accepting; anything else is a typo.
func checkEnvName(p *problems, at, name string) {
	if name == "" {
		return
	}

	for i, r := range name {
		ok := r == '_' || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			p.add(at, "%q is not an environment variable name (UPPER_SNAKE_CASE)", name)
			return
		}
	}
}

func (b Blueprint) validateAccSeeds(p *problems) {
	resources := map[string]Resource{}
	for _, r := range b.Resources {
		if !r.Drop {
			resources[r.Key] = r
		}
	}

	checkSeed := func(at string, seed *AccSeed, attrs []Attribute) {
		if seed == nil {
			return
		}

		checkEnvName(p, at+".skipUnlessEnv", seed.SkipUnlessEnv)

		required(p, at+".seedResourceKey", seed.SeedResourceKey)

		seedRes, ok := resources[seed.SeedResourceKey]
		if seed.SeedResourceKey != "" && !ok {
			p.add(at+".seedResourceKey",
				"names resource %q, which this blueprint does not declare", seed.SeedResourceKey)
		}

		for i, arg := range seed.Args {
			aat := fmt.Sprintf("%s.args[%d]", at, i)

			required(p, aat+".attr", arg.Attr)
			required(p, aat+".fromSeedAttr", arg.FromSeedAttr)

			if arg.Attr != "" && !hasAttributeNamed(attrs, arg.Attr) {
				p.add(aat+".attr", "names attribute %q, which this block does not declare", arg.Attr)
			}
			if ok && arg.FromSeedAttr != "" &&
				!hasAttributeNamed(seedRes.Schema.Attributes, arg.FromSeedAttr) {
				p.add(aat+".fromSeedAttr",
					"names attribute %q, which resource %q does not declare",
					arg.FromSeedAttr, seed.SeedResourceKey)
			}
		}
	}

	for _, d := range b.DataSources {
		if !d.Drop {
			checkSeed(fmt.Sprintf("dataSources[%s].accTest", d.Key), d.AccTest, d.Schema.Attributes)
		}
	}
	for _, e := range b.Ephemerals {
		if !e.Drop {
			checkSeed(fmt.Sprintf("ephemerals[%s].accTest", e.Key), e.AccTest, e.Schema.Attributes)
		}
	}

	for _, a := range b.Actions {
		if a.Drop || a.AccTest == nil {
			continue
		}
		at := fmt.Sprintf("actions[%s].accTest", a.Key)

		for i, env := range a.AccTest.EnvArgs {
			aat := fmt.Sprintf("%s.envArgs[%d]", at, i)
			required(p, aat+".attr", env.Attr)
			required(p, aat+".envVar", env.EnvVar)
			if env.Attr != "" && !hasAttributeNamed(a.Schema.Attributes, env.Attr) {
				p.add(aat+".attr", "names attribute %q, which this action does not declare", env.Attr)
			}
		}

		if a.AccTest.Cleanup != nil {
			a.AccTest.Cleanup.validate(at+".cleanup", p)
		}
	}
}

// validate checks one action.
//
// The skeleton a resource and a data source share, minus everything an action has nothing to
// reconcile with: no policy, no import, no identity, and one operation rather than four.
// Attributes are validated against BlockAction, which refuses Computed and Sensitive --
// an action has no state for a computed value to live in.
func (a Action) validate(at string, p *problems) {
	required(p, at+".key", a.Key)
	required(p, at+".name", a.Name)
	required(p, at+".goPackage", a.GoPackage)
	required(p, at+".goPackageAlias", a.GoPackageAlias)
	required(p, at+".goTypeName", a.GoTypeName)
	required(p, at+".modelTypeName", a.ModelTypeName)

	if len(a.Schema.Attributes) == 0 {
		p.add(at+".schema.attributes",
			"an action with no attributes cannot be emitted: it would have nothing to act on")
	}

	a.Binding.validate(at+".binding", p)

	seenNames := map[string]bool{}
	seenFields := map[string]bool{}

	for i, attr := range a.Schema.Attributes {
		aat := fmt.Sprintf("%s.schema.attributes[%d]", at, i)
		if attr.Name != "" {
			aat = fmt.Sprintf("%s.schema.attributes[%s]", at, attr.Name)
		}

		if attr.Drop {
			continue
		}

		attr.validate(aat, p)
		attr.validateForKind(BlockAction, aat, p)

		dup(p, seenNames, attr.Name, aat+".name", "attribute name")
		dup(p, seenFields, attr.GoField, aat+".goField", "model field")
	}
}

// validate checks an action's binding.
//
// One operation and no response model. An action returns nothing to Terraform -- there is no
// field on InvokeResponse to put a result in -- so whatever the SDK hands back is discarded,
// and asking for a response type would be asking for something with nowhere to go.
func (b ActionBinding) validate(at string, p *problems) {
	required(p, at+".service.importPath", b.Service.ImportPath)
	required(p, at+".service.typeName", b.Service.TypeName)
	required(p, at+".service.accessor", b.Service.Accessor)

	if b.Invoke == nil {
		p.add(at+".invoke", "is required: an action that calls nothing does nothing")
		return
	}

	b.Invoke.validate(at+".invoke", p)
}

// validate checks one data source.
//
// The same skeleton as a resource minus everything a data source has no operation for: no binding
// beyond a read, no policy, no import. Its attributes are validated against BlockDataSource, which
// refuses a default or a plan modifier -- fields the framework's datasource/schema package does not
// have.
func (d DataSource) validate(at string, p *problems) {
	required(p, at+".key", d.Key)
	required(p, at+".name", d.Name)
	required(p, at+".goPackage", d.GoPackage)
	required(p, at+".goPackageAlias", d.GoPackageAlias)
	required(p, at+".goTypeName", d.GoTypeName)
	required(p, at+".modelTypeName", d.ModelTypeName)

	if len(d.Schema.Attributes) == 0 {
		p.add(at+".schema.attributes", "a data source with no attributes cannot be emitted")
	}

	d.Binding.validate(at+".binding", p)

	seenNames := map[string]bool{}
	seenFields := map[string]bool{}

	for i, a := range d.Schema.Attributes {
		aat := fmt.Sprintf("%s.schema.attributes[%d]", at, i)
		if a.Name != "" {
			aat = fmt.Sprintf("%s.schema.attributes[%s]", at, a.Name)
		}

		if a.Drop {
			continue
		}

		a.validate(aat, p)
		a.validateForKind(BlockDataSource, aat, p)

		dup(p, seenNames, a.Name, aat+".name", "attribute name")
		dup(p, seenFields, a.GoField, aat+".goField", "model field")
	}
}

func dup(p *problems, seen map[string]bool, value, path, what string) {
	if value == "" {
		return
	}
	if seen[value] {
		p.add(path, "%s %q is used more than once", what, value)
	}
	seen[value] = true
}

func (pr Provider) validate(p *problems) {
	required(p, "provider.name", pr.Name)
	required(p, "provider.goModule", pr.GoModule)
	required(p, "provider.typePrefix", pr.TypePrefix)
	required(p, "provider.sdk.modulePath", pr.SDK.ModulePath)
	required(p, "provider.sdk.clientType", pr.SDK.ClientType)

	switch pr.SDK.Dialect {
	case DialectRestyService:
	case DialectKiotaFluent:
		p.add(
			"provider.sdk.dialect",
			"%q is reserved but not yet implemented by the emitter",
			pr.SDK.Dialect,
		)
	default:
		p.add("provider.sdk.dialect", "%q is not a known dialect", pr.SDK.Dialect)
	}
}

func (r Resource) validate(at string, p *problems) {
	required(p, at+".key", r.Key)
	required(p, at+".name", r.Name)
	required(p, at+".goPackage", r.GoPackage)
	required(p, at+".goPackageAlias", r.GoPackageAlias)
	required(p, at+".goTypeName", r.GoTypeName)
	required(p, at+".modelTypeName", r.ModelTypeName)

	if len(r.Schema.Attributes) == 0 {
		p.add(at+".schema.attributes", "a resource with no attributes cannot be emitted")
	}

	seenNames := map[string]bool{}
	seenFields := map[string]bool{}
	hasWritable := false

	for i, a := range r.Schema.Attributes {
		aat := fmt.Sprintf("%s.schema.attributes[%d]", at, i)
		if a.Name != "" {
			aat = fmt.Sprintf("%s.schema.attributes[%s]", at, a.Name)
		}
		if a.Drop {
			continue
		}

		a.validate(aat, p)
		a.validateForKind(BlockResource, aat, p)

		dup(p, seenNames, a.Name, aat+".name", "attribute name")
		// A duplicated Go field is the subtler failure: the schema is fine and
		// the model silently loses an attribute.
		dup(p, seenFields, a.GoField, aat+".goField", "model field")

		if !a.ComputedOptionalRequired.IsComputed() || a.ComputedOptionalRequired.IsOptional() {
			hasWritable = true
		}
	}

	r.Binding.validate(at+".binding", p)

	if r.Identity != nil {
		r.Identity.validate(r, at+".identity", p)
	}

	if r.List != nil {
		r.List.validate(r, at+".list", p)
	}

	for i, cv := range r.ConfigValidators {
		cv.validate(r, fmt.Sprintf("%s.configValidators[%d]", at, i), p)
	}
	r.validateSchemaVersion(at, p)
	r.validateIDBinding(at, seenNames, seenFields, p)
	r.validatePolicy(at, hasWritable, p)
	r.validateImport(at, seenNames, p)
	r.validateConditionGates(at, p)
	r.validateSweep(at, p)
	r.validateAccFixture(at, p)

	if r.AccTest != nil {
		checkEnvName(p, at+".accTest.skipUnlessEnv", r.AccTest.SkipUnlessEnv)
	}
}

// validateSweep holds a sweep-naming override to the rules inference follows.
//
// The name field is where a probe run's prefix is stamped, and the sweeper's only way of
// finding a stranded object is reading that prefix back. An override naming a field that is
// nested, unwritable or not a string would pass here and fail live -- as a create the API
// rejects, or worse, a sweep that matches nothing while orphans accumulate. So the write
// field is checked against the schema. The read field is not: an API that renames on read
// often returns the value under a key the schema never models, which is the very case the
// field exists for. All that can be demanded of it is that it is a top-level key.
func (r Resource) validateSweep(at string, p *problems) {
	if r.Sweep == nil {
		return
	}

	at += ".sweep"

	if r.Sweep.NameField == "" {
		p.add(at+".nameField", "a sweep override must name the field the prefix is written into")
		return
	}

	if strings.Contains(r.Sweep.NameField, ".") {
		p.add(at+".nameField",
			"%q is nested; the prefix must live in a top-level field the sweeper can read "+
				"back from a list response", r.Sweep.NameField)
		return
	}

	found := false
	for _, a := range r.Schema.Attributes {
		if a.Drop || a.Wire.JSONPath != r.Sweep.NameField {
			continue
		}

		found = true

		if a.Wire.SkipExpand || a.ComputedOptionalRequired == Computed {
			p.add(at+".nameField",
				"%q is not writable, so a create could never carry the prefix there",
				r.Sweep.NameField)
		}
		if a.Type.Kind != KindString {
			p.add(at+".nameField",
				"%q is %s, not a string; a name prefix cannot be stamped into it",
				r.Sweep.NameField, a.Type.Kind)
		}
	}

	if !found {
		p.add(at+".nameField", "no attribute has the wire path %q", r.Sweep.NameField)
	}

	if strings.Contains(r.Sweep.ReadNameField, ".") {
		p.add(at+".readNameField",
			"%q is nested; the sweeper reads the name from each top-level list item",
			r.Sweep.ReadNameField)
	}
}

// validateAccFixture holds curated fixture values to the schema they claim to serve.
//
// A hint is emitted verbatim into generated HCL, so the mistakes worth refusing are the
// ones that would emit a fixture that cannot apply or a value nothing consumes: an
// attribute that does not exist, one a practitioner could not configure anyway, an
// empty expression, or two hints fighting over one attribute.
func (r Resource) validateAccFixture(at string, p *problems) {
	if r.AccFixture == nil {
		return
	}

	at += ".accFixture"

	for i, block := range r.AccFixture.DataBlocks {
		if strings.TrimSpace(block) == "" {
			p.add(fmt.Sprintf("%s.dataBlocks[%d]", at, i), "is empty")
		}
	}

	seen := map[string]bool{}
	for i, h := range r.AccFixture.Values {
		hat := fmt.Sprintf("%s.values[%d]", at, i)
		if h.Attr != "" {
			hat = fmt.Sprintf("%s.values[%s]", at, h.Attr)
		}

		if h.Attr == "" {
			p.add(hat+".attr", "must name an attribute")
			continue
		}
		if strings.TrimSpace(h.HCL) == "" {
			p.add(hat+".hcl", "is empty; a hint exists to state the value the generator cannot derive")
		}
		if seen[h.Attr] {
			p.add(hat+".attr", "%q is hinted twice", h.Attr)
		}
		seen[h.Attr] = true

		found := false
		for _, a := range r.Schema.Attributes {
			if a.Drop || a.Name != h.Attr {
				continue
			}
			found = true
			if a.ComputedOptionalRequired == Computed {
				p.add(hat+".attr",
					"%q is computed only, so a fixture could not configure it", h.Attr)
			}
		}
		if !found {
			p.add(hat+".attr", "no attribute is named %q", h.Attr)
		}
	}
}

// validateConditionGates checks that every behaviour variant's condition names a wire path
// this schema actually has.
//
// Checked here rather than on the attribute, because the gate is nearly always a *different*
// attribute -- matchType's variant is gated on type -- so the whole schema's wire paths have
// to be in view. A condition on a path nothing has would never hold and never fail: the
// variant would sit in the committed file looking meaningful and constraining nothing.
func (r Resource) validateConditionGates(at string, p *problems) {
	known := map[string]bool{}
	var collect func(attrs []Attribute, prefix string)
	collect = func(attrs []Attribute, prefix string) {
		for _, a := range attrs {
			path := a.Wire.JSONPath
			if path == "" {
				path = a.Name
			}
			if prefix != "" {
				path = prefix + "." + path
			}
			known[path] = true
			if a.Type.NestedObject != nil {
				collect(a.Type.NestedObject.Attributes, path)
			}
		}
	}
	collect(r.Schema.Attributes, "")

	var check func(attrs []Attribute, prefix string)
	check = func(attrs []Attribute, prefix string) {
		for _, a := range attrs {
			aat := fmt.Sprintf("%s.schema.attributes[%s]", at, a.Name)
			if prefix != "" {
				aat = fmt.Sprintf("%s.schema.attributes[%s.%s]", at, prefix, a.Name)
			}
			for i, v := range a.Behaviour.Conditional {
				for j, c := range v.When {
					if c.JSONPath != "" && !known[c.JSONPath] {
						p.add(fmt.Sprintf("%s.behaviour.conditional[%d].when[%d].jsonPath", aat, i, j),
							"%q is not a wire path in this schema", c.JSONPath)
					}
				}
			}
			if a.Type.NestedObject != nil {
				check(a.Type.NestedObject.Attributes, a.Name)
			}
		}
	}
	check(r.Schema.Attributes, "")
}

// validateSchemaVersion refuses a schema version bump with nothing to migrate old state.
//
// The framework's behaviour decides this, and it is worth stating exactly, because the failure
// does not appear in testing. fwserver's UpgradeResourceState passes state through untouched when
// the stored version equals the schema version, and demands a ResourceWithUpgradeState carrying an
// entry for the stored version when it does not:
//
//	"This resource was implemented without an UpgradeState() method, however Terraform was
//	 expecting an implementation for version N upgrade."
//
// So a bumped version with no upgrader works perfectly for anyone creating the resource fresh, and
// fails for everyone holding state written by an earlier version -- which is precisely the people
// a version bump exists to serve, and nobody a green test suite covers.
//
// A version of zero is the framework's default and needs nothing.
func (r Resource) validateSchemaVersion(at string, p *problems) {
	switch {
	case r.Schema.Version < 0:
		p.add(at+".schema.version", "cannot be negative")

	case r.Schema.Version > 0 && !r.Hooks.StateUpgrade:
		p.add(at+".schema.version",
			"is %d, but hooks.stateUpgrade is not set, so nothing migrates state written by an "+
				"earlier version. Terraform refuses such a state with \"this resource was "+
				"implemented without an UpgradeState() method\", which affects only practitioners "+
				"upgrading -- exactly who the bump is for, and nobody a test suite covers",
			r.Schema.Version)

	case r.Schema.Version == 0 && r.Hooks.StateUpgrade:
		p.add(at+".hooks.stateUpgrade",
			"is set but schema.version is 0, so UpgradeState can never be called: the framework "+
				"passes state through untouched when the stored version already matches")
	}
}

// validateIDBinding checks the ID wiring points at an attribute that exists. A
// blueprint naming a nonexistent field here produces a generated create that
// assigns to nothing, which compiles only by coincidence.
func (r Resource) validateIDBinding(at string, names, fields map[string]bool, p *problems) {
	id := r.Binding.ID

	if id.Attribute == "" {
		p.add(at+".binding.id.attribute", "is required")
	} else if !names[id.Attribute] {
		p.add(
			at+".binding.id.attribute",
			"names attribute %q, which the resource does not declare",
			id.Attribute,
		)
	}

	if id.GoField == "" {
		p.add(at+".binding.id.goField", "is required")
	} else if !fields[id.GoField] {
		p.add(
			at+".binding.id.goField",
			"names model field %q, which the resource does not declare",
			id.GoField,
		)
	}

	// Only meaningful when there is a create to take an ID from.
	if r.Binding.Create != nil && id.FromCreate == "" {
		p.add(at+".binding.id.fromCreate", "is required when the resource has a create operation")
	}
}

func (r Resource) validatePolicy(at string, hasWritable bool, p *problems) {
	switch r.Policy.UpdateStyle {
	case UpdateMergePatch, UpdatePutFull:
		if r.Binding.Update == nil {
			p.add(
				at+".policy.updateStyle",
				"is %q but the resource has no update operation",
				r.Policy.UpdateStyle,
			)
		}
	case UpdateReplaceOnly:
		if r.Binding.Update != nil {
			p.add(
				at+".policy.updateStyle",
				"is %q but the resource declares an update operation",
				r.Policy.UpdateStyle,
			)
		}
	case "":
		// An update binding with no declared style is the dangerous gap: PUT and
		// PATCH differ in whether an omitted field is cleared, and guessing
		// wrong silently erases attributes the practitioner did not mention.
		if r.Binding.Update != nil {
			p.add(
				at+".policy.updateStyle",
				"is required when the resource has an update operation, "+
					"because whether an omitted field is preserved or cleared cannot be guessed",
			)
		}
	default:
		p.add(at+".policy.updateStyle", "%q is not a known update style", r.Policy.UpdateStyle)
	}

	// A resource with no update path and no writable attribute can never be
	// changed, which is almost always a modelling mistake rather than intent.
	if r.Binding.Update == nil && r.Policy.UpdateStyle != UpdateReplaceOnly && hasWritable {
		p.add(
			at+".policy.updateStyle",
			"should be %q: the resource has writable attributes but no update operation",
			UpdateReplaceOnly,
		)
	}
}

func (r Resource) validateImport(at string, names map[string]bool, p *problems) {
	switch r.Import.Style {
	case "", ImportUnsupported:
	case ImportPassthroughID:
		switch {
		case r.Import.Attribute == "":
			p.add(at+".import.attribute", "is required for a passthrough import")
		case !names[r.Import.Attribute]:
			p.add(
				at+".import.attribute",
				"names attribute %q, which the resource does not declare",
				r.Import.Attribute,
			)
		}
	default:
		p.add(at+".import.style", "%q is not a known import style", r.Import.Style)
	}
}

// validateServerDefaultIsComputed refuses an optional attribute the API fills in for itself.
//
// Terraform requires the value after an apply to equal the value it planned. An attribute that is
// Optional and not Computed plans as null when the practitioner omits it, so an API that supplies
// its own value makes every such apply fail:
//
//	Error: Provider produced inconsistent result after apply
//	unexpected new value: .icon: was null, but now cty.StringVal("LABEL").
//
// Computed is what tells Terraform the value may arrive from elsewhere. Adding it takes nothing
// away from a practitioner -- they can still set the attribute -- which is why this is refused
// outright rather than merely recommended, unlike promoting something to Required.
//
// The exception is an attribute the API never returns: state keeps the configured null, so the
// planned and applied values agree and there is nothing to reconcile.
//
// The pilot had this both ways round, which is what makes the rule worth enforcing rather than
// documenting: `color` was optional-and-computed and worked, `icon` was optional alone and failed
// on the first live apply.
func (a Attribute) validateServerDefaultIsComputed(at string, p *problems) {
	if a.Behaviour.ServerDefault == nil || a.ComputedOptionalRequired != Optional {
		return
	}
	if a.Behaviour.ReturnedOnRead != nil && !*a.Behaviour.ReturnedOnRead {
		return
	}

	p.add(at+".computedOptionalRequired",
		"is %q, but the API was observed applying its own value (%s) and returning it. Terraform "+
			"requires the applied value to equal the planned one, so every apply that omits this "+
			"attribute fails with \"was null, but now ...\". Use %q, which still lets a "+
			"practitioner set it",
		Optional, a.Behaviour.ServerDefault.Raw, ComputedOptional)
}

func (a Attribute) validate(at string, p *problems) {
	required(p, at+".name", a.Name)
	required(p, at+".goField", a.GoField)

	a.validateServerDefaultIsComputed(at, p)

	switch a.ComputedOptionalRequired {
	case Required, Optional, Computed, ComputedOptional:
	case "":
		p.add(at+".presence", "is required")
	default:
		p.add(at+".presence", "%q is not a known presence", a.ComputedOptionalRequired)
	}

	a.Type.validate(at+".type", p)

	// A default only takes effect on an attribute Terraform may fill in, so one
	// on a purely required or optional attribute is silently dead configuration.
	if a.Default != nil {
		if !a.ComputedOptionalRequired.IsComputed() {
			p.add(
				at+".default",
				"is set but presence is %q; a default only applies to a computed attribute",
				a.ComputedOptionalRequired,
			)
		}
		if a.Default.Static == nil && a.Default.Custom == nil {
			p.add(at+".default", "must set either static or custom")
		}
		if a.Default.Static != nil && a.Default.Custom != nil {
			p.add(at+".default", "sets both static and custom, which are mutually exclusive")
		}
	}

	a.validateConditionalBehaviour(at, p)

	// The wire directions are checked by validateForKind, because which of them an
	// attribute needs depends on whether its block kind sends anything to the API.
}

// validateConditionalBehaviour checks a variant's own shape.
//
// Whether each condition's jsonPath names a field the schema actually has is checked at the
// resource level, where the whole schema's wire paths are in view; everything self-contained
// is refused here, so a hand-edited variant fails close to the text that is wrong.
func (a Attribute) validateConditionalBehaviour(at string, p *problems) {
	seenWhen := map[string]bool{}

	for i, v := range a.Behaviour.Conditional {
		vat := fmt.Sprintf("%s.behaviour.conditional[%d]", at, i)

		if len(v.When) == 0 {
			// A variant with no precondition is the unconditional Behaviour wearing a
			// costume: it would read as conditional while applying always.
			p.add(vat+".when", "is required; an unconditional observation belongs in behaviour itself")
		}

		seenPath := map[string]bool{}
		for j, c := range v.When {
			cat := fmt.Sprintf("%s.when[%d]", vat, j)
			switch {
			case c.JSONPath == "":
				p.add(cat+".jsonPath", "is required")
			case c.Equals == "":
				// Absence of a gate value is a different observation from the gate holding
				// the empty string, and nothing distinguishes them once written.
				p.add(cat+".equals", "is required")
			case seenPath[c.JSONPath]:
				p.add(cat+".jsonPath", "%q appears twice in one precondition, which cannot both hold",
					c.JSONPath)
			}
			seenPath[c.JSONPath] = true
		}

		if v.Behaviour.IsZero() {
			p.add(vat+".behaviour", "observes nothing; a variant must say what held under its condition")
		}
		if len(v.Behaviour.Conditional) > 0 {
			p.add(vat+".behaviour.conditional",
				"a variant must not nest variants; conditions are conjunctive, so state them in one when")
		}

		if key := v.WhenKey(); key != "" {
			if seenWhen[key] {
				// Two variants for one branch would race: which one a consumer honours would
				// depend on iteration order.
				p.add(vat+".when", "duplicates another variant's precondition; fold the observations into one variant")
			}
			seenWhen[key] = true
		}
	}
}

func (t AttrType) validate(at string, p *problems) {
	if t.NestedObject != nil && !t.Kind.IsNested() {
		p.add(at+".nested", "is set on non-nested kind %q", t.Kind)
	}

	t.Constraints.validate(t.Kind, at+".constraints", p)

	switch t.Kind {
	case KindBool, KindString, KindInt32, KindInt64, KindFloat32, KindFloat64, KindNumber:
		if t.ElementType != nil {
			p.add(at+".elem", "is set on scalar kind %q", t.Kind)
		}
	case KindList, KindSet, KindMap:
		if t.ElementType == nil {
			p.add(at+".elem", "is required for collection kind %q", t.Kind)
			return
		}
		t.ElementType.validate(at+".elem", p)
	case KindListNested, KindSetNested, KindSingleNested:
		if t.ElementType != nil {
			p.add(at+".elem", "is set on nested kind %q, which uses nested instead", t.Kind)
		}
		if t.NestedObject == nil {
			p.add(at+".nested", "is required for nested kind %q", t.Kind)
			return
		}
		t.NestedObject.validate(at+".nested", p)
	case "":
		p.add(at+".kind", "is required")
	default:
		p.add(at+".kind", "%q is not a known type kind", t.Kind)
	}
}

// validate refuses a constraint the kind has no validator for.
//
// Every framework validator lives in a per-type package, so a bound on the wrong kind is not a
// warning -- it is a reference to a function that does not exist. Refusing here names the
// attribute; the alternative surfaces as a compile error in generated output.
func (c Constraints) validate(kind TypeKind, at string, p *problems) {
	if c.IsZero() {
		return
	}

	isString := kind == KindString
	isCollection := kind.IsCollection() || kind.IsNestedCollection()

	if c.Pattern != "" && !isString {
		p.add(at+".pattern", "applies to a string; %q has no RegexMatches validator", kind)
	}

	if (c.MinLength != nil || c.MaxLength != nil) && !isString {
		p.add(at+".minLength/maxLength",
			"bound a string's length; %q has no Length validator", kind)
	}

	if (c.MinItems != nil || c.MaxItems != nil) && !isCollection {
		p.add(at+".minItems/maxItems",
			"bound a collection's size; %q has no Size validator", kind)
	}

	if c.Minimum != nil || c.Maximum != nil {
		switch kind {
		case KindInt32, KindInt64, KindFloat32, KindFloat64:
		case KindNumber:
			// numbervalidator exists but carries only AtLeastOneOf: the framework provides no
			// range validator for an arbitrary-precision number, so there is nothing to
			// generate. A bound on one needs the attribute narrowing to a float or int kind.
			p.add(at+".minimum/maximum",
				"the framework's numbervalidator has no range validator; narrow the attribute "+
					"to an int or float kind to bound it")
		default:
			p.add(at+".minimum/maximum",
				"bound a number; %q has no range validator", kind)
		}
	}

	// A contradiction rather than a mistake about types: no value satisfies it, so every plan
	// against the generated provider would fail.
	if c.MinLength != nil && c.MaxLength != nil && *c.MinLength > *c.MaxLength {
		p.add(at+".minLength", "is %d, above maxLength %d, which nothing can satisfy",
			*c.MinLength, *c.MaxLength)
	}
	if c.MinItems != nil && c.MaxItems != nil && *c.MinItems > *c.MaxItems {
		p.add(at+".minItems", "is %d, above maxItems %d, which nothing can satisfy",
			*c.MinItems, *c.MaxItems)
	}
	if c.Minimum != nil && c.Maximum != nil && *c.Minimum > *c.Maximum {
		p.add(at+".minimum", "is %v, above maximum %v, which nothing can satisfy",
			*c.Minimum, *c.Maximum)
	}
}

func (n NestedAttributeObject) validate(at string, p *problems) {
	required(p, at+".goTypeName", n.GoTypeName)
	required(p, at+".sdkType", n.SDKType)
	required(p, at+".attrTypesVar", n.AttrTypesVar)
	required(p, at+".objectTypeVar", n.ObjectTypeVar)
	// expandFunc and flattenFunc are checked by validateWireForKind: a data source needs
	// only the flatten direction, so requiring both here would refuse a valid one.

	if len(n.Attributes) == 0 {
		p.add(at+".attributes", "a nested object with no attributes cannot be emitted")
		return
	}

	seenNames := map[string]bool{}
	seenFields := map[string]bool{}

	for i, a := range n.Attributes {
		aat := fmt.Sprintf("%s.schema.attributes[%d]", at, i)
		if a.Name != "" {
			aat = fmt.Sprintf("%s.schema.attributes[%s]", at, a.Name)
		}
		if a.Drop {
			continue
		}

		a.validate(aat, p)

		dup(p, seenNames, a.Name, aat+".name", "attribute name")
		dup(p, seenFields, a.GoField, aat+".goField", "model field")
	}
}

func (b ResourceBinding) validate(at string, p *problems) {
	required(p, at+".service.importPath", b.Service.ImportPath)
	required(p, at+".service.typeName", b.Service.TypeName)
	required(p, at+".service.accessor", b.Service.Accessor)

	required(p, at+".body.requestType", b.Body.RequestType)
	required(p, at+".body.responseType", b.Body.ResponseType)
	required(p, at+".body.constructorExpr", b.Body.ConstructorExpr)

	// The split-update pair travels together: a type with no constructor cannot be
	// instantiated, and a constructor with no type is a leftover from a removed split.
	// And a split with no update operation constrains nothing -- it would sit in the
	// committed file looking meaningful, which is worse than being refused.
	if b.Body.UpdateRequestType != "" && b.Body.UpdateConstructorExpr == "" {
		p.add(at+".body.updateConstructorExpr",
			"is required when updateRequestType is set: the emitter cannot instantiate "+
				"a body it has no constructor expression for")
	}
	if b.Body.UpdateConstructorExpr != "" && b.Body.UpdateRequestType == "" {
		p.add(at+".body.updateRequestType",
			"is required when updateConstructorExpr is set")
	}
	if b.Body.UpdateRequestType != "" && b.Update == nil {
		p.add(at+".body.updateRequestType",
			"names an update body type, but the binding has no update operation to send it")
	}

	switch b.Body.AccessStyle {
	case AccessStructField:
	case AccessMethod:
		p.add(
			at+".body.accessStyle",
			"%q is reserved but not yet implemented by the emitter",
			b.Body.AccessStyle,
		)
	default:
		p.add(at+".body.accessStyle", "%q is not a known access style", b.Body.AccessStyle)
	}

	// Read is what refreshes state, so a resource without one cannot participate
	// in a plan at all.
	if b.Read == nil {
		p.add(at+".read", "is required: a resource with no read operation cannot refresh state")
	}

	for name, op := range map[string]*Operation{
		"create": b.Create, "read": b.Read, "update": b.Update, "delete": b.Delete,
	} {
		if op != nil {
			op.validate(at+"."+name, p)
		}
	}
}

// validate checks a data source's binding.
//
// Read is not merely required, it is the whole binding: a data source that cannot read is
// not a data source. The resource equivalent has three more operations and a request
// body, none of which exist on this type, so there is nothing here to refuse.
// validate checks an identity schema.
//
// Every attribute must name a resource attribute it mirrors, because generated code copies
// the value out of the resource model rather than fetching it again -- and a name that does
// not resolve would be a compile error in someone else's provider.
func (ri ResourceIdentity) validate(r Resource, at string, p *problems) {
	required(p, at+".goTypeName", ri.GoTypeName)

	if len(ri.Attributes) == 0 {
		p.add(at+".attributes", "an identity schema with no attributes cannot be emitted")
		return
	}

	seenNames := map[string]bool{}
	seenFields := map[string]bool{}

	for i, a := range ri.Attributes {
		aat := fmt.Sprintf("%s.attributes[%d]", at, i)
		if a.Name != "" {
			aat = fmt.Sprintf("%s.attributes[%s]", at, a.Name)
		}

		required(p, aat+".name", a.Name)
		required(p, aat+".goField", a.GoField)

		dup(p, seenNames, a.Name, aat+".name", "identity attribute name")
		dup(p, seenFields, a.GoField, aat+".goField", "identity model field")

		// The framework's identityschema has scalars and a list of scalars, and nothing
		// else. A nested identity is not expressible there, so refusing it here names the
		// attribute instead of producing a type that does not exist.
		switch {
		case a.Kind == "":
			p.add(aat+".kind", "is required")
		case a.Kind.IsNested(), a.Kind == KindMap:
			p.add(aat+".kind",
				"%q has no identityschema counterpart; an identity is scalars, or a list of them",
				a.Kind)
		}

		// Exactly one of the two flags. Neither means the attribute can never be supplied;
		// both is a contradiction the framework rejects at runtime, which is a worse place
		// to find out.
		switch {
		case !a.RequiredForImport && !a.OptionalForImport:
			p.add(aat, "must set either requiredForImport or optionalForImport")
		case a.RequiredForImport && a.OptionalForImport:
			p.add(aat, "sets both requiredForImport and optionalForImport, which are exclusive")
		}

		if a.FromAttribute == "" {
			p.add(aat+".fromAttribute",
				"is required: generated code copies the identity value out of the resource model")
			continue
		}
		if !hasAttributeNamed(r.Schema.Attributes, a.FromAttribute) {
			p.add(aat+".fromAttribute",
				"names attribute %q, which this resource does not declare", a.FromAttribute)
		}
	}
}

// validate checks a list facet.
//
// The first refusal is the one that matters: the framework raises an error diagnostic for a
// ListResult with no identity, so a list resource on a resource that declares none cannot
// work. Catching it here names both halves; at runtime it surfaces as a query that fails
// with nothing to point at.
func (lf ListFacet) validate(r Resource, at string, p *problems) {
	if r.Identity == nil {
		p.add(at,
			"requires the resource to declare an identity: ListResult.Identity is mandatory, "+
				"so a list resource without one cannot produce a usable result")
		return
	}

	required(p, at+".goTypeName", lf.GoTypeName)
	required(p, at+".service.importPath", lf.Service.ImportPath)
	required(p, at+".service.typeName", lf.Service.TypeName)
	required(p, at+".service.accessor", lf.Service.Accessor)
	required(p, at+".elementType", lf.ElementType)
	required(p, at+".response.type", lf.Response.Type)

	if lf.Read == nil {
		p.add(at+".read",
			"is required: a list resource's whole requirement of the API is a collection read")
	} else {
		lf.Read.validate(at+".read", p)
	}

	if len(lf.IdentityFrom) == 0 {
		p.add(at+".identityFrom",
			"is required: every result must carry an identity, so each identity field needs a "+
				"source on the element")
		return
	}

	// Every identity field must be filled. A partially-populated identity is worse than
	// none: Terraform would record an address that does not resolve.
	want := map[string]bool{}
	for _, ia := range r.Identity.Attributes {
		want[ia.GoField] = true
	}

	seen := map[string]bool{}

	for i, m := range lf.IdentityFrom {
		mat := fmt.Sprintf("%s.identityFrom[%d]", at, i)

		required(p, mat+".goField", m.GoField)
		required(p, mat+".fromSdkField", m.FromSDKField)

		if m.GoField == "" {
			continue
		}
		if !want[m.GoField] {
			p.add(mat+".goField",
				"names %q, which the resource's identity does not declare", m.GoField)
			continue
		}
		dup(p, seen, m.GoField, mat+".goField", "identity mapping")
	}

	for _, ia := range r.Identity.Attributes {
		if !seen[ia.GoField] {
			p.add(at+".identityFrom",
				"does not fill identity field %q; a partly-filled identity records an address "+
					"that does not resolve", ia.GoField)
		}
	}

	lf.validateFilterSchema(at, p)
}

// validateFilterSchema checks a list facet's filter config schema.
//
// BlockList's refusal of Computed is what keeps this filter-only structurally. The
// cross-check on the read call's arguments is the part a per-attribute walk cannot see:
// an ArgConfigField naming a model field no filter attribute has would generate a read
// that does not compile.
func (lf ListFacet) validateFilterSchema(at string, p *problems) {
	filterFields := map[string]bool{}

	if lf.Schema != nil {
		if len(lf.Schema.Attributes) == 0 {
			p.add(at+".schema.attributes",
				"is declared and empty; a facet with no filters should omit the schema entirely")
		}

		seenNames := map[string]bool{}
		seenFields := map[string]bool{}

		for i, a := range lf.Schema.Attributes {
			aat := fmt.Sprintf("%s.schema.attributes[%d]", at, i)
			if a.Name != "" {
				aat = fmt.Sprintf("%s.schema.attributes[%s]", at, a.Name)
			}

			if a.Drop {
				continue
			}

			a.validate(aat, p)
			a.validateForKind(BlockList, aat, p)

			dup(p, seenNames, a.Name, aat+".name", "filter attribute name")
			dup(p, seenFields, a.GoField, aat+".goField", "filter model field")

			filterFields[a.GoField] = true
		}
	}

	if lf.Read == nil {
		return
	}

	for i, arg := range lf.Read.Args {
		if arg.Kind != ArgConfigField || arg.Field == "" {
			continue
		}
		if !filterFields[arg.Field] {
			p.add(fmt.Sprintf("%s.read.args[%d].field", at, i),
				"names model field %q, which no filter attribute declares", arg.Field)
		}
	}
}

// validate checks one cross-attribute rule.
//
// Every named attribute must exist, because the rule is rendered as path.MatchRoot on that
// name: a name that does not resolve is not a compile error -- the framework accepts any path
// expression -- it is a rule that silently never fires. That makes it worse than a typo the
// compiler would catch, and the reason to check it here.
func (cv ConfigValidator) validate(r Resource, at string, p *problems) {
	switch cv.Kind {
	case ConfigConflicting, ConfigAtLeastOneOf, ConfigExactlyOneOf, ConfigRequiredTogether:
	case "":
		p.add(at+".kind", "is required")
	default:
		p.add(at+".kind", "%q is not a known cross-attribute rule", cv.Kind)
	}

	if len(cv.Attributes) < 2 {
		p.add(at+".attributes",
			"a cross-attribute rule relates at least two attributes; over one it is a "+
				"per-attribute validator written in the wrong place")

		return
	}

	seen := map[string]bool{}

	for i, name := range cv.Attributes {
		aat := fmt.Sprintf("%s.attributes[%d]", at, i)

		if name == "" {
			p.add(aat, "is empty")
			continue
		}
		if !hasAttributeNamed(r.Schema.Attributes, name) {
			p.add(aat,
				"names attribute %q, which this resource does not declare, so the rule would "+
					"never fire", name)
			continue
		}
		dup(p, seen, name, aat, "attribute in this rule")

		cv.validateMember(r, name, aat, p)
	}
}

// validateMember refuses a member whose configurability makes the rule degenerate.
//
// These rules are about *configuration* -- resourcevalidator reads config values, not state --
// so what a member can hold in configuration decides whether the rule means anything. Both
// cases below compile, both produce a rule that is not the rule the blueprint states, and
// neither announces itself: one rule silently relates fewer attributes than it names, the
// other is satisfied by every configuration ever written.
func (cv ConfigValidator) validateMember(r Resource, name, at string, p *problems) {
	src, ok := findAttributeNamed(r.Schema.Attributes, name)
	if !ok {
		return
	}

	// Computed-only: the practitioner cannot set it, so its config value is always null and it
	// can never participate. The rule then relates one fewer attribute than it claims to.
	if src.ComputedOptionalRequired == Computed {
		p.add(at,
			"names attribute %q, which is computed and so cannot be set in configuration; a "+
				"cross-attribute rule reads config values, so this member can never "+
				"participate", name)

		return
	}

	// Required: always set, so "at least one is set" and "exactly one is set" are decided
	// before the practitioner writes anything.
	if src.ComputedOptionalRequired == Required {
		switch cv.Kind {
		case ConfigAtLeastOneOf:
			p.add(at,
				"names required attribute %q, so at-least-one is satisfied by every "+
					"configuration and the rule would never fire", name)
		case ConfigExactlyOneOf:
			p.add(at,
				"names required attribute %q, which is always set, so exactly-one does not "+
					"choose between the members -- it forbids all the others, which is what "+
					"conflicting says plainly", name)
		case ConfigConflicting, ConfigRequiredTogether:
			// Both stay meaningful: conflicting over a required member forbids the others,
			// and required-together over one is what makes the rest required too.
		}
	}
}

// findAttributeNamed returns a declared, undropped attribute by name.
func findAttributeNamed(attrs []Attribute, name string) (Attribute, bool) {
	for _, a := range attrs {
		if a.Name == name && !a.Drop {
			return a, true
		}
	}

	return Attribute{}, false
}

// hasAttributeNamed reports whether a schema declares an attribute by that name, ignoring
// dropped ones -- a dropped attribute is not in the generated model, so an identity cannot
// read from it.
func hasAttributeNamed(attrs []Attribute, name string) bool {
	for _, a := range attrs {
		if a.Name == name && !a.Drop {
			return true
		}
	}
	return false
}

func (b DataSourceBinding) validate(at string, p *problems) {
	required(p, at+".service.importPath", b.Service.ImportPath)
	required(p, at+".service.typeName", b.Service.TypeName)
	required(p, at+".service.accessor", b.Service.Accessor)

	required(p, at+".response.type", b.Response.Type)

	switch b.Response.AccessStyle {
	case AccessStructField:
	case AccessMethod:
		p.add(
			at+".response.accessStyle",
			"%q is reserved but not yet implemented by the emitter",
			b.Response.AccessStyle,
		)
	default:
		p.add(at+".response.accessStyle", "%q is not a known access style", b.Response.AccessStyle)
	}

	if b.Read == nil {
		p.add(at+".read", "is required: a data source with no read operation has nothing to do")
		return
	}

	b.Read.validate(at+".read", p)
}

func (o Operation) validate(at string, p *problems) {
	switch o.Style {
	case CallStyleMethod:
		required(p, at+".method", o.Method)
	case CallStyleFluent:
		p.add(at+".style", "%q is reserved but not yet implemented by the emitter", o.Style)
	default:
		p.add(at+".style", "%q is not a known call style", o.Style)
	}

	switch o.Return {
	case ReturnResultTransportError, ReturnTransportError, ReturnResultError, ReturnError:
	case "":
		p.add(at+".return", "is required")
	default:
		p.add(at+".return", "%q is not a known return arity", o.Return)
	}

	// The arity and the result type have to agree, or the generated assignment
	// has the wrong number of values on one side.
	if o.Return.HasResult() && o.ResultType == "" {
		p.add(at+".resultType", "is required for return arity %q", o.Return)
	}
	if !o.Return.HasResult() && o.ResultType != "" {
		p.add(at+".resultType", "is set but return arity %q yields no result", o.Return)
	}

	for i, a := range o.Args {
		a.validate(fmt.Sprintf("%s.args[%d]", at, i), p)
	}
}

func (a Argument) validate(at string, p *problems) {
	switch a.Kind {
	case ArgContext, ArgBody:
	case ArgStateField, ArgPlanField, ArgConfigField:
		if a.Field == "" && a.Expr == "" {
			p.add(at, "kind %q needs either field or expr", a.Kind)
		}
	case ArgLiteral:
		if a.Expr == "" {
			p.add(at+".expr", "is required for a literal argument")
		}
	case "":
		p.add(at+".kind", "is required")
	default:
		p.add(at+".kind", "%q is not a known argument kind", a.Kind)
	}
}

func required(p *problems, path, value string) {
	if strings.TrimSpace(value) == "" {
		p.add(path, "is required")
	}
}
