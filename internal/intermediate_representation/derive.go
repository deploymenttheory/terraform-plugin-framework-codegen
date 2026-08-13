package intermediate_representation

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

// configExcludedReason is the reason recorded for entities services.exclude
// drops, distinguishable at a glance from a classification exclusion.
const configExcludedReason = "excluded by configuration"

// Derive computes the model from the revised document and the config. It is
// pure: the same inputs yield the same value every run, byte-for-byte under
// JSON. Nothing here reads a clock, the environment or the filesystem, and
// nothing writes — the derivation exists only in memory, for exactly one
// run.
func Derive(document *specmodel.Document, configuration *config.Config) (*Model, error) {
	if document == nil {
		return nil, errors.New("intermediate_representation: the document is nil")
	}
	if configuration == nil {
		return nil, errors.New("intermediate_representation: the config is nil")
	}
	if configuration.Provider.Name == "" {
		return nil, errors.New("intermediate_representation: provider.name is empty; every terraform type name derives from it")
	}

	derivation := &deriver{index: indexOperations(document)}
	model := &Model{Provider: Provider{Name: configuration.Provider.Name}}

	excluded := map[string]bool{}
	for _, excludedKey := range configuration.Services.Exclude {
		excluded[excludedKey] = true
	}

	classifications := specmodel.Classify(document)

	// Decide inclusion and names first: actions need every kept entity's
	// key to resolve their parent, and a key collision must be resolved
	// before any entity is built against an ambiguous name.
	type kept struct {
		classification specmodel.Classification
		names          Names
	}
	var keep []kept
	claimed := map[string]string{} // final key -> the collection path that claimed it
	family := map[string][]int{}   // pre-disambiguation key -> indices into keep
	parentKey := map[string]string{}
	for _, classification := range classifications.Entities {
		names := deriveNames(configuration.Provider.Name, classification.Key, classification.CollectionPath)
		original := names.Key
		// Two collection paths can derive one key: sibling API versions
		// once the version prefix factors out, or paths that differ only
		// in an embedded parameter. Both entities generate: the later one
		// in collection-path order takes a mechanically disambiguated key,
		// and every member of the family carries a co-management note so
		// the generated schema description says the overlap out loud.
		if winner, taken := claimed[names.Key]; taken {
			names = names.withKey(configuration.Provider.Name,
				disambiguateKey(names.Key, classification.CollectionPath, winner, claimed))
		}
		claimed[names.Key] = classification.CollectionPath
		if excluded[names.Service] || excluded[names.Key] {
			model.Excluded = append(model.Excluded, Exclusion{Key: names.Key, Reason: configExcludedReason})
			continue
		}
		parentKey[classification.CollectionPath] = names.Key
		family[original] = append(family[original], len(keep))
		keep = append(keep, kept{classification: classification, names: names})
	}
	for _, excluded := range classifications.Excluded {
		names := deriveNames(configuration.Provider.Name, excluded.Key, excluded.CollectionPath)
		model.Excluded = append(model.Excluded, Exclusion{Key: names.Key, Reason: excluded.Reason})
	}

	// A collision family that generates more than one entity co-manages
	// one API surface: every member's note names its siblings, so the
	// prose lands on both sides of the overlap.
	notes := map[string]string{}
	for _, members := range family {
		if len(members) < 2 {
			continue
		}
		for _, i := range members {
			siblings := make([]string, 0, len(members)-1)
			for _, j := range members {
				if j != i {
					siblings = append(siblings, keep[j].names.TerraformType)
				}
			}
			sort.Strings(siblings)
			notes[keep[i].names.Key] = coManagementNote(siblings)
		}
	}

	for _, candidate := range keep {
		note := notes[candidate.names.Key]
		for _, kind := range candidate.classification.Kinds {
			switch kind {
			case specmodel.KindResource:
				resource := derivation.resource(candidate.classification, candidate.names)
				resource.CoManagementNote = note
				model.Resources = append(model.Resources, resource)
			case specmodel.KindDatasource:
				ds := derivation.datasource(candidate.classification, candidate.names)
				ds.CoManagementNote = note
				model.Datasources = append(model.Datasources, ds)
			case specmodel.KindListResource:
				lr := derivation.listResource(candidate.classification, candidate.names)
				lr.CoManagementNote = note
				model.ListResources = append(model.ListResources, lr)
			case specmodel.KindAction:
				a := derivation.action(candidate.classification, candidate.names, parentKey)
				a.CoManagementNote = note
				model.Actions = append(model.Actions, a)
			}
		}
	}

	sortModel(model)
	return model, nil
}

// disambiguateKey computes the later entity's key when two collection
// paths derive the same one. The algorithm is mechanical, never a guess:
//
//  1. Split both collection paths into segments.
//  2. Keep, in path order, every segment of the later path the winning
//     path does not also contain — the distinguishing segments.
//  3. Render a distinguishing parameter segment "{name}" as "by_" plus
//     the name snake_cased, and a distinguishing literal segment as
//     itself snake_cased.
//  4. Append the renderings to the colliding key, underscore-joined:
//     "/tags/{id}/assign" against the winner "/tags/assign" turns
//     "tags_assign" into "tags_assign_by_id".
//  5. Should the result still be claimed, or should no segment
//     distinguish the paths, an ordinal _2, _3 … appends until the key
//     is free.
func disambiguateKey(key, laterPath, winnerPath string, claimed map[string]string) string {
	winner := map[string]bool{}
	for _, segment := range pathSegments(winnerPath) {
		winner[segment] = true
	}
	parts := []string{key}
	for _, segment := range pathSegments(laterPath) {
		if winner[segment] {
			continue
		}
		if strings.HasPrefix(segment, "{") {
			parts = append(parts, "by_"+snakeCase(strings.Trim(segment, "{}")))
			continue
		}
		parts = append(parts, snakeCase(segment))
	}
	candidate := strings.Join(parts, "_")
	if _, taken := claimed[candidate]; !taken && candidate != key {
		return candidate
	}
	for n := 2; ; n++ {
		next := fmt.Sprintf("%s_%d", candidate, n)
		if _, taken := claimed[next]; !taken {
			return next
		}
	}
}

// pathSegments splits a path template into its non-empty segments.
func pathSegments(path string) []string {
	var out []string
	for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
		if segment != "" {
			out = append(out, segment)
		}
	}
	return out
}

// sortModel fixes every slice's order by entity key, so document order and
// classification order can never leak into the model's shape.
func sortModel(model *Model) {
	sort.Slice(model.Resources, func(i, j int) bool { return model.Resources[i].Names.Key < model.Resources[j].Names.Key })
	sort.Slice(model.Datasources, func(i, j int) bool { return model.Datasources[i].Names.Key < model.Datasources[j].Names.Key })
	sort.Slice(model.ListResources, func(i, j int) bool { return model.ListResources[i].Names.Key < model.ListResources[j].Names.Key })
	sort.Slice(model.Actions, func(i, j int) bool { return model.Actions[i].Names.Key < model.Actions[j].Names.Key })
	sort.Slice(model.Excluded, func(i, j int) bool {
		if model.Excluded[i].Key != model.Excluded[j].Key {
			return model.Excluded[i].Key < model.Excluded[j].Key
		}
		return model.Excluded[i].Reason < model.Excluded[j].Reason
	})
}

// deriver carries the operation index every entity builder reads.
type deriver struct {
	index map[string]*specmodel.Operation
}

// indexOperations keys every operation by path and method, so a
// classification's operation reference finds its full operation back.
func indexOperations(document *specmodel.Document) map[string]*specmodel.Operation {
	index := map[string]*specmodel.Operation{}
	for pi := range document.Paths {
		for oi := range document.Paths[pi].Operations {
			operation := &document.Paths[pi].Operations[oi]
			index[operation.Path+" "+operation.Method] = operation
		}
	}
	return index
}

// full resolves a classification operation reference to the document's
// operation, nil for a nil reference.
func (derivation *deriver) full(reference *specmodel.Op) *specmodel.Operation {
	if reference == nil {
		return nil
	}
	return derivation.index[reference.Path+" "+reference.Method]
}

// operation renders one operation reference into the dialect-neutral form binders
// key on, nil for a nil reference.
func (derivation *deriver) operation(reference *specmodel.Op, kind OperationKind) *Operation {
	if reference == nil {
		return nil
	}
	full := derivation.full(reference)
	return &Operation{
		Kind:           kind,
		Method:         reference.Method,
		PathTemplate:   reference.Path,
		OperationID:    reference.OperationID,
		PathParameters: pathParams(reference.Path, full),
		SuccessCode:    successCode(full),
	}
}

func (derivation *deriver) resource(classification specmodel.Classification, names Names) Resource {
	createFull, readFull := derivation.full(classification.Create), derivation.full(classification.Read)
	updateFull, deleteFull := derivation.full(classification.Update), derivation.full(classification.Delete)

	var createBody, readBody *specmodel.Schema
	if createFull != nil {
		createBody = createFull.RequestBody
	}
	if readFull != nil {
		readBody = readFull.SuccessSchema()
	}
	tree := buildTree(createBody, readBody, classification.MissingUpdate)
	keyParam, keyType := itemKeyParam(classification.ItemPath, readFull)
	ensureID(tree, keyParam, keyType)
	readOperation := derivation.operation(classification.Read, OperationRead)
	if readOperation != nil {
		ensureParentParameters(tree, parentParameters(readOperation.PathParameters))
	}

	updateStyle := ""
	if classification.Update != nil {
		updateStyle = UpdateStylePatchMerge
		if updateFull != nil {
			if s, ok := updateFull.Extensions.UpdateStyle(); ok {
				updateStyle = s
			}
		}
	}

	deleteNotFoundOK := false
	if deleteFull != nil {
		deleteNotFoundOK, _ = deleteFull.Extensions.DeleteNotFoundOK()
	}

	return Resource{
		Names: names,
		Operations: Operations{
			Create: derivation.operation(classification.Create, OperationCreate),
			Read:   derivation.operation(classification.Read, OperationRead),
			Update: derivation.operation(classification.Update, OperationUpdate),
			Delete: derivation.operation(classification.Delete, OperationDelete),
			List:   derivation.operation(classification.List, OperationList),
		},
		Schema:              tree,
		MissingUpdate:       classification.MissingUpdate,
		UpdateStyle:         updateStyle,
		EventualConsistency: maxEventualConsistency(createFull, readFull, updateFull, deleteFull),
		DeleteNotFoundOK:    deleteNotFoundOK,
		Timeouts:            defaultTimeouts(),
		ListEnvelopeKey:     listEnvelopeKey(derivation.full(classification.List)),
	}
}

// listEnvelopeKey is the wire property a list response wraps its item array
// under: empty when the response is a bare array (or absent), otherwise the
// wrapping key. The generated list mock keys its envelope on this instead of
// assuming every API wraps under "value" — the SDK's collection accessor is
// derived from the same answer, so the two agree.
//
// x-tfpfgen-list-response-shape wins where it is present: it carries what a
// live collection response actually contained, and it is written onto the
// operation precisely when the document's own list schema was found to be
// wrong. Absent it, the schema is read as before — the first array-typed
// property of a wrapping object — so an unaudited document behaves exactly
// as it did.
func listEnvelopeKey(list *specmodel.Operation) string {
	if list == nil {
		return ""
	}
	if shape, ok := list.Extensions.ListResponseShape(); ok {
		if shape.Wrapped() {
			return shape.Key
		}
		return ""
	}
	flattened := flatten(list.SuccessSchema())
	if flattened.declaredType == "array" {
		return ""
	}
	for _, property := range flattened.properties {
		if flatten(property.Schema).declaredType == "array" {
			return property.Name
		}
	}
	return ""
}

func (derivation *deriver) datasource(classification specmodel.Classification, names Names) Datasource {
	readFull := derivation.full(classification.Read)
	var readBody *specmodel.Schema
	if readFull != nil {
		readBody = readFull.SuccessSchema()
	}
	itemTree := buildTree(nil, readBody, false)
	keyParam, keyType := itemKeyParam(classification.ItemPath, readFull)
	ensureID(itemTree, keyParam, keyType)

	if classification.LookupByKey {
		requireKey(itemTree, keyParam, keyType)
		readOperation := derivation.operation(classification.Read, OperationRead)
		if readOperation != nil {
			ensureParentParameters(itemTree, parentParameters(readOperation.PathParameters))
		}
		return Datasource{
			Names:        names,
			Operations:   Operations{Read: readOperation},
			Schema:       itemTree,
			LookupByKey:  true,
			KeyParameter: keyParam,
		}
	}

	// The companion datasource: filter_type and filter_value select which
	// objects come back, items carries them. The filter attributes are the
	// toolkit's own vocabulary, not the API's, so they take no wire names
	// beyond their own.
	listOperation := derivation.operation(classification.List, OperationList)
	companionTree := &AttributeTree{Attributes: []Attribute{
		{Name: "filter_type", WireName: "filter_type", Kind: TypeString, Presence: PresenceRequired},
		{Name: "filter_value", WireName: "filter_value", Kind: TypeString, Presence: PresenceOptional},
		{Name: "items", WireName: "items", Kind: TypeList, ElementKind: TypeObject, Presence: PresenceComputed, Nested: itemTree},
	}}
	// A collection path carries no item key, so every one of its path
	// parameters is a parent the caller has to supply.
	if listOperation != nil {
		ensureParentParameters(companionTree, listOperation.PathParameters)
	}

	return Datasource{
		Names:           names,
		Operations:      Operations{Read: derivation.operation(classification.Read, OperationRead), List: listOperation},
		Schema:          companionTree,
		ListEnvelopeKey: listEnvelopeKey(derivation.full(classification.List)),
	}
}

func (derivation *deriver) listResource(classification specmodel.Classification, names Names) ListResource {
	listFull := derivation.full(classification.List)
	var listBody *specmodel.Schema
	if listFull != nil {
		listBody = listFull.SuccessSchema()
	}
	// A list response is usually the element array itself; an envelope
	// object is taken as-is rather than guessed into an element.
	element := listBody
	if flattened := flatten(listBody); flattened.declaredType == "array" && flattened.items != nil {
		element = flattened.items
	}
	return ListResource{
		Names:           names,
		ListOperation:   *derivation.operation(classification.List, OperationList),
		Schema:          buildTree(nil, element, false),
		ListEnvelopeKey: listEnvelopeKey(listFull),
	}
}

func (derivation *deriver) action(classification specmodel.Classification, names Names, parentKey map[string]string) Action {
	createFull := derivation.full(classification.Create)
	var request *AttributeTree
	if createFull != nil && createFull.RequestBody != nil {
		request = buildTree(createFull.RequestBody, nil, false)
	}
	return Action{
		Names:           names,
		InvokeOperation: *derivation.operation(classification.Create, OperationInvoke),
		RequestSchema:   request,
		ParentEntity:    enclosingEntity(classification.CollectionPath, parentKey),
	}
}

// enclosingEntity finds the entity whose collection path is the longest
// proper prefix of the action's, segment-aligned; empty when none is.
func enclosingEntity(collectionPath string, parentKey map[string]string) string {
	path := collectionPath
	for {
		cut := strings.LastIndex(path, "/")
		if cut <= 0 {
			return ""
		}
		path = path[:cut]
		if key, ok := parentKey[path]; ok {
			return key
		}
	}
}

// templateParam matches one {name} segment of a path template.
var templateParam = regexp.MustCompile(`\{([^}]+)\}`)

// pathParams lists an operation's path parameters in path-template order —
// the order any call expression will take them — typed from the declared
// parameter schema, string when the document does not say.
func pathParams(pathTemplate string, operation *specmodel.Operation) []Parameter {
	matches := templateParam.FindAllStringSubmatch(pathTemplate, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]Parameter, 0, len(matches))
	for _, model := range matches {
		property := Parameter{Name: model[1], Type: TypeString}
		if operation != nil {
			for _, declared := range operation.Parameters {
				if declared.In == "path" && declared.Name == model[1] {
					if candidate := scalarKind(declared.Schema); candidate != "" {
						property.Type = candidate
					}
				}
			}
		}
		out = append(out, property)
	}
	return out
}

// itemKeyParam names the item path's trailing parameter and its type,
// which is what the id attribute and a lookup key derive from.
func itemKeyParam(itemPath string, read *specmodel.Operation) (string, AttributeType) {
	matches := templateParam.FindAllStringSubmatch(itemPath, -1)
	if len(matches) == 0 {
		return "", ""
	}
	name := matches[len(matches)-1][1]
	kind := TypeString
	if read != nil {
		for _, property := range read.Parameters {
			if property.In == "path" && property.Name == name {
				if candidate := scalarKind(property.Schema); candidate != "" {
					kind = candidate
				}
			}
		}
	}
	return name, kind
}

// scalarKind maps a scalar schema type onto an attribute kind, empty for
// anything else.
func scalarKind(s *specmodel.Schema) AttributeType {
	switch flatten(s).declaredType {
	case "string":
		return TypeString
	case "boolean":
		return TypeBool
	case "integer":
		return TypeInt64
	case "number":
		return TypeFloat64
	}
	return ""
}

// successCode is the first declared 2xx status. Responses arrive sorted by
// status string, which orders 2xx codes numerically.
func successCode(operation *specmodel.Operation) int {
	if operation == nil {
		return 0
	}
	for _, resource := range operation.Responses {
		if strings.HasPrefix(resource.Status, "2") {
			if n, err := strconv.Atoi(resource.Status); err == nil {
				return n
			}
		}
	}
	return 0
}

// maxEventualConsistency takes the largest declared read-after-write lag
// across the lifecycle: whichever operation observed the worst lag governs
// how long generated code must be prepared to wait.
func maxEventualConsistency(operations ...*specmodel.Operation) time.Duration {
	var max time.Duration
	for _, operation := range operations {
		if operation == nil {
			continue
		}
		if derivation, ok := operation.Extensions.EventualConsistency(); ok && derivation > max {
			max = derivation
		}
	}
	return max
}
