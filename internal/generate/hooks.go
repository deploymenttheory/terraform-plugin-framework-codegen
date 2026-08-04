package generate

import (
	"fmt"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// The framework packages a resource-level cross-attribute rule needs.
const (
	pkgResourceValidator = validatorPkgRoot + "resourcevalidator"
)

// configValidatorFunc maps a rule kind to its resourcevalidator constructor.
//
// The absence of an entry is the refusal, and blueprint.Validate reports it first -- this map
// is the second half of the same statement, so the two cannot drift into disagreeing about
// which rules exist.
var configValidatorFunc = map[blueprint.ConfigValidatorKind]string{
	blueprint.ConfigConflicting:      "Conflicting",
	blueprint.ConfigAtLeastOneOf:     "AtLeastOneOf",
	blueprint.ConfigExactlyOneOf:     "ExactlyOneOf",
	blueprint.ConfigRequiredTogether: "RequiredTogether",
}

// configValidators renders a resource's cross-attribute rules.
//
// Each becomes one resourcevalidator call over path.MatchRoot expressions. They live on the
// resource rather than on an attribute because that is what they are about: a rule relating two
// fields has no single field to hang off, and the diagnostic it produces should name the block.
func configValidators(r blueprint.Resource, imports *importSet) ([]string, error) {
	if len(r.ConfigValidators) == 0 {
		return nil, nil
	}

	out := make([]string, 0, len(r.ConfigValidators))

	for _, cv := range r.ConfigValidators {
		fn, ok := configValidatorFunc[cv.Kind]
		if !ok {
			return nil, &ErrUnsupported{
				What: fmt.Sprintf("config validator of resource %q", r.Key),
				Why:  fmt.Sprintf("rule %q has no resourcevalidator counterpart", cv.Kind),
			}
		}

		paths := make([]string, 0, len(cv.Attributes))
		for _, name := range cv.Attributes {
			paths = append(paths, fmt.Sprintf("path.MatchRoot(%s)", goStringLit(name)))
		}

		out = append(out, fmt.Sprintf("resourcevalidator.%s(%s)", fn, strings.Join(paths, ", ")))
	}

	imports.add(pkgResourceValidator, "")
	imports.add(pkgPath, "")

	return out, nil
}
