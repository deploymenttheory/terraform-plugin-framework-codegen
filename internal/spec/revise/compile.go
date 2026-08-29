package revise

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/correction"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// category says what compiling one confirmed observation amounted to.
type category int

const (
	// catCompiled: the observation became correction operations.
	catCompiled category = iota
	// catAlreadyStated: the revised document already states the fact, so
	// there is nothing to correct — the property re-auditing converges on.
	catAlreadyStated
	// catNoForm: no correction form exists yet for this kind; inventing an
	// x-tfpfgen-* key is an owner decision, not a compiler's.
	catNoForm
	// catUnplaceable: the document has no place for the fact — the entity,
	// operation or attribute the observation names cannot be found.
	catUnplaceable
	// catVetoed: a sibling observation blocks this one — a confirmed
	// derivedDefault vetoes a serverDefault on the same attribute.
	catVetoed
)

// compiled is one observation's compilation: either operations plus the
// justification they carry, or a category and reason explaining why not.
type compiled struct {
	operations    []correction.Operation
	justification string
	category      category
	reason        string
}

func stated(reason string) compiled      { return compiled{category: catAlreadyStated, reason: reason} }
func unplaceable(reason string) compiled { return compiled{category: catUnplaceable, reason: reason} }

// compiler compiles confirmed observations against the evolving revised
// state, so two corrections touching the same schema see each other.
type compiler struct {
	entities map[string]specmodel.Classification
	// state is the revised document with every correction compiled so far
	// already applied.
	state []byte
	// vetoes marks entity/attribute pairs where a confirmed derivedDefault
	// observation blocks a static serverDefault correction.
	vetoes map[[2]string]bool
	// restated maps each document pointer an accepted correction wrote to
	// the evidence that wrote it, so an observation recompiling its own
	// accepted correction — its value moved — is told from another
	// entity's observation landing on the same shared site.
	restated map[string]string
	// defaulted maps each server-default site this run has already
	// compiled a value for to that value, so a second entity observing a
	// different default on a shared site in the same run proposes nothing.
	defaulted map[string]any
	// enumAccepted maps each enum site to the values some entity's
	// observation accepted there, this run's or an accepted correction's,
	// so one entity's rejection of a value never removes what another
	// entity sharing the site sends successfully.
	enumAccepted map[string]map[string]bool
	// variants gathers, per (entity, gate field), the validWhen edges the
	// run confirmed: gate value -> the subject fields valid under it. A
	// validConfiguration correction reads it to assemble its per-value field
	// sets, since one validConfiguration observation carries only the gate
	// values, not which fields each admits.
	variants map[[2]string]map[string][]string
}

// compile turns one confirmed observation into correction operations, or
// says why it produced none. Deterministic: the same observation against the
// same state always compiles identically.
func (c *compiler) compile(o observe.Observation) (compiled, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(c.state, &root); err != nil || root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return compiled{}, fmt.Errorf("the revised state is not usable YAML: %v", err)
	}
	loc := &locator{top: root.Content[0]}

	cls, ok := c.entities[o.Entity]
	if !ok {
		return unplaceable(fmt.Sprintf("entity %q is not classified in the revised document", o.Entity)), nil
	}

	rule, ok := compileRules[o.Kind]
	if !ok {
		// observe.Read validates kinds against the closed set, so reaching
		// here means the sets have drifted — the failure mode an evidence
		// store must surface, not drop.
		return compiled{}, fmt.Errorf("observation %s: kind %q has no compilation rule", o.ID, o.Kind)
	}
	return rule(c, loc, cls, o)
}

// compileRule turns one confirmed observation into correction operations.
// Every rule takes the same arguments so that one map can hold them all.
type compileRule func(*compiler, *locator, specmodel.Classification, observe.Observation) (compiled, error)

// compileRules is the single answer to which observation kinds compile.
// CompilableKinds reads these keys, so the vocabulary a human meets at
// `tfpfgen config validate` is the set of rules that exist rather than a
// second list beside them: a kind reaches both at once or neither.
var compileRules = map[observe.Kind]compileRule{
	observe.KindWritable:      alwaysCompiles((*compiler).writable),
	observe.KindRequiredByAPI: alwaysCompiles((*compiler).requiredByAPI),
	observe.KindRequiredWhen:  alwaysCompiles((*compiler).requiredWhen),
	observe.KindServerDefault: alwaysCompiles((*compiler).serverDefault),
	observe.KindUpdateStyle:   alwaysCompiles((*compiler).updateStyle),

	observe.KindDeleteNotFoundOK: alwaysCompiles((*compiler).deleteNotFoundOK),
	observe.KindReadAfterWrite:   alwaysCompiles((*compiler).readAfterWrite),

	observe.KindUndocumentedFieldInSpec: alwaysCompiles((*compiler).undocumentedField),
	observe.KindNormalisation:           alwaysCompiles((*compiler).normalisation),

	observe.KindValidWhen:          alwaysCompiles((*compiler).validWhen),
	observe.KindDependsOn:          alwaysCompiles((*compiler).dependsOn),
	observe.KindMutuallyExclusive:  alwaysCompiles((*compiler).mutuallyExclusive),
	observe.KindValidConfiguration: alwaysCompiles((*compiler).validConfiguration),

	observe.KindValues:             (*compiler).values,
	observe.KindListWrapper:        (*compiler).listWrapper,
	observe.KindListPagination:     (*compiler).listPagination,
	observe.KindIdentifierProperty: (*compiler).identifierProperty,

	observe.KindImmutable: propertyFlagRule(specmodel.ExtImmutable,
		"the live API refuses to change the value after create"),
	observe.KindVolatile: propertyFlagRule(specmodel.ExtVolatile,
		"the value differs between two identical reads"),
	observe.KindServerForced: propertyFlagRule(specmodel.ExtServerForced,
		"the server substitutes its own value regardless of what was sent"),
	observe.KindIgnoredOnUpdate: propertyFlagRule(specmodel.ExtIgnoredOnUpdate,
		"updates accept a new value with a success status and do not apply it"),

	// A derived default is real and has no correction form: the value the
	// server substitutes depends on the request, so no static declaration
	// states it. The kind is compilable so a reviewer is told that.
	observe.KindDerivedDefault: func(*compiler, *locator, specmodel.Classification, observe.Observation) (compiled, error) {
		return compiled{category: catNoForm, reason: noFormReason}, nil
	},
}

// alwaysCompiles adapts a rule that cannot fail to the common signature, so
// a rule declares an error return only when it has one to give.
func alwaysCompiles(f func(*compiler, *locator, specmodel.Classification, observe.Observation) compiled) compileRule {
	return func(c *compiler, loc *locator, cls specmodel.Classification, o observe.Observation) (compiled, error) {
		return f(c, loc, cls, o), nil
	}
}

// propertyFlagRule builds the rule for a kind whose whole correction is one
// extension key on the observed property, carrying the finding that key
// records.
func propertyFlagRule(key, finding string) compileRule {
	return func(c *compiler, loc *locator, cls specmodel.Classification, o observe.Observation) (compiled, error) {
		return c.propertyFlag(loc, cls, o, key, finding), nil
	}
}

// property finds the attribute the observation is about, create request
// schema first, read response schema second — the same order downstream
// derivation folds the two views in.
func (c *compiler) property(loc *locator, cls specmodel.Classification, o observe.Observation) (propertyLocation, compiled, bool) {
	if o.Attribute == "" {
		return propertyLocation{}, unplaceable(fmt.Sprintf("a %s observation names no attribute", o.Kind)), false
	}
	if node, pointer, ok := loc.requestSchema(cls.Create); ok {
		if site, ok := loc.findPath(node, pointer, o.Attribute); ok {
			return site, compiled{}, true
		}
	}
	if node, pointer, ok := loc.responseSchema(cls.Read); ok {
		if site, ok := loc.findPath(node, pointer, o.Attribute); ok {
			return site, compiled{}, true
		}
	}
	return propertyLocation{}, unplaceable(fmt.Sprintf(
		"no schema of %s declares a property %q", o.Entity, o.Attribute)), false
}

// writable compiles writable=false into readOnly — the one thing standard
// OpenAPI can say about a value the API accepts and never stores.
func (c *compiler) writable(loc *locator, cls specmodel.Classification, o observe.Observation) compiled {
	if o.Value == true {
		return stated("the document already reads as writable; there is nothing to correct")
	}
	site, why, ok := c.property(loc, cls, o)
	if !ok {
		return why
	}
	// writeOnly must sit on a concrete schema: beside a $ref it is ignored,
	// so the declaration the reference resolves to is the correctable spot.
	//
	// writeOnly rather than readOnly: the wire shows the value accepted and
	// absent from the answer, which is what a property the API stores under
	// another name looks like as much as one it discards, and readOnly would
	// take a property the API requires away from the practitioner. Declared
	// write-only, it stays configurable and the state keeps the configured
	// value.
	target, targetPtr, ok := loc.followSchemaRefs(site.property, site.propPtr)
	if !ok {
		return unplaceable(fmt.Sprintf("the schema of %s.%s resolves outside the document", o.Entity, o.Attribute))
	}
	if loc.readOnlyDeclared(target, targetPtr) {
		return stated("the document already declares the property readOnly")
	}
	if loc.flagDeclared(target, targetPtr, "writeOnly") {
		return stated("the document already declares the property writeOnly")
	}
	return compiled{
		operations: []correction.Operation{{Op: "add", Path: targetPtr + "/writeOnly", Value: true}},
		justification: fmt.Sprintf("the audit confirmed a writable observation on %s.%s: "+
			"the live API accepts the value and never returns it, so the property is writeOnly", o.Entity, o.Attribute),
	}
}

// propertyFlag compiles the boolean per-attribute kinds that become one
// x-tfpfgen-* true annotation on the property.
func (c *compiler) propertyFlag(loc *locator, cls specmodel.Classification, o observe.Observation, key, finding string) compiled {
	if o.Value != true {
		return stated("the observation confirms the document's own reading; there is nothing to correct")
	}
	site, why, ok := c.property(loc, cls, o)
	if !ok {
		return why
	}
	if extension := loc.extensionNode(site.property, site.propPtr, key); extension != nil && extension.Value == "true" {
		return stated(fmt.Sprintf("the document already declares %s", key))
	}
	return compiled{
		operations: []correction.Operation{{Op: "add", Path: site.propPtr + "/" + key, Value: true}},
		justification: fmt.Sprintf("the audit confirmed %s %s observation on %s.%s: %s (%s)",
			article(o.Kind), o.Kind, o.Entity, o.Attribute, finding, key),
	}
}

// article picks the indefinite article a kind's name takes, so justification
// prose reads as written rather than generated.
func article(kind observe.Kind) string {
	switch kind[0] {
	case 'a', 'e', 'i', 'o', 'u':
		return "an"
	default:
		return "a"
	}
}

// requiredByAPI compiles into the declaring schema's required list.
func (c *compiler) requiredByAPI(loc *locator, cls specmodel.Classification, o observe.Observation) compiled {
	if o.Value != true {
		return stated("the observation confirms the document's own reading; there is nothing to correct")
	}
	site, why, ok := c.property(loc, cls, o)
	if !ok {
		return why
	}
	// The check spans the whole entity schema — the attribute may already be
	// required by another allOf branch than the one declaring it.
	if node, pointer, ok := loc.requestSchema(cls.Create); ok && loc.requiredHas(node, pointer, o.Attribute) {
		return stated("the document already requires the property")
	}
	if loc.requiredHas(site.declaration, site.declarationPointer, o.Attribute) {
		return stated("the document already requires the property")
	}

	operation := correction.Operation{Op: "add", Path: site.declarationPointer + "/required", Value: []any{o.Attribute}}
	if request := mapValue(site.declaration, "required"); request != nil {
		operation = correction.Operation{Op: "add", Path: site.declarationPointer + "/required/-", Value: o.Attribute}
	}
	return compiled{
		operations: []correction.Operation{operation},
		justification: fmt.Sprintf("the audit confirmed a requiredByAPI observation on %s.%s: "+
			"a create omitting the property is rejected whatever the document declares", o.Entity, o.Attribute),
	}
}

// requiredWhen compiles a value-conditional requirement into
// x-tfpfgen-required-when on the property.
func (c *compiler) requiredWhen(loc *locator, cls specmodel.Classification, o observe.Observation) compiled {
	if o.Value != true {
		return stated("the observation confirms the document's own reading; there is nothing to correct")
	}
	site, why, ok := c.property(loc, cls, o)
	if !ok {
		return why
	}
	equals := literalSpelling(o.Condition.Equals)
	if extension := loc.extensionNode(site.property, site.propPtr, specmodel.ExtRequiredWhen); extension != nil {
		if p, e := mapValue(extension, "property"), mapValue(extension, "equals"); p != nil && e != nil &&
			p.Value == o.Condition.Attribute && e.Value == equals {
			return stated("the document already declares this conditional requirement")
		}
	}
	return compiled{
		operations: []correction.Operation{{
			Op:   "add",
			Path: site.propPtr + "/" + specmodel.ExtRequiredWhen,
			Value: map[string]string{
				"property": o.Condition.Attribute,
				"equals":   equals,
			},
		}},
		justification: fmt.Sprintf("the audit confirmed a requiredWhen observation on %s.%s: "+
			"the property is required when %s equals %q (%s)",
			o.Entity, o.Attribute, o.Condition.Attribute, equals, specmodel.ExtRequiredWhen),
	}
}

// serverDefault compiles the constant an omitting create stores into the
// schema's default — unless a confirmed derivedDefault vetoes it.
func (c *compiler) serverDefault(loc *locator, cls specmodel.Classification, o observe.Observation) compiled {
	if c.vetoes[[2]string{o.Entity, o.Attribute}] {
		return compiled{category: catVetoed, reason: "a confirmed derivedDefault observation on the same " +
			"attribute vetoes a static default: the omitted-attribute value is computed, not constant"}
	}
	site, why, ok := c.property(loc, cls, o)
	if !ok {
		return why
	}
	// The reading lands on the property, never on the schema its $ref
	// resolves to, and never in OpenAPI's own `default`.
	//
	// The resolved schema is often a shared scalar type: the live run put one
	// account group's identifier into components/schemas/…AccountGroupId,
	// restating a single object's id as the default for every use of that type
	// in the document. And `default` says what the document declares, which
	// nothing in the generation path reads — the attribute's presence is
	// decided from writability and from this extension.
	path := site.propPtr + "/" + specmodel.ExtServerDefault
	// A different value another entity observed on the same site, whether
	// accepted earlier or compiled earlier in this run: the property's
	// schema is shared between entities and each stores its own default —
	// eight test types sharing one schema whose type property defaults to
	// each one's own kind. That is a fact about each entity, and the
	// document has one site for the property, so no correction states it;
	// proposing one would only replace the other entity's value and be
	// replaced in turn. A value this observation's own correction wrote, or
	// the document's own wrong reading, is still corrected.
	shared := "another entity's value on this shared property; a default that differs by entity has no site in the document"
	if by, ok := c.restated[path]; ok && by != evidenceReference(o) {
		return stated(fmt.Sprintf("the document already declares %s with %s", specmodel.ExtServerDefault, shared))
	}
	if earlier, ok := c.defaulted[path]; ok && !jsonEqual(earlier, o.Value) {
		return stated(fmt.Sprintf("this run already compiled %s with %s", specmodel.ExtServerDefault, shared))
	}
	if extension := loc.extensionNode(site.property, site.propPtr, specmodel.ExtServerDefault); extension != nil {
		var current any
		if extension.Decode(&current) == nil && jsonEqual(current, o.Value) {
			return stated(fmt.Sprintf("the document already declares %s", specmodel.ExtServerDefault))
		}
	}
	if c.defaulted == nil {
		c.defaulted = map[string]any{}
	}
	c.defaulted[path] = o.Value
	return compiled{
		operations: []correction.Operation{{Op: "add", Path: path, Value: o.Value}},
		justification: fmt.Sprintf("the audit confirmed a serverDefault observation on %s.%s: omitting the "+
			"property stores %s, so the generated attribute is Optional and Computed (%s)",
			o.Entity, o.Attribute, literalSpelling(o.Value), specmodel.ExtServerDefault),
	}
}

// noFormReason is the report line for a kind no correction form exists for.
const noFormReason = "no correction form exists yet; adding an x-tfpfgen-* key is an owner decision"

// undocumentedField compiles an undocumentedFieldInSpec observation into an
// operation adding the property, with its observed JSON type, to the entity
// schema's properties. The site is the first properties mapping of the read
// response schema — the view the field was observed in — walking $refs and
// allOf branches in the same order findProperty reads them; the request
// schema is the fallback for an entity whose read declares no properties.
func (c *compiler) undocumentedField(loc *locator, cls specmodel.Classification, o observe.Observation) compiled {
	if o.Attribute == "" {
		return unplaceable(fmt.Sprintf("a %s observation names no attribute", o.Kind))
	}
	if _, _, found := c.property(loc, cls, o); found {
		return stated("the document already declares the property")
	}

	sitePtr, ok := propertiesSite(loc, cls.Read, cls.Create)
	if !ok {
		return unplaceable(fmt.Sprintf("no schema of %s declares a properties mapping to add %q to", o.Entity, o.Attribute))
	}

	jsonType, _ := o.Value.(string)
	value := map[string]any{"type": jsonType}
	if jsonType == "array" {
		// 3.0 requires items on an array schema; nothing more is known.
		value["items"] = map[string]any{}
	}
	return compiled{
		operations: []correction.Operation{{
			Op: "add", Path: sitePtr + "/properties/" + escapeToken(o.Attribute), Value: value,
		}},
		justification: fmt.Sprintf("the audit confirmed an undocumentedFieldInSpec observation on %s.%s: "+
			"read responses carry the field with the stable JSON type %s, and no schema declares it", o.Entity, o.Attribute, jsonType),
	}
}

// propertiesSite finds the pointer of the first schema node carrying a
// properties mapping: the read operation's response schema first, the
// create operation's request schema as the fallback.
func propertiesSite(loc *locator, read, create *specmodel.OperationReference) (string, bool) {
	find := func(node *yaml.Node, pointer string) (string, bool) {
		var sitePtr string
		loc.walkSchemaWithPtr(node, pointer, map[string]bool{}, func(n *yaml.Node, p string) bool {
			if properties := mapValue(n, "properties"); properties != nil && properties.Kind == yaml.MappingNode {
				sitePtr = p
				return true
			}
			return false
		})
		return sitePtr, sitePtr != ""
	}
	if node, pointer, ok := loc.responseSchema(read); ok {
		if sitePtr, found := find(node, pointer); found {
			return sitePtr, true
		}
	}
	if node, pointer, ok := loc.requestSchema(create); ok {
		if sitePtr, found := find(node, pointer); found {
			return sitePtr, true
		}
	}
	return "", false
}

// updateStyle compiles onto the update operation, where derivation reads it.
func (c *compiler) updateStyle(loc *locator, cls specmodel.Classification, o observe.Observation) compiled {
	if cls.Update == nil {
		return unplaceable(fmt.Sprintf("entity %s has no update operation to annotate", o.Entity))
	}
	style, _ := o.Value.(string)
	opNode := loc.nodeAt(opPointer(cls.Update))
	if extension := mapValue(opNode, specmodel.ExtUpdateStyle); extension != nil && extension.Value == style {
		return stated("the document already declares this update style")
	}
	return compiled{
		operations: []correction.Operation{{
			Op: "add", Path: opPointer(cls.Update) + "/" + specmodel.ExtUpdateStyle, Value: style,
		}},
		justification: fmt.Sprintf("the audit confirmed an updateStyle observation on %s: "+
			"the update operation treats omitted fields as %s (%s)", o.Entity, style, specmodel.ExtUpdateStyle),
	}
}

// deleteNotFoundOK compiles onto the delete operation.
func (c *compiler) deleteNotFoundOK(loc *locator, cls specmodel.Classification, o observe.Observation) compiled {
	if o.Value != true {
		return stated("the observation confirms the document's own reading; there is nothing to correct")
	}
	if cls.Delete == nil {
		return unplaceable(fmt.Sprintf("entity %s has no delete operation to annotate", o.Entity))
	}
	opNode := loc.nodeAt(opPointer(cls.Delete))
	if extension := mapValue(opNode, specmodel.ExtDeleteNotFoundOK); extension != nil && extension.Value == "true" {
		return stated(fmt.Sprintf("the document already declares %s", specmodel.ExtDeleteNotFoundOK))
	}
	return compiled{
		operations: []correction.Operation{{
			Op: "add", Path: opPointer(cls.Delete) + "/" + specmodel.ExtDeleteNotFoundOK, Value: true,
		}},
		justification: fmt.Sprintf("the audit confirmed a deleteNotFoundOK observation on %s: "+
			"deleting an already-deleted object answers 404, which delete logic should treat as success (%s)",
			o.Entity, specmodel.ExtDeleteNotFoundOK),
	}
}

// readAfterWrite compiles the measured read lag onto the read operation —
// unless the lag is zero, or any of the entity's operations already declares
// a lag at least as long, since derivation takes the maximum across them.
func (c *compiler) readAfterWrite(loc *locator, cls specmodel.Classification, o observe.Observation) compiled {
	if cls.Read == nil {
		return unplaceable(fmt.Sprintf("entity %s has no read operation to annotate", o.Entity))
	}
	lag, _ := o.Value.(string)
	observed, err := time.ParseDuration(lag)
	if err != nil {
		// Validate refused this shape on read; reaching here is drift.
		return unplaceable(fmt.Sprintf("the observed lag %q is not a duration", lag))
	}
	// A zero lag is a finding, not a correction: the read never lagged the
	// write. Compiling it produces an eventual-consistency annotation of "0s",
	// which changes no generated behaviour and costs a human a decision on a
	// no-op. The audit is right to record it; revision is right to stop here.
	if observed <= 0 {
		return stated(fmt.Sprintf(
			"the measured read-after-write lag is %s: reads never lagged a write, so there is nothing to declare", lag))
	}
	for _, operation := range []*specmodel.OperationReference{cls.Create, cls.Read, cls.Update, cls.Delete} {
		if operation == nil {
			continue
		}
		if extension := mapValue(loc.nodeAt(opPointer(operation)), specmodel.ExtReadAfterWrite); extension != nil {
			if existing, err := time.ParseDuration(extension.Value); err == nil && existing >= observed {
				return stated("the document already declares a read-after-write lag at least this long")
			}
		}
	}
	return compiled{
		operations: []correction.Operation{{
			Op: "add", Path: opPointer(cls.Read) + "/" + specmodel.ExtReadAfterWrite, Value: lag,
		}},
		justification: fmt.Sprintf("the audit confirmed a readAfterWrite observation on %s: "+
			"a read may lag a write by up to %s (%s)", o.Entity, lag, specmodel.ExtReadAfterWrite),
	}
}

// listWrapper compiles the observed collection-response wrapping onto
// the entity's list operation. It is annotated beside the operation rather
// than left to the document's own list response schema because that schema
// is exactly what the observation was recorded to contradict: every API
// spells its envelope differently and the document routinely gets it wrong,
// so derivation must be able to read what the wire actually carried.
func (c *compiler) listWrapper(loc *locator, cls specmodel.Classification, o observe.Observation) (compiled, error) {
	var wrapper observe.ListWrapper
	raw, err := json.Marshal(o.Value)
	if err == nil {
		err = json.Unmarshal(raw, &wrapper)
	}
	if err != nil {
		return compiled{}, fmt.Errorf("observation %s: its value is not a list-wrapper record: %w", o.ID, err)
	}
	if cls.List == nil {
		return unplaceable(fmt.Sprintf("entity %s has no list operation to annotate", o.Entity)), nil
	}
	// Everything the extension will carry is checked here, against the
	// vocabulary specmodel enforces at load: a correction that compiles into
	// a document the loader then refuses would break every later stage, and
	// the observation store is hand-editable enough for that to happen.
	switch {
	case wrapper.Wrapped && wrapper.Key == "":
		return unplaceable(fmt.Sprintf(
			"the observed list response on %s is wrapped but names no key", o.Entity)), nil
	case !wrapper.Wrapped && wrapper.Key != "":
		return unplaceable(fmt.Sprintf("the observed list response on %s is unwrapped but names the "+
			"key %q, so what it found cannot be stated", o.Entity, wrapper.Key)), nil
	}

	// Sorted map keys make the correction's JSON byte-stable, and the key is
	// carried only where it means something.
	value := map[string]string{"wrapped": strconv.FormatBool(wrapper.Wrapped)}
	finding := "the live list response is a bare item array"
	if wrapper.Wrapped {
		value["key"] = wrapper.Key
		finding = fmt.Sprintf("the live list response wraps its item array under %q", wrapper.Key)
	}

	opPtr := opPointer(cls.List)
	if extension := mapValue(loc.nodeAt(opPtr), specmodel.ExtListWrapper); extension != nil && wrapperStated(extension, value) {
		return stated("the document already declares this list wrapping"), nil
	}
	return compiled{
		operations: []correction.Operation{{
			Op: "add", Path: opPtr + "/" + specmodel.ExtListWrapper, Value: value,
		}},
		justification: fmt.Sprintf("the audit confirmed a listWrapper observation on %s: %s (%s)",
			o.Entity, finding, specmodel.ExtListWrapper),
	}, nil
}

// listPagination annotates the entity's list operation with the pagination
// style its live response advertised.
func (c *compiler) listPagination(loc *locator, cls specmodel.Classification, o observe.Observation) (compiled, error) {
	style, _ := o.Value.(string)
	if style == "" {
		style = "none"
	}
	if cls.List == nil {
		return unplaceable(fmt.Sprintf("entity %s has no list operation to annotate", o.Entity)), nil
	}
	if !specmodel.ValidListPagination(style) {
		return unplaceable(fmt.Sprintf("the observed list response on %s names the pagination style %q, "+
			"which is not one of cursor, offset, page, none", o.Entity, style)), nil
	}

	opPtr := opPointer(cls.List)
	if extension := mapValue(loc.nodeAt(opPtr), specmodel.ExtListPagination); extension != nil {
		if extension.Value == style {
			return stated("the document already declares this pagination style"), nil
		}
	}
	return compiled{
		operations: []correction.Operation{{
			Op: "add", Path: opPtr + "/" + specmodel.ExtListPagination, Value: style,
		}},
		justification: fmt.Sprintf("the audit confirmed a listPagination observation on %s: "+
			"the live list response advertises %s pagination (%s)",
			o.Entity, style, specmodel.ExtListPagination),
	}, nil
}

// identifierProperty annotates the read operation with the response property
// carrying the value its item path addresses the object by.
//
// It goes on the read because that is the operation whose path parameter the
// property answers: derivation reads the two together, and the correspondence
// exists nowhere else in the document.
func (c *compiler) identifierProperty(loc *locator, cls specmodel.Classification, o observe.Observation) (compiled, error) {
	property, ok := o.Value.(string)
	if !ok || property == "" {
		return compiled{}, fmt.Errorf("observation %s: its value is not a property name", o.ID)
	}
	if cls.Read == nil {
		return unplaceable(fmt.Sprintf("entity %s has no read operation to annotate", o.Entity)), nil
	}
	// The property must exist on what the read answers, or the correction
	// would name a field nothing can read.
	node, pointer, ok := loc.responseSchema(cls.Read)
	if !ok {
		return unplaceable(fmt.Sprintf("entity %s has no read response schema to check %q against",
			o.Entity, property)), nil
	}
	if _, found := loc.findProperty(node, pointer, property); !found {
		return unplaceable(fmt.Sprintf("the read response of %s declares no %q to identify it by",
			o.Entity, property)), nil
	}

	opPtr := opPointer(cls.Read)
	if extension := mapValue(loc.nodeAt(opPtr), specmodel.ExtIdentifierProperty); extension != nil && extension.Value == property {
		return stated("the document already names this identifying property"), nil
	}
	return compiled{
		operations: []correction.Operation{{
			Op: "add", Path: opPtr + "/" + specmodel.ExtIdentifierProperty, Value: property,
		}},
		justification: fmt.Sprintf("the audit confirmed an identifierProperty observation on %s: "+
			"the live response carries its id as %q, which its item path does not name (%s)",
			o.Entity, property, specmodel.ExtIdentifierProperty),
	}, nil
}

// wrapperStated reports whether an existing x-tfpfgen-list-wrapper node
// already says exactly what the compiled value would say — key for key, so a
// re-audit of an unchanged API proposes nothing.
func wrapperStated(extension *yaml.Node, value map[string]string) bool {
	if extension.Kind != yaml.MappingNode {
		return false
	}
	declared := map[string]string{}
	for i := 0; i+1 < len(extension.Content); i += 2 {
		declared[extension.Content[i].Value] = extension.Content[i+1].Value
	}
	if len(declared) != len(value) {
		return false
	}
	for k, v := range value {
		if declared[k] != v {
			return false
		}
	}
	return true
}

// literalSpelling renders a condition or default value the way a document
// annotation spells it: strings as themselves, everything else in its
// canonical JSON form.
func literalSpelling(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}

// jsonEqual compares two values through a JSON normalisation, the same rule
// correction application uses, so "already declared" here and "stale" there
// can never disagree.
func jsonEqual(a, b any) bool {
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	var na, nb any
	if json.Unmarshal(ra, &na) != nil || json.Unmarshal(rb, &nb) != nil {
		return false
	}
	return fmt.Sprintf("%#v", na) == fmt.Sprintf("%#v", nb)
}
