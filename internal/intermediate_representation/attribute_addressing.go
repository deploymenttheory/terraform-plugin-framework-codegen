// The attributes that exist to address an object rather than to describe it:
// the id every entity carries, and the path parameters above it. No request
// or response body declares them, so nothing else in the derivation would
// produce them.

package intermediate_representation

// ensureID guarantees the id attribute every resource and datasource
// carries: computed, mapped from the response'schema id field when the schema
// declares one, otherwise synthesized from the item path parameter.
func ensureID(tree *AttributeTree, keyParam string, keyType AttributeType) {
	for index := range tree.Attributes {
		if tree.Attributes[index].Name == "id" {
			tree.Attributes[index].ComputedOptionalRequired = Computed
			tree.Attributes[index].RequiresReplace = false
			return
		}
	}
	wire := keyParam
	if wire == "" {
		wire = "id"
	}
	kind := keyType
	if kind == "" {
		kind = TypeString
	}
	tree.Attributes = append([]Attribute{{
		Name:                     "id",
		WireName:                 wire,
		Kind:                     kind,
		ComputedOptionalRequired: Computed,
	}}, tree.Attributes...)
}

// ensureParentParameters gives every path parameter above the item key an
// attribute to be read from: required, and prepended in path order ahead of
// the id.
//
// An item path is not always /things/{id}. A parent-scoped API spells it
// /repos/{owner}/{repo}/rulesets/{ruleset_id}, and owner and repo appear in
// no request or response body — they are addressing, not content. Emission
// had nothing to feed them from and refused the entity, which on a
// thoroughly parent-scoped document is most of the API.
//
// A parent the body does declare is left as the body declares it; the
// document is a better authority on its own field than the URL is. Only a
// parameter no attribute answers is added.
func ensureParentParameters(tree *AttributeTree, parents []Parameter) {
	if tree == nil || len(parents) == 0 {
		return
	}
	// A name the tree already uses cannot be added again, whatever it holds:
	// two attributes of one name is not a schema. Where the sitting tenant is
	// an object, it is a different thing the document spells the same way — a
	// repository's owner block beside the owner segment of its path — and it
	// cannot answer the parameter either. Emission refuses the entity by
	// name, which is a better answer than a renamed attribute nobody asked
	// for or a schema that does not load.
	declared := make(map[string]bool, len(tree.Attributes))
	for _, attribute := range tree.Attributes {
		declared[attribute.Name] = true
	}

	added := make([]Attribute, 0, len(parents))
	for _, parent := range parents {
		name := snakeCase(parent.Name)
		if declared[name] {
			continue
		}
		declared[name] = true
		kind := parent.Type
		if kind == "" {
			kind = TypeString
		}
		added = append(added, Attribute{
			Name:                     name,
			WireName:                 parent.Name,
			Kind:                     kind,
			ComputedOptionalRequired: Required,
			// Addressing is not editable: an object does not move to another
			// parent in place, and every API that admits the move spells it
			// as its own operation.
			RequiresReplace: true,
		})
	}
	if len(added) == 0 {
		return
	}
	tree.Attributes = append(added, tree.Attributes...)
}

// ensureFilterAttributes offers one optional argument per scalar field at the
// root of a listed object, so a caller selects the object it wants by a value
// it knows instead of by its position in the collection.
//
// Only the root scalars. A nested field would need HCL to describe the whole
// object to match one leaf of it, and a collection has no single value to
// compare, so neither is offered — the caller reads those off the items the
// filters selected.
//
// A name the tree already carries is left alone: it is an addressing
// parameter, already required, and a required attribute narrows the results
// as well as an optional one would.
func ensureFilterAttributes(tree, item *AttributeTree) {
	if tree == nil || item == nil {
		return
	}
	declared := make(map[string]bool, len(tree.Attributes))
	for _, attribute := range tree.Attributes {
		declared[attribute.Name] = true
	}

	added := make([]Attribute, 0, len(item.Attributes))
	for _, attribute := range item.Attributes {
		if attribute.Nested != nil || declared[attribute.Name] {
			continue
		}
		switch attribute.Kind {
		case TypeString, TypeBool, TypeInt64, TypeFloat64:
		default:
			continue
		}
		declared[attribute.Name] = true
		added = append(added, Attribute{
			Name:                     attribute.Name,
			WireName:                 attribute.WireName,
			Description:              attribute.Description,
			Kind:                     attribute.Kind,
			ComputedOptionalRequired: Optional,
			Filter:                   true,
		})
	}
	tree.Attributes = append(tree.Attributes, added...)
}

// addressingSchema is a collection path's addressing attributes as a tree of
// their own, for a list resource to declare as the configuration of its list
// block. Nil when the path takes no parameters.
//
// Every parameter is a parent: a collection path carries no item key, so
// there is no id to absorb the last one. None carries RequiresReplace — a
// list block declares a query, and a query has no plan for a modifier to act
// on.
func addressingSchema(parameters []Parameter) *AttributeTree {
	if len(parameters) == 0 {
		return nil
	}
	tree := &AttributeTree{}
	ensureParentParameters(tree, parameters)
	for index := range tree.Attributes {
		tree.Attributes[index].RequiresReplace = false
	}
	return tree
}

// parentParameters is an operation's path parameters above the item key: all
// of them but the last, which addresses the object itself and becomes the id.
func parentParameters(parameters []Parameter) []Parameter {
	if len(parameters) < 2 {
		return nil
	}
	return parameters[:len(parameters)-1]
}

// requireKey turns the lookup key into the datasource'schema single required
// argument: the matching attribute becomes required, or a new one is
// prepended when the response object does not carry the key.
func requireKey(tree *AttributeTree, keyParam string, keyType AttributeType) {
	name := snakeCase(keyParam)
	for index := range tree.Attributes {
		if tree.Attributes[index].Name == name {
			tree.Attributes[index].ComputedOptionalRequired = Required
			return
		}
	}
	kind := keyType
	if kind == "" {
		kind = TypeString
	}
	tree.Attributes = append([]Attribute{{
		Name:                     name,
		WireName:                 keyParam,
		Kind:                     kind,
		ComputedOptionalRequired: Required,
	}}, tree.Attributes...)
}
