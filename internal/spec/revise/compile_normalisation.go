// The normalisation rule of the correction compiler: how the live API
// respells a value, recorded so generated state keeps the configured
// spelling.

package revise

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/correction"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// normalisation compiles a normalisation observation.
//
// The relation between the value sent and the value stored is read back
// from the observation's own excerpts — the create that sent it and the
// read that answered it — and recorded as x-tfpfgen-normalisation, so
// generated state keeps the configured spelling when the answer is the
// stored form of it. A `format: date-time` property whose stored spelling
// is not an RFC 3339 timestamp also loses the format: the document promises
// a wire format the API does not speak, and an SDK built on the promise
// cannot read the answer.
func (c *compiler) normalisation(loc *locator, cls specmodel.Classification, o observe.Observation) compiled {
	site, why, ok := c.property(loc, cls, o)
	if !ok {
		return why
	}
	kind, ok := normalisationKind(o)
	if !ok {
		return unplaceable(fmt.Sprintf("a normalisation observation on %s.%s carries no excerpt pair the sent and stored values can be read from", o.Entity, o.Attribute))
	}
	var operations []correction.Operation
	var parts []string
	if extension := loc.extensionNode(site.property, site.propPtr, specmodel.ExtNormalisation); extension == nil || extension.Value != kind {
		operations = append(operations, correction.Operation{Op: "add", Path: site.propPtr + "/" + specmodel.ExtNormalisation, Value: kind})
		parts = append(parts, fmt.Sprintf("stores the value %s, so generated state keeps the configured spelling (%s)", kind, specmodel.ExtNormalisation))
	}
	spelling, _ := o.Value.(string)
	if formatNode, formatPtr, declared := loc.scalarSite(site.property, site.propPtr, "format"); declared && formatNode.Value == "date-time" && spelling != "" {
		if _, err := time.Parse(time.RFC3339Nano, spelling); err != nil {
			operations = append(operations,
				correction.Operation{Op: "test", Path: formatPtr, Value: "date-time"},
				correction.Operation{Op: "remove", Path: formatPtr},
			)
			parts = append(parts, fmt.Sprintf("answers the property as %q, which is not an RFC 3339 timestamp, so the declared date-time format is withdrawn and the property is a plain string", spelling))
		}
	}
	if len(operations) == 0 {
		return stated(fmt.Sprintf("the document already declares %s", specmodel.ExtNormalisation))
	}
	return compiled{
		operations: operations,
		justification: fmt.Sprintf("the audit confirmed a normalisation observation on %s.%s: the live API %s",
			o.Entity, o.Attribute, strings.Join(parts, "; ")),
	}
}

// normalisationKind reads the relation between what was sent and what was
// stored out of the observation's excerpts: the value the first request
// carried for the attribute against the value the last response answered.
func normalisationKind(o observe.Observation) (string, bool) {
	var sent, got any
	haveSent, haveGot := false, false
	for _, excerpt := range o.Excerpts {
		if !haveSent && len(excerpt.RequestFragment) > 0 {
			var request map[string]any
			if json.Unmarshal(excerpt.RequestFragment, &request) == nil {
				if v, ok := request[o.Attribute]; ok {
					sent, haveSent = v, true
				}
			}
		}
		if len(excerpt.ResponseFragment) > 0 {
			var response map[string]any
			if json.Unmarshal(excerpt.ResponseFragment, &response) == nil {
				if v, ok := response[o.Attribute]; ok {
					got, haveGot = v, true
				}
			}
		}
	}
	if !haveSent || !haveGot {
		return "", false
	}
	kind, _, ok := observe.Normalisation(sent, got)
	return kind, ok
}
