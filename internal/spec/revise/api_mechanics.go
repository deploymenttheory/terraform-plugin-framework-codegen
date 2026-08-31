// api_mechanics.go is the catalogue of API mechanics: properties an API
// carries in its responses that describe the API rather than the resource,
// which revision leaves out of the document. Terraform state models the
// resource a practitioner manages; a field whose value is derivable from the
// endpoint and the id carries no state, only noise — in every schema, every
// plan and every reference page.
//
// The catalogue is deliberately short and grows one mechanic at a time, as
// emittance surfaces one in a generated schema. Each mechanic matches exact
// wire spellings from a published convention, never a guess: a vendor's
// field that happens to be named like one is indistinguishable on the wire
// and is treated the same.
//
// Removal happens here, at revision, rather than at derivation: the revised
// document is the single source of truth, so the generated SDK, the
// derivation and the audit's proposals all stop seeing a mechanic together.
// The one place a removed property remains visible is the revision's own
// result, which names every site it left out.

package revise

import (
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/yamlwalk"
)

// NavigationLinks is the first API mechanic: the API's own links to itself
// and its neighbours, as HAL (JSON Hypertext Application Language) spells
// them under the reserved property "_links".
const NavigationLinks = "navigationLinks"

// apiMechanics maps each mechanic to the exact wire spellings it removes.
var apiMechanics = map[string][]string{
	NavigationLinks: {"_links"},
}

// apiMechanicOf answers the mechanic a wire property name belongs to.
func apiMechanicOf(wireName string) (string, bool) {
	for mechanic, wireNames := range apiMechanics {
		if slices.Contains(wireNames, wireName) {
			return mechanic, true
		}
	}
	return "", false
}

// APIMechanicRemoval is one property revision left out of the document.
type APIMechanicRemoval struct {
	// Mechanic is the catalogue entry the property matched, e.g.
	// "navigationLinks".
	Mechanic string
	// Property is the wire name removed, e.g. "_links".
	Property string
	// Pointer is the JSON pointer of the properties mapping the property
	// was removed from.
	Pointer string
}

// removeAPIMechanics removes every catalogued property from every schema
// properties mapping in the document, and each removed name from the
// required list beside it. The walk is in document order, so the removals
// are reported deterministically and the output is byte-stable.
func removeAPIMechanics(document []byte) ([]byte, []APIMechanicRemoval, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(document, &root); err != nil {
		return nil, nil, fmt.Errorf("the revised document is not usable YAML: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, nil, fmt.Errorf("the revised document is not a single YAML document")
	}

	var removed []APIMechanicRemoval
	removeAPIMechanicsFrom(root.Content[0], "", &removed)
	if len(removed) == 0 {
		return document, nil, nil
	}

	yamlwalk.ForceBlockStyle(&root)
	out, err := yaml.Marshal(&root)
	if err != nil {
		return nil, nil, err
	}
	return out, removed, nil
}

// removeAPIMechanicsFrom walks one node. At every mapping holding a
// "properties" mapping, catalogued keys are removed from it, and the names
// removed are pruned from the mapping's own "required" sequence — the
// sibling that would otherwise demand a property that is no longer there.
func removeAPIMechanicsFrom(node *yaml.Node, pointer string, removed *[]APIMechanicRemoval) {
	switch node.Kind {
	case yaml.MappingNode:
		for index := 0; index+1 < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			childPointer := pointer + "/" + escapeToken(key.Value)
			if key.Value == "properties" && value.Kind == yaml.MappingNode {
				names := removeCataloguedKeys(value, childPointer, removed)
				if len(names) > 0 {
					pruneRequired(node, names)
				}
			}
			removeAPIMechanicsFrom(value, childPointer, removed)
		}
	case yaml.SequenceNode:
		for index, item := range node.Content {
			removeAPIMechanicsFrom(item, fmt.Sprintf("%s/%d", pointer, index), removed)
		}
	}
}

// removeCataloguedKeys removes every catalogued key from one properties
// mapping, recording each removal, and answers the wire names removed.
func removeCataloguedKeys(properties *yaml.Node, pointer string, removed *[]APIMechanicRemoval) []string {
	var names []string
	kept := properties.Content[:0]
	for index := 0; index+1 < len(properties.Content); index += 2 {
		key, value := properties.Content[index], properties.Content[index+1]
		if mechanic, isMechanic := apiMechanicOf(key.Value); isMechanic {
			names = append(names, key.Value)
			*removed = append(*removed, APIMechanicRemoval{Mechanic: mechanic, Property: key.Value, Pointer: pointer})
			continue
		}
		kept = append(kept, key, value)
	}
	properties.Content = kept
	return names
}

// pruneRequired drops the removed names from a schema node's required
// sequence, and the sequence itself when nothing remains.
func pruneRequired(schema *yaml.Node, names []string) {
	dropped := map[string]bool{}
	for _, name := range names {
		dropped[name] = true
	}
	for index := 0; index+1 < len(schema.Content); index += 2 {
		if schema.Content[index].Value != "required" || schema.Content[index+1].Kind != yaml.SequenceNode {
			continue
		}
		sequence := schema.Content[index+1]
		kept := sequence.Content[:0]
		for _, item := range sequence.Content {
			if !dropped[item.Value] {
				kept = append(kept, item)
			}
		}
		sequence.Content = kept
		if len(sequence.Content) == 0 {
			schema.Content = append(schema.Content[:index], schema.Content[index+2:]...)
		}
		return
	}
}
