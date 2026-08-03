package sdkbind

import (
	"fmt"
	"go/types"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
)

// StaticFacts derives probe facts from the SDK's own types, with no HTTP.
//
// The one fact this produces today is zeroValueUnsendable: a value-typed scalar
// tagged `omitempty` drops its zero before it travels, so a configured false, 0
// or "" silently becomes an omission. The classic-tests wave met this live --
// networkMeasurements false could never reach the API -- as an inconsistent-apply
// error at acceptance time, when the evidence had been sitting in the SDK's
// struct tags all along.
//
// The claim is mechanical and re-derivable from the pinned SDK, which is why the
// facts carry Corroborated: nothing about a live tenant could contradict what the
// encoder demonstrably does. Their evidence cites source, not cassette
// interactions, so they must live in their own committed file rather than inside
// a snapshot -- replay purity is asserted for snapshot facts only.
func StaticFacts(l *Loader, bp blueprint.Blueprint) ([]probe.Fact, error) {
	var facts []probe.Fact

	for _, res := range bp.Resources {
		svc := res.Binding.Service

		request := typeNameOf(res.Binding.Body.RequestType)
		update := ""
		if res.Binding.Body.SplitsUpdateBody() {
			update = typeNameOf(res.Binding.Body.UpdateRequestType)
		}
		if request == "" {
			continue
		}

		for _, a := range res.Schema.Attributes {
			if a.Drop || a.Wire.SkipExpand || a.Wire.Expand == nil || a.Wire.SDKField == "" {
				continue
			}

			ev, ok, err := zeroUnsendableOn(l, svc.ImportPath, request, a.Wire.SDKField)
			if err != nil {
				// Absent or unresolvable fields are bindings problems with their own
				// reporting; a facts pass repeating them would double every miss.
				continue
			}

			// A split update body must agree before the claim is made about the
			// attribute: if either path can send the zero, "unsendable" is a lie on
			// that path, and the fact has no per-operation home.
			if ok && update != "" {
				evU, okU, errU := zeroUnsendableOn(l, svc.ImportPath, update, a.Wire.SDKField)
				if errU != nil || !okU {
					continue
				}
				ev = append(ev, evU...)
			}

			if !ok {
				continue
			}

			facts = append(facts, probe.Fact{
				Resource:   res.Key,
				JSONPath:   a.Wire.JSONPath,
				Field:      probe.FactZeroValueUnsendable,
				Value:      probe.BoolValue(true),
				Confidence: probe.Corroborated,
				Probe:      "static.sdkbind",
				Evidence:   ev,
				Rationale: "the SDK declares this field value-typed with omitempty, so its " +
					"zero value is dropped by the encoder before any request is sent",
			})
		}
	}

	probe.SortFacts(facts)

	return facts, nil
}

// zeroUnsendableOn reports whether typeName's field drops its zero value: a
// value-typed scalar (possibly named, never a pointer) whose json tag says
// omitempty. A pointer field can express "explicitly zero" as a non-nil pointer,
// so it is never unsendable; a collection's nil-versus-empty distinction is a
// different subject and out of scope here.
func zeroUnsendableOn(
	l *Loader,
	importPath, typeName, field string,
) (evidence []string, ok bool, err error) {
	named, err := l.LookupType(importPath, typeName)
	if err != nil {
		return nil, false, err
	}

	st, err := structUnder(named)
	if err != nil {
		return nil, false, err
	}

	f, tag, found := fieldWithTag(st, field)
	if !found {
		return nil, false, fmt.Errorf("%w: %s has no field %q", ErrBindings, typeName, field)
	}

	jsonTag := tagValue(tag, "json")
	if !strings.Contains(jsonTag, ",omitempty") {
		return nil, false, nil
	}

	if _, isPointer := f.Type().(*types.Pointer); isPointer {
		return nil, false, nil
	}

	basic, isBasic := f.Type().Underlying().(*types.Basic)
	if !isBasic || basic.Info()&(types.IsBoolean|types.IsNumeric|types.IsString) == 0 {
		return nil, false, nil
	}

	return []string{fmt.Sprintf("static:%s.%s.%s json:%q", pkgName(importPath), typeName,
		field, jsonTag)}, true, nil
}

// fieldWithTag is fieldByName plus the struct tag, which the verify path never needed.
func fieldWithTag(st *types.Struct, name string) (*types.Var, string, bool) {
	for i := range st.NumFields() {
		if f := st.Field(i); f.Name() == name {
			return f, st.Tag(i), true
		}
	}
	return nil, "", false
}

// tagValue extracts one key's value from a struct tag without reflect.StructTag,
// which panics on malformed tags a generated SDK could legally carry.
func tagValue(tag, key string) string {
	for tag != "" {
		// Skip leading space.
		i := 0
		for i < len(tag) && tag[i] == ' ' {
			i++
		}
		tag = tag[i:]
		if tag == "" {
			break
		}

		// Scan to the colon.
		i = 0
		for i < len(tag) && tag[i] != ':' && tag[i] != ' ' && tag[i] != '"' {
			i++
		}
		if i+1 >= len(tag) || tag[i] != ':' || tag[i+1] != '"' {
			break
		}
		name := tag[:i]
		tag = tag[i+1:]

		// Scan the quoted value.
		i = 1
		for i < len(tag) && tag[i] != '"' {
			if tag[i] == '\\' {
				i++
			}
			i++
		}
		if i >= len(tag) {
			break
		}
		value := tag[1:i]
		tag = tag[i+1:]

		if name == key {
			return value
		}
	}
	return ""
}

// pkgName is the last element of an import path, which is how the evidence reads
// in an error message a human follows back to source.
func pkgName(importPath string) string {
	if i := strings.LastIndex(importPath, "/"); i >= 0 {
		return importPath[i+1:]
	}
	return importPath
}
