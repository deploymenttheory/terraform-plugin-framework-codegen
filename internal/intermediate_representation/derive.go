package intermediate_representation

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
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

	derivation := &operationIndexer{index: indexOperations(document)}
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
		names := deriveNames(configuration.Provider.Name, classification.Key, classification.CollectionPath, classification.Tag)
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
			model.ExcludedByConfiguration = append(model.ExcludedByConfiguration, unsupportedEntity(names, classification.CollectionPath, "", Cause{Code: CauseExcludedByConfiguration}, configExcludedReason))
			continue
		}
		parentKey[classification.CollectionPath] = names.Key
		family[original] = append(family[original], len(keep))
		keep = append(keep, kept{classification: classification, names: names})
	}
	for _, excluded := range classifications.Excluded {
		names := deriveNames(configuration.Provider.Name, excluded.Key, excluded.CollectionPath, excluded.Tag)
		model.ExcludedByClassification = append(model.ExcludedByClassification, unsupportedEntity(names, excluded.CollectionPath, "", Cause{Code: excluded.Cause}, excluded.Reason))
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
				resource := derivation.resource(candidate.classification, candidate.names, parentKey)
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
	for _, excluded := range [][]UnsupportedEntity{model.ExcludedByConfiguration, model.ExcludedByClassification} {
		sort.Slice(excluded, func(i, j int) bool {
			if excluded[i].Key != excluded[j].Key {
				return excluded[i].Key < excluded[j].Key
			}
			return excluded[i].Reason < excluded[j].Reason
		})
	}
}

// operationIndexer carries the operation index every entity builder reads.
type operationIndexer struct {
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
func (derivation *operationIndexer) full(reference *specmodel.OperationReference) *specmodel.Operation {
	if reference == nil {
		return nil
	}
	return derivation.index[reference.Path+" "+reference.Method]
}

// operation renders one operation reference into the dialect-neutral form binders
// key on, nil for a nil reference.
func (derivation *operationIndexer) operation(reference *specmodel.OperationReference, kind OperationKind) *Operation {
	if reference == nil {
		return nil
	}
	full := derivation.full(reference)
	return &Operation{
		Kind:            kind,
		Method:          reference.Method,
		PathTemplate:    reference.Path,
		OperationID:     reference.OperationID,
		PathParameters:  pathParameters(reference.Path, full),
		QueryParameters: requiredQueryParameters(full),
		SuccessCode:     successCode(full),
	}
}

// requiredQueryParameters lists an operation's required query parameters
// with the value the document states for each, in declaration order. A
// required parameter the document states no value for is left out: the
// document has not said what to send.
func requiredQueryParameters(operation *specmodel.Operation) []QueryParameter {
	if operation == nil {
		return nil
	}
	var out []QueryParameter
	for _, declared := range operation.Parameters {
		if declared.In != "query" || !declared.Required {
			continue
		}
		value := declared.Example
		if declared.Schema != nil {
			resolved := declared.Schema.Resolved()
			if value == nil {
				value = resolved.Example
			}
			if value == nil {
				value = resolved.Default
			}
		}
		if value == nil {
			continue
		}
		kind := scalarKind(declared.Schema)
		if kind == "" {
			kind = TypeString
		}
		out = append(out, QueryParameter{Name: declared.Name, Type: kind, Value: value})
	}
	return out
}

func (derivation *operationIndexer) resource(classification specmodel.Classification, names Names, parentKey map[string]string) Resource {
	parentEntity := enclosingEntity(classification.CollectionPath, parentKey)
	createFull, readFull := derivation.full(classification.Create), derivation.full(classification.Read)
	updateFull, deleteFull := derivation.full(classification.Update), derivation.full(classification.Delete)

	var createBody, readBody, updateBody *specmodel.Schema
	if createFull != nil {
		createBody = createFull.RequestBody
	}
	if readFull != nil {
		readBody = readFull.SuccessSchema()
	}
	if updateFull != nil {
		updateBody = updateFull.RequestBody
	}
	// A singleton has no create body; what the practitioner may set is what
	// the update accepts. Taking the write side from there is what keeps its
	// attributes from all deriving computed.
	//
	// It also makes the create-minus-update difference empty by
	// construction, which is right: an entity whose only write is the update
	// has nothing the update refuses.
	if classification.Singleton && updateFull != nil {
		createBody = updateFull.RequestBody
	}
	tree := buildTree(createBody, readBody, updateBody, classification.MissingUpdate)
	keyParam, keyType := itemKeyParameter(classification.ItemPath, readFull)
	// A singleton is not keyed: its path names no item, so its trailing
	// parameter addresses the parent it sits under, not the object.
	if classification.Singleton {
		keyParam, keyType = "", ""
	}
	// The audit may have found that the response spells this identifier
	// differently from the path parameter that addresses it. Where it has,
	// that name is the one the response can actually be read through.
	if readFull != nil {
		if named, ok := readFull.Extensions.IdentifierProperty(); ok {
			keyParam = named
		}
	}
	ensureID(tree, keyParam, keyType)
	refuseReservedRootNames(tree)
	readOperation := derivation.operation(classification.Read, OperationRead)
	if readOperation != nil {
		parents := parentParameters(readOperation.PathParameters)
		if classification.Singleton {
			parents = readOperation.PathParameters
		}
		ensureParentParameters(tree, parents, parentEntity)
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
		Singleton:           classification.Singleton,
		ParentEntity:        parentEntity,
		UpdateStyle:         updateStyle,
		EventualConsistency: maxEventualConsistency(createFull, readFull, updateFull, deleteFull),
		DeleteNotFoundOK:    deleteNotFoundOK,
		Timeouts:            defaultTimeouts(),
		ListWrapperKey:      listWrapperKey(derivation.full(classification.List)),
	}
}

// listWrapperKey is the wire property a list response wraps its item array
// under: empty when the response is a bare array (or absent), otherwise the
// wrapping key. The generated list mock keys its envelope on this instead of
// assuming every API wraps under "value" — the SDK's collection accessor is
// derived from the same answer, so the two agree.
//
// x-tfpfgen-list-wrapper wins where it is present: it carries what a
// live collection response actually contained, and it is written onto the
// operation precisely when the document's own list schema was found to be
// wrong. Absent it, the schema is read as before — the first array-typed
// property of a wrapping object — so an unaudited document behaves exactly
// as it did.
func listWrapperKey(list *specmodel.Operation) string {
	if list == nil {
		return ""
	}
	if wrapper, ok := list.Extensions.ListWrapper(); ok {
		if wrapper.Wrapped {
			return wrapper.Key
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

// listElementSchema is the schema of one object a collection response
// carries, whatever the response wraps it in.
//
// A bare array yields its items. A wrapping envelope — an object whose
// payload is an array under a key, beside a count or a pagination cursor —
// yields that array's items, the key taken from the observed
// x-tfpfgen-list-wrapper where the audit recorded one and from the
// document's own single array property otherwise.
//
// Taking the envelope as-is would make the element tree the envelope's own
// fields, and nothing would bind: the SDK reaches through the envelope to the
// element, so derivation would ask for results and totalCount off a model
// carrying id, username and date, every attribute would prune, and the entity
// would be removed for having nothing left to map. Envelopes are the common
// shape rather than the exception.
//
// A response that is neither is returned unchanged: it may be a single
// object the classification took for a collection, and guessing an element
// out of it would be worse than deriving nothing.
func listElementSchema(list *specmodel.Operation) *specmodel.Schema {
	if list == nil {
		return nil
	}
	body := list.SuccessSchema()
	flattened := flatten(body)
	if flattened.declaredType == "array" {
		return flattened.items
	}

	key := listWrapperKey(list)
	if key == "" {
		return body
	}
	for _, property := range flattened.properties {
		if property.Name != key {
			continue
		}
		if items := flatten(property.Schema).items; items != nil {
			return items
		}
	}
	return body
}

func (derivation *operationIndexer) datasource(classification specmodel.Classification, names Names) Datasource {
	readFull := derivation.full(classification.Read)
	var readBody *specmodel.Schema
	if readFull != nil {
		readBody = readFull.SuccessSchema()
	}
	// An entity the API only enumerates has no read to describe one object,
	// so the collection's own element describes it instead. That is the same
	// schema a list resource reads, and the only account of the shape the
	// document offers.
	if readBody == nil {
		readBody = listElementSchema(derivation.full(classification.List))
	}
	itemTree := buildTree(nil, readBody, nil, false)
	keyParam, keyType := itemKeyParameter(classification.ItemPath, readFull)
	ensureID(itemTree, keyParam, keyType)

	if classification.LookupByKey {
		requireKey(itemTree, keyParam, keyType)
		refuseReservedRootNames(itemTree)
		readOperation := derivation.operation(classification.Read, OperationRead)
		if readOperation != nil {
			ensureParentParameters(itemTree, parentParameters(readOperation.PathParameters), "")
		}
		return Datasource{
			Names:        names,
			Operations:   Operations{Read: readOperation},
			Schema:       itemTree,
			LookupByKey:  true,
			KeyParameter: keyParam,
		}
	}

	// The companion datasource: the item's own scalar fields select which
	// objects come back, items carries them. A datasource whose only
	// argument is the collection itself makes the caller address results by
	// position, so the filters are what makes it usable.
	listOperation := derivation.operation(classification.List, OperationList)
	companionTree := &AttributeTree{}
	// A collection path carries no item key, so every one of its path
	// parameters is a parent the caller has to supply. Ahead of the filters,
	// because a parent is required and a filter optional: where both spell
	// one name, the caller must still be able to fill the path.
	if listOperation != nil {
		ensureParentParameters(companionTree, listOperation.PathParameters, "")
	}
	ensureFilterAttributes(companionTree, itemTree)
	companionTree.Attributes = append(companionTree.Attributes, Attribute{
		Name: "items", WireName: "items", Kind: TypeList, ElementType: TypeObject,
		ComputedOptionalRequired: Computed, Nested: itemTree,
	})
	refuseReservedRootNames(companionTree)

	return Datasource{
		Names:          names,
		Operations:     Operations{Read: derivation.operation(classification.Read, OperationRead), List: listOperation},
		Schema:         companionTree,
		ListWrapperKey: listWrapperKey(derivation.full(classification.List)),
	}
}

// listResource derives the list capability of a managed resource: terraform
// matches it to that resource by type name, so it carries the entity's own
// Names and exists only where the resource does.
func (derivation *operationIndexer) listResource(classification specmodel.Classification, names Names) ListResource {
	listFull := derivation.full(classification.List)
	element := listElementSchema(listFull)
	listOperation := *derivation.operation(classification.List, OperationList)
	addressing := addressingSchema(listOperation.PathParameters)
	refuseReservedRootNames(addressing)

	// A list result is an identity, and an identity is the resource's id. The
	// resource takes its id from the item path key where the object declares
	// no property of that name, and the element has to answer the same key by
	// the same rule: /tests/{testId} keys the object on testId whether it is
	// being read one at a time or streamed.
	//
	// Without this the element kept only the document's own spelling, and an
	// API that does not happen to call its key "id" published no identity at
	// all — which refused the entity outright, for a difference in wording.
	tree := buildTree(nil, element, nil, false)
	keyParam, keyType := itemKeyParameter(classification.ItemPath, derivation.full(classification.Read))
	ensureID(tree, keyParam, keyType)

	return ListResource{
		Names:            names,
		ListOperation:    listOperation,
		Schema:           tree,
		AddressingSchema: addressing,
		ListWrapperKey:   listWrapperKey(listFull),
	}
}

func (derivation *operationIndexer) action(classification specmodel.Classification, names Names, parentKey map[string]string) Action {
	createFull := derivation.full(classification.Create)
	var request *AttributeTree
	if createFull != nil && createFull.RequestBody != nil {
		request = buildTree(createFull.RequestBody, nil, nil, false)
		refuseReservedRootNames(request)
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

// templateParameterPattern matches one {name} segment of a path template.
var templateParameterPattern = regexp.MustCompile(`\{([^}]+)\}`)

// pathParameters lists an operation's path parameters in path-template order —
// the order any call expression will take them — typed from the declared
// parameter schema, string when the document does not say.
func pathParameters(pathTemplate string, operation *specmodel.Operation) []Parameter {
	matches := templateParameterPattern.FindAllStringSubmatch(pathTemplate, -1)
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

// itemKeyParameter names the item path's trailing parameter and its type,
// which is what the id attribute and a lookup key derive from.
func itemKeyParameter(itemPath string, read *specmodel.Operation) (string, AttributeType) {
	matches := templateParameterPattern.FindAllStringSubmatch(itemPath, -1)
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
		if derivation, ok := operation.Extensions.ReadAfterWrite(); ok && derivation > max {
			max = derivation
		}
	}
	return max
}
