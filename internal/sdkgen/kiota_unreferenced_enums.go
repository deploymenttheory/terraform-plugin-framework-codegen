package sdkgen

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// kiotaEnumDeclaration recognises the enum type kiota mints for an inline
// enum: an int-backed type whose Parse companion sits in the same file.
var kiotaEnumDeclaration = regexp.MustCompile(`(?m)^type (\w+) int$`)

// refuseUnreferencedRequestBodyEnums fails the generation when kiota
// emitted an enum type for a request-body property that no other file in
// the SDK carries.
//
// That shape is the trace of a kiota defect: given two operations whose
// bodies declare same-named inline enums, kiota drops the property from
// every request model, still emits the enum types, and prints no warning.
// Prenormalisation extracts inline request-body enums to named components
// so the defect has nothing to bite on; an unreferenced enum appearing
// anyway means a document shape the extraction did not reach, and a
// provider silently missing a writable property is worse than no SDK — so
// the run refuses, naming every property.
func refuseUnreferencedRequestBodyEnums(outDir string) error {
	sources := map[string][]byte{}
	err := filepath.WalkDir(outDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		source, err := os.ReadFile(path) //nolint:gosec // a .go file under the staging dir this run wrote
		if err != nil {
			return err
		}
		sources[path] = source
		return nil
	})
	if err != nil {
		return err
	}

	var dropped []string
	for path, source := range sources {
		if !strings.Contains(filepath.Base(path), "_request_body") {
			continue
		}
		for _, match := range kiotaEnumDeclaration.FindAllSubmatch(source, -1) {
			typeName := match[1]
			if !bytes.Contains(source, append([]byte("func Parse"), typeName...)) {
				continue
			}
			if referencedElsewhere(sources, path, typeName) {
				continue
			}
			relative, relErr := filepath.Rel(outDir, path)
			if relErr != nil {
				relative = path
			}
			dropped = append(dropped, fmt.Sprintf("%s (%s)", typeName, relative))
		}
	}
	if len(dropped) == 0 {
		return nil
	}

	sort.Strings(dropped)
	return fmt.Errorf(
		"kiota dropped %d request-body propert%s: each of these enum types was generated and no request model carries it:\n  %s\n"+
			"the document declares the property writable, so a provider generated from this SDK would silently lack it; "+
			"extend the request-body enum extraction in prenormalisation to reach the shape that produced this",
		len(dropped), pluralYIes(len(dropped)), strings.Join(dropped, "\n  "))
}

// referencedElsewhere reports whether any file other than the declaring one
// mentions the type name.
func referencedElsewhere(sources map[string][]byte, declaringPath string, typeName []byte) bool {
	for path, source := range sources {
		if path == declaringPath {
			continue
		}
		if bytes.Contains(source, typeName) {
			return true
		}
	}
	return false
}

// pluralYIes answers the y/ies ending for "property".
func pluralYIes(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
