package intermediate_representation

import (
	"regexp"
	"strings"
	"unicode"
)

// Names is the naming block computed once per entity, so every consumer —
// emitter, binders, docs — spells the same thing the same way.
type Names struct {
	// Key is the snake_case entity key, the sort key for every slice in
	// the model. The API version segment is factored out into
	// APIVersionDirectory rather than living in the name.
	Key string `json:"key"`
	// PascalCase is the exported Go type spelling, e.g. "HTTPServer".
	PascalCase string `json:"pascal"`
	// CamelCase is the unexported Go spelling, e.g. "httpServer".
	CamelCase string `json:"camel"`
	// TerraformType is "<provider>_<key>".
	TerraformType string `json:"terraform_type"`
	// Package is the Go package name: the key with its underscores
	// removed, per Go convention.
	Package string `json:"package"`
	// Service is the service area: the first path segment with any
	// version prefix stripped, e.g. "/v7/tests/http-server" -> "tests".
	Service string `json:"service"`
	// APIVersionDirectory is the stripped version segment, "v1" when the path
	// declares none.
	APIVersionDirectory string `json:"api_version_directory"`
	// Tag is the group the document places the entity in, empty when it
	// declares none. Unlike Service, which this package derives from the
	// path, it is the vendor's own grouping and is carried rather than
	// computed.
	Tag string `json:"tag,omitempty"`
}

// acronyms is the closed set of initialisms Go spellings uppercase whole,
// keyed by their snake_case form, sorted. This table is owner-owned:
// additions go through the repository owner, never in passing.
var acronyms = map[string]string{
	"acl":   "ACL",
	"api":   "API",
	"arn":   "ARN",
	"cidr":  "CIDR",
	"cli":   "CLI",
	"cpu":   "CPU",
	"css":   "CSS",
	"dns":   "DNS",
	"ftp":   "FTP",
	"gpu":   "GPU",
	"html":  "HTML",
	"http":  "HTTP",
	"https": "HTTPS",
	"id":    "ID",
	"ip":    "IP",
	"json":  "JSON",
	"jwt":   "JWT",
	"lan":   "LAN",
	"mac":   "MAC",
	"oauth": "OAuth",
	"os":    "OS",
	"ram":   "RAM",
	"sdk":   "SDK",
	"smtp":  "SMTP",
	"sql":   "SQL",
	"ssh":   "SSH",
	"ssl":   "SSL",
	"tcp":   "TCP",
	"tls":   "TLS",
	"udp":   "UDP",
	"ui":    "UI",
	"uri":   "URI",
	"url":   "URL",
	"uuid":  "UUID",
	"vpn":   "VPN",
	"wan":   "WAN",
	"xml":   "XML",
	"yaml":  "YAML",
}

// versionSegment matches a leading API version path segment such as "v7".
var versionSegment = regexp.MustCompile(`^v\d+$`)

// deriveNames computes the naming block from the classification key and
// the collection path the key was derived from.
func deriveNames(provider, key, collectionPath, tag string) Names {
	version, service := "", ""
	first := true
	for _, segment := range strings.Split(strings.Trim(collectionPath, "/"), "/") {
		if segment == "" || strings.HasPrefix(segment, "{") {
			continue
		}
		if first && versionSegment.MatchString(strings.ToLower(segment)) {
			version = strings.ToLower(segment)
			first = false
			continue
		}
		first = false
		if service == "" {
			service = strings.ToLower(strings.ReplaceAll(segment, "-", "_"))
		}
	}

	lowered := strings.ToLower(key)
	if version != "" {
		lowered = strings.TrimPrefix(lowered, version+"_")
	}
	if service == "" {
		service = lowered
	}
	if version == "" {
		version = "v1"
	}

	names := Names{Service: service, APIVersionDirectory: version, Tag: tag}
	return names.withKey(provider, lowered)
}

// withKey rebuilds every key-derived field of the naming block around a
// replacement key, leaving the path-derived fields alone. Disambiguation
// renames an entity after its names were first computed; this keeps every
// spelling in step with the final key.
func (names Names) withKey(provider, key string) Names {
	names.Key = key
	names.PascalCase = pascalCase(key)
	names.CamelCase = camelCase(key)
	names.TerraformType = provider + "_" + key
	names.Package = packageName(provider, key)
	return names
}

// packageName is the Go package name for one entity: the provider name and
// the entity key, both stripped of the punctuation a Go identifier may not
// carry, per the convention that a package name is one lower-case word.
//
// The provider prefix is not decoration. Without it the key alone names the
// package, and a key is whatever the document's path segments spell — which
// includes Go's reserved words. An entity keyed "package" produced `package
// package`, and the failure surfaced nowhere near its cause, as a template
// "rendering Go that does not parse". No reserved word begins with a provider
// name, so prefixing removes the whole class rather than escaping one case of
// it, and it makes the package a generated file imports unmistakable.
func packageName(provider, key string) string {
	return identifierWord(provider) + identifierWord(key)
}

// identifierWord reduces a name to the lower-case letters and digits a Go
// identifier may carry, dropping the separators a provider name or an entity
// key is allowed to use.
func identifierWord(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// GoName is pascalCase for consumers outside the derivation: the emitter
// spells model field names and type names from attribute keys, and it must
// spell them exactly the way the derivation spells entity names — same
// acronym table, same casing — or the two drift apart file by file.
func GoName(snake string) string {
	return pascalCase(snake)
}

// TerraformName is snakeCase for consumers outside the derivation: the emitter
// derives terraform spellings for names the model does not carry
// pre-spelled, such as an action's path parameters.
func TerraformName(wire string) string {
	return snakeCase(wire)
}

// pascalCase turns a snake_case key into its exported Go spelling. Parts
// in the acronym table uppercase whole: "http_server" is "HTTPServer",
// never "HttpServer".
func pascalCase(key string) string {
	var b strings.Builder
	for _, part := range strings.Split(key, "_") {
		if part == "" {
			continue
		}
		if a, ok := acronyms[part]; ok {
			b.WriteString(a)
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

// camelCase turns a snake_case key into its unexported Go spelling: the
// leading part stays lowercase whole — an acronym there lowers entirely,
// "id" stays "id" and "api_key" becomes "apiKey" — and the rest follows
// the Pascal rules.
func camelCase(key string) string {
	parts := strings.Split(key, "_")
	for len(parts) > 0 && parts[0] == "" {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return ""
	}
	return parts[0] + pascalCase(strings.Join(parts[1:], "_"))
}

// snakeCase turns a wire property name — camelCase, PascalCase, kebab-case
// or dotted — into the terraform attribute spelling. Acronym runs keep
// their shape: "IPAddress" becomes "ip_address", not "i_p_address".
func snakeCase(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, rune := range runes {
		switch {
		case rune == '-' || rune == '.' || rune == ' ':
			b.WriteRune('_')
		case unicode.IsUpper(rune):
			if i > 0 && boundaryBefore(runes, i) {
				b.WriteRune('_')
			}
			b.WriteRune(unicode.ToLower(rune))
		default:
			b.WriteRune(rune)
		}
	}
	return leadWithALetter(b.String())
}

// leadWithALetter trims what a terraform attribute name may not begin with.
//
// The framework's own name rule admits a leading underscore, but the reflect
// layer that decodes a model does not: a tfsdk tag "must only use lowercase
// letters, underscores, and numbers, and must start with a letter". An API
// property named _links reaches the schema, and the provider then fails to
// decode any object carrying it.
//
// Trimmed rather than refused, because the name is the only thing wrong with
// the attribute and the value behind it is ordinary.
func leadWithALetter(name string) string {
	trimmed := strings.TrimLeft(name, "_0123456789")
	if trimmed == "" {
		return name
	}
	return trimmed
}

// boundaryBefore reports whether a word boundary sits before the upper-case
// rune at i: after a lower-case rune or digit, or where an acronym run ends
// because a lower-case rune follows.
func boundaryBefore(runes []rune, i int) bool {
	previous := runes[i-1]
	if unicode.IsLower(previous) || unicode.IsDigit(previous) {
		return true
	}
	return unicode.IsUpper(previous) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
}
