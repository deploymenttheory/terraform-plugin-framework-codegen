package emit

import (
	"fmt"
	"sort"
	"strings"
)

// Registry carries the import and registration lines `provider generate`
// registers into the provider-core registry files, one set per registry
// slot.
type Registry struct {
	Resources     Registrations
	Datasources   Registrations
	ListResources Registrations
	Actions       Registrations
}

// Registrations pairs one slot's aliased import lines with its
// constructor registration lines.
type Registrations struct {
	// Imports are finished aliased import lines, e.g.
	// `testsV7HTTPServer "example.com/mod/internal/services/resources/tests/v7/http_server"`.
	Imports []string
	// Registrations are finished constructor entries, e.g.
	// `testsV7HTTPServer.NewHTTPServerResource,`.
	Registrations []string
}

// add records one entity's pair of lines.
func (s *Registrations) add(importLine, registrationLine string) {
	s.Imports = append(s.Imports, importLine)
	s.Registrations = append(s.Registrations, registrationLine)
}

// RegistrySlots are the registry sentinel spellings, in the fixed order
// the glossary declares them.
var RegistrySlots = []string{"resources", "datasources", "list_resources", "actions"}

// ByKind returns the set for one sentinel kind.
func (r Registry) ByKind(kind string) (Registrations, error) {
	switch kind {
	case "resources":
		return r.Resources, nil
	case "datasources":
		return r.Datasources, nil
	case "list_resources":
		return r.ListResources, nil
	case "actions":
		return r.Actions, nil
	}
	return Registrations{}, fmt.Errorf("%q is not a registry sentinel kind (%s)", kind, strings.Join(RegistrySlots, " | "))
}

// Register replaces the machine-maintained lines at one kind's sentinels in
// a provider-core registry file: the block after `// tfpfgen:<kind>:imports`
// and the block after `// tfpfgen:<kind>:registrations`.
//
// The operation is idempotent and total: whatever generated lines sat
// after a sentinel are replaced wholesale by the given set — sorted,
// de-duplicated — so re-registering the same set is a no-op and a renamed
// entity cannot leave its old registration behind. Every other line,
// sentinels included, is preserved byte for byte. A registry file whose
// sentinel is missing is an error, not a silent skip: the sentinel is the
// contract that makes the file machine-maintainable.
func Register(content []byte, kind string, set Registrations) ([]byte, error) {
	if _, err := (Registry{}).ByKind(kind); err != nil {
		return nil, err
	}

	out := string(content)
	var err error
	out, err = registerSentinel(out, "// tfpfgen:"+kind+":imports", set.Imports)
	if err != nil {
		return nil, err
	}
	out, err = registerSentinel(out, "// tfpfgen:"+kind+":registrations", set.Registrations)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// registerSentinel replaces the generated block following one sentinel: the
// lines between the sentinel and the first following line whose content is
// a lone closing delimiter.
func registerSentinel(content, sentinel string, lines []string) (string, error) {
	rows := strings.Split(content, "\n")

	at := -1
	for i, row := range rows {
		if strings.TrimSpace(row) == sentinel {
			at = i
			break
		}
	}
	if at == -1 {
		return "", fmt.Errorf("the registry file carries no %q sentinel; regenerate the provider core", sentinel)
	}

	indent := leadingWhitespace(rows[at])
	end := at + 1
	for end < len(rows) && !closesBlock(rows[end]) {
		end++
	}
	if end == len(rows) {
		return "", fmt.Errorf("no closing delimiter follows the %q sentinel", sentinel)
	}

	deduped := map[string]bool{}
	var block []string
	for _, line := range lines {
		if line == "" || deduped[line] {
			continue
		}
		deduped[line] = true
		block = append(block, indent+line)
	}
	sort.Strings(block)

	out := append([]string{}, rows[:at+1]...)
	out = append(out, block...)
	out = append(out, rows[end:]...)
	return strings.Join(out, "\n"), nil
}

// closesBlock reports whether a row is the delimiter that ends a sentinel
// block: a lone `)` or `}` at any indentation.
func closesBlock(row string) bool {
	t := strings.TrimSpace(row)
	return t == ")" || t == "}" || t == "},"
}

// leadingWhitespace is a row's indentation prefix.
func leadingWhitespace(row string) string {
	return row[:len(row)-len(strings.TrimLeft(row, " \t"))]
}
