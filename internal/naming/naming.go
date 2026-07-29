// Package naming turns names from an API specification into the identifiers a
// generated provider needs: Go types and fields, Terraform attribute and
// resource type names, package directories and import aliases.
//
// It is deliberately a separate package with no dependency on the IR. Naming is
// where a generator quietly produces code that does not compile — a field
// colliding with an imported package, an acronym run split wrongly, an
// identifier starting with a digit — and isolating it makes those cases
// table-testable rather than discovered at build time.
//
// Every conversion funnels through SplitWords, so a name is decomposed exactly
// once and the various spellings are only different joins of the same words.
// That is what keeps TerraformName and GoFieldName from disagreeing about where
// a word boundary falls, which is the usual source of a model field whose tfsdk
// tag does not match its schema attribute.
//
// The rules are ported from go-sdk-thousandeyes/internal/codegen with two
// substantive changes. Initialisms are applied per word rather than only to a
// trailing one, so "apiEndpoint" becomes APIEndpoint rather than ApiEndpoint.
// And the reserved-identifier set is much wider: a generated provider's function
// bodies live alongside imports named resource, schema, types, path and convert,
// and shadowing any of them breaks the file.
//
// Names that must match an existing SDK symbol are never derived here. They come
// from internal/sdkbind, which reads the SDK's real syntax tree, because guessing
// a symbol's spelling from a naming rule is how a generator produces code that
// refers to functions that do not exist.
package naming

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// nonAlphanumeric matches every run that cannot appear in a Go identifier.
var nonAlphanumeric = regexp.MustCompile(`[^A-Za-z0-9]+`)

// Initialisms are the acronyms Go convention fully capitalises.
//
// Order matters: a longer entry must precede any entry that is a prefix or
// suffix of it, so UUID is matched before ID.
var Initialisms = []string{
	"HTTPS", "HTTP", "JSON", "UUID", "YAML", "CIDR", "CSV", "XML", "SKU",
	"API", "ASN", "BGP", "CPU", "DNS", "FTP", "MTU", "RAM", "SIP", "SSL",
	"TCP", "TLS", "UDP", "URI", "URL", "VPN", "ARN", "AID", "ID", "IP", "OS",
}

// reserved are identifiers a generated local variable must not take.
//
// The first two groups are Go's keywords and predeclared identifiers. The third
// is the harder one, and the reason this set is wider than the SDK generator's:
// every generated resource file imports packages named resource, schema, types,
// path, timeouts, convert, crud and client, and a local variable shadowing one of
// them breaks the file in a way that reads as a template bug rather than a
// naming bug.
//
// This is not hypothetical. The archetype provider's own resource template
// contains `resource, err := r.client...`, shadowing the imported resource
// package, and does not compile.
var reserved = map[string]bool{
	// Keywords.
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,

	// Predeclared identifiers.
	"any": true, "append": true, "bool": true, "byte": true, "cap": true,
	"clear": true, "close": true, "complex": true, "copy": true, "delete": true,
	"error": true, "false": true, "float32": true, "float64": true, "int": true,
	"int32": true, "int64": true, "len": true, "make": true, "max": true,
	"min": true, "new": true, "nil": true, "panic": true, "print": true,
	"println": true, "recover": true, "rune": true, "string": true, "true": true,
	"uint": true, "uint64": true,

	// Package names imported by every generated resource file.
	"client": true, "convert": true, "crud": true, "datasource": true,
	"diag": true, "errors": true, "fmt": true, "path": true, "planmodifier": true,
	"provider": true, "resource": true, "schema": true, "tflog": true,
	"timeouts": true, "types": true, "validator": true,

	// Local variables the generated CRUD skeleton always declares.
	"cancel": true, "ctx": true, "data": true, "diags": true, "endpoint": true,
	"err": true, "httpResp": true, "object": true, "opts": true, "plan": true,
	"r": true, "req": true, "requestBody": true, "remoteResource": true,
	"resp": true, "result": true, "state": true,
}

// IsReserved reports whether s may not be used as a generated identifier.
func IsReserved(s string) bool { return reserved[s] }

// Options configures the per-target naming rules.
type Options struct {
	// StripPrefix removes a specification's component namespace from generated
	// type names. The ThousandEyes unified document prefixes every component
	// with its source domain ("Tests_API_BgpTest") because it merges 27
	// per-domain specifications, so the same concept appears once per domain and
	// the prefix carries no information inside a package that already belongs to
	// that domain. Most documents have no such convention, so this is data
	// rather than a hardcoded pattern.
	StripPrefix *regexp.Regexp

	// ExtraInitialisms extends Initialisms for one target, so a domain acronym
	// does not require changing this package.
	ExtraInitialisms []string
}

// DefaultStripPrefix matches the "<Domain>_API_" namespace convention.
var DefaultStripPrefix = regexp.MustCompile(`^[A-Za-z0-9]+(_[A-Za-z0-9]+)*_API_`)

// SplitWords decomposes a name into its lower-case words. It is the single
// source of truth for where a word boundary falls, and every other conversion in
// this package is a different join of its output.
//
// Boundaries are inserted at every non-alphanumeric run, before an upper-case
// letter following a lower-case letter or digit, and before the final upper-case
// letter of an acronym run that is followed by a lower-case letter. That last
// rule is what makes "HTTPProxy" split as [http, proxy] rather than as one word
// or as four letters.
//
//	"displayName" -> [display name]
//	"HTTPProxy"   -> [http proxy]
//	"iOSVersion"  -> [i os version]
//	"ipv6Address" -> [ipv6 address]
//	"_links"      -> [links]
//	"tag.colour"   -> [tag colour]
func SplitWords(s string) []string {
	// Normalise every non-alphanumeric run to a single separator so dotted
	// paths, hyphens and HAL underscores all funnel into one code path.
	s = strings.Trim(nonAlphanumeric.ReplaceAllString(s, "_"), "_")
	if s == "" {
		return nil
	}

	var (
		words []string
		cur   strings.Builder
	)
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}

	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == '_':
			flush()
			continue

		case unicode.IsUpper(r):
			prevIsLowerOrDigit := i > 0 &&
				(unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]))
			// The tail of an acronym run starts a new word when the next rune
			// is lower-case: "HTTPProxy" breaks at the second P.
			startsNewWord := i > 0 && i+1 < len(runes) &&
				unicode.IsUpper(runes[i-1]) && unicode.IsLower(runes[i+1])

			if prevIsLowerOrDigit || startsNewWord {
				flush()
			}
		}

		cur.WriteRune(unicode.ToLower(r))
	}
	flush()

	return words
}

// TerraformName converts a JSON property name into a Terraform attribute name.
//
//	"displayName" -> "display_name"
//	"legacyId"    -> "legacy_id"
//	"HTTPProxy"   -> "http_proxy"
//	"_links"      -> "links"
func TerraformName(s string) string {
	return strings.Join(SplitWords(s), "_")
}

// exported joins words into an exported Go identifier, upper-casing any word
// that is an initialism. It never returns the empty string, and never returns an
// identifier beginning with a digit.
func exported(words []string, initialisms []string) string {
	var b strings.Builder

	for _, w := range words {
		if up, ok := matchInitialism(w, initialisms); ok {
			b.WriteString(up)
			continue
		}
		b.WriteString(strings.ToUpper(w[:1]))
		if len(w) > 1 {
			b.WriteString(w[1:])
		}
	}

	out := b.String()
	switch {
	case out == "":
		return "Value"
	case out[0] >= '0' && out[0] <= '9':
		// A Go identifier cannot begin with a digit.
		return "N" + out
	default:
		return out
	}
}

func matchInitialism(word string, initialisms []string) (string, bool) {
	for _, in := range initialisms {
		if strings.EqualFold(word, in) {
			return in, true
		}
		// A pluralised initialism keeps its lower-case s: Go convention is
		// LabelIDs and URLs, not LabelIDS or URLS. Without this, "labelIds"
		// becomes LabelIds, which reads as a typo in generated code.
		if len(word) == len(in)+1 && strings.EqualFold(word[:len(in)], in) &&
			(word[len(word)-1] == 's' || word[len(word)-1] == 'S') {
			return in + "s", true
		}
	}
	return "", false
}

func (o Options) initialisms() []string {
	if len(o.ExtraInitialisms) == 0 {
		return Initialisms
	}
	// Target-specific entries win, so a target can override a default.
	out := make([]string, 0, len(Initialisms)+len(o.ExtraInitialisms))
	out = append(out, o.ExtraInitialisms...)
	out = append(out, Initialisms...)
	return out
}

// GoTypeName converts a specification component name into an exported Go type
// name, stripping the specification's component namespace first.
//
//	"Tests_API_BgpTest" -> "BGPTest"
//	"tag"               -> "Tag"
func (o Options) GoTypeName(s string) string {
	if o.StripPrefix != nil {
		s = o.StripPrefix.ReplaceAllString(s, "")
	}
	return exported(SplitWords(s), o.initialisms())
}

// GoFieldName converts a JSON property name into an exported Go field name.
//
// It does not strip the component namespace: that convention applies to type
// names, and a property is never namespaced.
//
//	"legacyId"    -> "LegacyID"
//	"apiEndpoint" -> "APIEndpoint"
//	"displayName" -> "DisplayName"
func (o Options) GoFieldName(s string) string {
	return exported(SplitWords(s), o.initialisms())
}

// SafeIdentifier returns an identifier safe to use as a local variable or
// parameter, suffixing anything reserved rather than silently shadowing it.
func SafeIdentifier(s string) string {
	if s == "" {
		return "value"
	}
	if IsReserved(s) {
		return s + "Value"
	}
	return s
}

// SnakeDirName converts a name into the snake_case directory and package name a
// generated resource lives in. It never returns a name a Go package cannot use.
func SnakeDirName(s string) string {
	out := TerraformName(s)
	if out == "" {
		return "misc"
	}
	if out[0] >= '0' && out[0] <= '9' {
		return "v" + out
	}
	return out
}

// TerraformTypeName builds the registry-visible resource type name. Empty
// components are skipped, so a resource with no service group does not produce a
// doubled separator, which would be an invalid type name.
//
//	("thousandeyes", "tag")                  -> "thousandeyes_tag"
//	("thousandeyes", "tests", "httpServer")  -> "thousandeyes_tests_http_server"
func TerraformTypeName(providerPrefix string, parts ...string) string {
	out := make([]string, 0, len(parts)+1)
	if n := TerraformName(providerPrefix); n != "" {
		out = append(out, n)
	}
	for _, p := range parts {
		if n := TerraformName(p); n != "" {
			out = append(out, n)
		}
	}
	return strings.Join(out, "_")
}

// PackageAlias builds the lowerCamel import alias a generated provider uses to
// disambiguate one resource package from another. Aliases must be unique across
// the whole provider, which Unique enforces.
//
//	("v7", "tag")              -> "v7Tag"
//	("v7", "http_server_test") -> "v7HTTPServerTest"
func (o Options) PackageAlias(parts ...string) string {
	var b strings.Builder

	for i, p := range parts {
		words := SplitWords(p)
		if len(words) == 0 {
			continue
		}
		id := exported(words, o.initialisms())
		if i == 0 {
			b.WriteString(lowerFirst(id))
			continue
		}
		b.WriteString(id)
	}

	out := b.String()
	if out == "" {
		return "pkg"
	}
	// An alias colliding with a keyword or an imported package name would break
	// every file that used it.
	return SafeIdentifier(out)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	// Leave an acronym run alone: lowering only the first rune of "ID" gives
	// "iD", which reads as a typo.
	if len(r) > 1 && unicode.IsUpper(r[0]) && unicode.IsUpper(r[1]) {
		return s
	}
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// Unique returns want if it is free, or want with the smallest numeric suffix
// that is free, recording the result in taken.
//
// Collisions are resolved here, on names, before anything is rendered. The SDK
// generator resolves them afterwards by rewriting formatted Go source with
// regular expressions, and its own comments record how fragile that is; keeping
// it at the naming layer removes the entire class of bug.
func Unique(taken map[string]bool, want string) string {
	if want == "" {
		want = "value"
	}
	if !taken[want] {
		taken[want] = true
		return want
	}
	for n := 2; ; n++ {
		candidate := want + strconv.Itoa(n)
		if !taken[candidate] {
			taken[candidate] = true
			return candidate
		}
	}
}
