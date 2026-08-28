package run

// Reference binding is how a synthesised body carries a real id where the
// document only says that a field holds one. A field named agentId, or an
// array named alertRules, refers to an object in another collection, and no
// invented value satisfies it: the document's example id belongs to the
// vendor's tenant, and a string of the right type is refused by construction.
// The only valid value is one the API itself already serves, and the
// document says where — the collection whose path spells the same noun.
//
// Binding happens at synthesis, before the first request, by writing a
// $borrow:<collection path> token into the body; execution resolves the
// token with one read of that collection, cached for the run. The token
// keeps synthesis pure and offline, as every other plan token does.

import (
	"sort"
	"strings"
	"unicode"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// BorrowToken prefixes a body value execution replaces with a real id read
// from the named collection path.
const BorrowToken = "$borrow:"

// referenceCollections indexes every classified entity's collection path by
// the noun its static segments spell, so a field name can be matched to the
// collection it references.
func referenceCollections(classified []specmodel.Classification) map[string]string {
	out := map[string]string{}
	for _, c := range classified {
		// A collection under a parent cannot be read without the parent's
		// id, which a borrow has no way to supply.
		if c.CollectionPath == "" || c.List == nil || strings.Contains(c.CollectionPath, "{") {
			continue
		}
		noun := pathNoun(c.CollectionPath)
		if noun == "" {
			continue
		}
		// Two collections spelling one noun would make the match a guess;
		// the shorter path is the root collection, which is the one a bare
		// field name means.
		if existing, taken := out[noun]; !taken || len(c.CollectionPath) < len(existing) {
			out[noun] = c.CollectionPath
		}
	}
	return out
}

// pathNoun spells a collection path's static segments as one noun: each
// segment split on punctuation, every word singularised and lower-cased,
// parameters left out. "/alerts/rules" and "/alert-rules" both spell
// "alertrule".
func pathNoun(path string) string {
	var words []string
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || strings.HasPrefix(segment, "{") {
			continue
		}
		words = append(words, splitWords(segment)...)
	}
	return joinNoun(words)
}

// fieldNoun spells a field name as the noun it references: an Id or Ids
// suffix dropped, the rest split on case and punctuation, each word
// singularised and lower-cased. "agentId" and "agents" both spell "agent";
// "alertRules" spells "alertrule".
func fieldNoun(field string) string {
	nouns := fieldNouns(field)
	if len(nouns) == 0 {
		return ""
	}
	return nouns[0]
}

// fieldNouns spells every noun a field name may reference, longest first:
// the whole name, then each shorter tail of it. A qualified reference —
// targetAgentId, loginAccountGroupId — names its collection in its last
// words and its role in the first, so "targetagent" is tried before
// "agent".
func fieldNouns(field string) []string {
	words := splitWords(field)
	if n := len(words); n > 0 && (words[n-1] == "id" || words[n-1] == "ids") {
		words = words[:n-1]
	}
	var out []string
	for start := 0; start < len(words); start++ {
		out = append(out, joinNoun(words[start:]))
	}
	return out
}

// referencedCollection answers the collection path a field references: the
// longest tail of its name that spells a collection the document lists.
func referencedCollection(field string, references map[string]string) (string, bool) {
	for _, noun := range fieldNouns(field) {
		if path, ok := references[noun]; ok {
			return path, true
		}
	}
	return "", false
}

// splitWords breaks an identifier on case changes, digits-to-letters
// boundaries and punctuation, lower-casing every word.
func splitWords(identifier string) []string {
	var words []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			words = append(words, strings.ToLower(current.String()))
			current.Reset()
		}
	}
	runes := []rune(identifier)
	for i, r := range runes {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(runes[i-1]) || (i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(runes[i-1]))):
			flush()
			current.WriteRune(r)
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return words
}

// joinNoun singularises each word and joins them. Singularisation is the
// one rule English plurals mostly follow — a trailing s goes — which is
// enough for a match that only ever has to agree with itself.
func joinNoun(words []string) string {
	var b strings.Builder
	for _, w := range words {
		if len(w) > 1 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = strings.TrimSuffix(w, "s")
		}
		b.WriteString(w)
	}
	return b.String()
}

// referenceField reports whether a field name says it holds an identifier
// of another object: an id-suffixed name, or a plural naming a collection.
func referenceField(field string, plural bool) bool {
	words := splitWords(field)
	if len(words) == 0 {
		return false
	}
	last := words[len(words)-1]
	if last == "id" || last == "ids" {
		return len(words) > 1
	}
	return plural && strings.HasSuffix(last, "s")
}

// bindReferences walks a synthesised value and replaces every reference the
// document can satisfy with a borrow token: a string under an id-named key
// becomes one token, a list of strings under a collection-named key becomes
// a list of one, and objects and lists of objects are walked. skip names
// the top-level fields whose values are not the synthesiser's to change —
// the operator's — and applies only at the top level, because an operator
// value is a whole value.
func bindReferences(value any, field string, references map[string]string, skip map[string]bool) any {
	if len(references) == 0 {
		return value
	}
	return bindOne(value, field, references, skip, true)
}

func bindOne(value any, field string, references map[string]string, skip map[string]bool, top bool) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if top && skip[k] {
				out[k] = v[k]
				continue
			}
			out[k] = bindOne(v[k], k, references, skip, false)
		}
		return out
	case []any:
		if len(v) > 0 {
			if _, isString := v[0].(string); isString && referenceField(field, true) {
				if path, ok := referencedCollection(field, references); ok {
					return []any{BorrowToken + path}
				}
			}
		}
		out := make([]any, len(v))
		for i := range v {
			out[i] = bindOne(v[i], field, references, skip, false)
		}
		return out
	case string:
		if referenceField(field, false) && !strings.HasPrefix(v, "$") {
			if path, ok := referencedCollection(field, references); ok {
				return BorrowToken + path
			}
		}
		return v
	default:
		return value
	}
}
