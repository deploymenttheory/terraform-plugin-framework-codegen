package emit

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"text/template"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/fixtures"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/templates"
)

// hashHeader renders the shared DO-NOT-EDIT header in its #-commented
// form, from the same partial every template includes — one wording site.
func hashHeader(source string) (string, error) {
	partial, err := fs.ReadFile(templates.ProviderCore, headerPartial)
	if err != nil {
		return "", fmt.Errorf("reading the shared header partial: %w", err)
	}
	t, err := template.New("_header").Parse(string(partial))
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", headerPartial, err)
	}
	var buffer bytes.Buffer
	if err := t.ExecuteTemplate(&buffer, "hashheader", struct{ Source string }{Source: source}); err != nil {
		return "", fmt.Errorf("rendering the hash header: %w", err)
	}
	return buffer.String(), nil
}

// hclBlock renders one terraform block around a fixture body, with the
// shared header above it.
func hclBlock(source, blockHeader, preamble, body string, skips []fixtures.Omission) ([]byte, error) {
	header, err := hashHeader(source)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(header + "\n\n")
	if preamble != "" {
		b.WriteString(preamble + "\n\n")
	}
	b.WriteString(blockHeader + " {\n")
	b.WriteString(body)
	for _, s := range skips {
		fmt.Fprintf(&b, "  # %s skipped: %s\n", s.Name, s.Reason)
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}

// retainedWireNames is the wire names a replayed configuration keeps whether
// or not the API echoed them: the ones the document marks required — a
// create omitting one is refused, so the risk of holding it belongs to the
// API rather than to the generator — and the ones whose state keeps the
// planned value, which no response was ever going to echo.
func retainedWireNames(spec fixtures.Fixture, nodes []node) map[string]bool {
	out := map[string]bool{}
	for _, e := range spec.Entries {
		if e.ComputedOptionalRequired == ir.Required && e.Wire != "" {
			out[e.Wire] = true
		}
	}
	for _, n := range nodes {
		if n.fb != nil && n.fb.KeptFromPlan && n.attribute.WireName != "" {
			out[n.attribute.WireName] = true
		}
	}
	return out
}

// parentBlockDepth bounds how far up a chain of parents a fixture reaches:
// a parent's parent is rendered too, and a document whose paths nest deeper
// than this is one no fixture is going to exercise honestly.
const parentBlockDepth = 4

// parentBlocks is the configuration of the parent a resource addresses, and
// of that parent's own parents: one block per ancestor, each named for its
// entity and minimal. live selects the live suite's spelling — the accepted
// body, invented names, the run suffix — over the unit suite's derivation.
// attribute names the resource's own attribute that takes the parent's id,
// and reference is the expression it takes. Empty when the resource has no
// parent the provider emits, in which case the fixture keeps its invented
// value.
//
// The parent's block is its own minimal configuration: the smallest object
// the API accepts is the one a child needs to exist under, and the values
// are the same ones the parent's own test sends.
func (e *serviceRenderer) parentBlocks(r *ir.Resource, depth int, live bool) (blocks, attribute, reference string) {
	parent := e.resources[r.ParentEntity]
	if parent == nil || depth >= parentBlockDepth {
		return "", "", ""
	}
	attribute = parentAttribute(r)
	if attribute == "" {
		return "", "", ""
	}
	pb := e.bindings.Resources[parent.Names.Key]
	if pb == nil {
		return "", "", ""
	}
	e.parentTypes = append(e.parentTypes, parent.Names.TerraformType)
	nodes := e.joinTree(bindingKindResource, parent.Names.Key, parent.Schema, pb.Fields, addressingNames(parent.Schema,
		parent.Operations.Read, parent.Operations.Create, parent.Operations.Update, parent.Operations.Delete))
	spec := deriveFixtures(parent.Schema, nodes)
	// A replayed body is already the smallest accepted create and renders
	// whole; a derived fixture still selects the required attributes.
	minimal, form := spec, fixtures.ConfigMinimal
	if live {
		accepted, _, replayed := e.acceptedFixtures(parent, spec, nodes)
		minimal = accepted.WithInventedNames().WithRunSuffix()
		if replayed {
			form = fixtures.ConfigMaximal
		}
	}
	above, parentAttr, parentRef := e.parentBlocks(parent, depth+1, live)
	if parentAttr != "" {
		minimal = minimal.WithExpression(parentAttr, parentRef)
	}
	label := parent.Names.Key
	block := fmt.Sprintf("resource %q %q {\n%s}", parent.Names.TerraformType, label, minimal.HCL(form))
	if above != "" {
		block = above + "\n\n" + block
	}
	return block, attribute, parent.Names.TerraformType + "." + label + ".id"
}

// parentAttribute names the root attribute answering the path parameter of
// the immediate parent: the last parameter above the item key, or the last
// parameter of a singleton, whose path names no item of its own.
func parentAttribute(r *ir.Resource) string {
	if r.Operations.Read == nil || r.Schema == nil {
		return ""
	}
	parameters := r.Operations.Read.PathParameters
	if !r.Singleton {
		if len(parameters) < 2 {
			return ""
		}
		parameters = parameters[:len(parameters)-1]
	}
	if len(parameters) == 0 {
		return ""
	}
	last := parameters[len(parameters)-1]
	for _, a := range r.Schema.Attributes {
		if a.Nested != nil || a.Name == idAttributeName {
			continue
		}
		if a.WireName == last.Name || a.Name == ir.TerraformName(last.Name) {
			return a.Name
		}
	}
	return ""
}

// acceptedFixtures replays what the probe got the API to accept, where it
// cleared this entity; replayed reports whether it did.
func (e *serviceRenderer) acceptedFixtures(r *ir.Resource, spec fixtures.Fixture, nodes []node) (accMinimal, accMaximal fixtures.Fixture, replayed bool) {
	accMinimal, accMaximal = spec, spec
	if rec, ok := e.pc.AcceptedRequestBodies[r.Names.Key]; ok {
		required := retainedWireNames(spec, nodes)
		if rec.Minimal != nil {
			accMinimal = spec.FromAcceptedRequestBody(rec.Minimal.Request, rec.Minimal.Response, required)
			replayed = true
		}
		switch {
		case rec.Maximal != nil:
			accMaximal = spec.FromAcceptedRequestBody(rec.Maximal.Request, rec.Maximal.Response, required)
			replayed = true
		case rec.Minimal != nil:
			// No larger create was ever accepted, so the fullest known
			// configuration is the smallest one. Emitting the document's
			// derived maximal instead would put back the invented values the
			// record exists to replace.
			accMaximal = accMinimal
			accMaximal.Omissions = append(accMaximal.Omissions, fixtures.Omission{
				Name:   "(every optional attribute)",
				Reason: "no create larger than the minimal one was accepted, so this configuration is the minimal one",
			})
		}
	}
	return accMinimal, accMaximal, replayed
}

// resourceFixtures emits a resource's terraform fixtures, response
// fixtures and examples.
func (e *serviceRenderer) resourceFixtures(r *ir.Resource, spec fixtures.Fixture, nodes []node, dir string) ([]File, error) {
	key := r.Names.Key
	source := key
	blockHeader := fmt.Sprintf("resource %q %q", r.Names.TerraformType, "test")

	// The unit suite meets a mock built from these same values, so its
	// configuration carries them verbatim. The acceptance suite meets a live
	// API, where a name that is a constant collides with whatever the last
	// run left behind.
	accMinimal, accMaximal, replayed := e.acceptedFixtures(r, spec, nodes)
	liveMinimal := accMinimal.WithInventedNames().WithRunSuffix()
	liveMaximal := accMaximal.WithInventedNames().WithRunSuffix()
	unitMinimal, unitMaximal := spec, spec

	// An object under a parent is configured under that parent's block, and
	// takes its identifier from it: an invented parent id addresses nothing,
	// live or mocked.
	livePreamble, unitPreamble := fixtures.RunSuffixBlock, ""
	// A timestamp the API wanted ahead of the request is taken from a
	// time_offset block: the record's value was ahead of the run that made
	// it, not of the run that replays it.
	e.timeOffsets = false
	if rec, ok := e.pc.AcceptedRequestBodies[r.Names.Key]; ok {
		var rewritten []string
		if rec.Minimal != nil {
			liveMinimal, rewritten = liveMinimal.WithFutureDates(rec.Minimal.FutureDates)
		}
		if rec.Maximal != nil {
			var more []string
			liveMaximal, more = liveMaximal.WithFutureDates(rec.Maximal.FutureDates)
			for _, name := range more {
				if !slices.Contains(rewritten, name) {
					rewritten = append(rewritten, name)
				}
			}
		} else if rec.Minimal != nil {
			liveMaximal, _ = liveMaximal.WithFutureDates(rec.Minimal.FutureDates)
		}
		for _, name := range rewritten {
			livePreamble += "\n\n" + fixtures.FutureDateBlock(name)
			e.timeOffsets = true
		}
	}
	e.parentTypes = nil
	if parents, attribute, reference := e.parentBlocks(r, 0, true); parents != "" {
		liveMinimal = liveMinimal.WithExpression(attribute, reference)
		liveMaximal = liveMaximal.WithExpression(attribute, reference)
		livePreamble += "\n\n" + parents
	}
	e.parentTypes = nil
	if parents, attribute, reference := e.parentBlocks(r, 0, false); parents != "" {
		unitMinimal = unitMinimal.WithExpression(attribute, reference)
		unitMaximal = unitMaximal.WithExpression(attribute, reference)
		unitPreamble = parents
	}
	suites := []struct {
		name     string
		minimal  fixtures.Fixture
		maximal  fixtures.Fixture
		preamble string
	}{
		{name: "unit", minimal: unitMinimal, maximal: unitMaximal, preamble: unitPreamble},
		{name: "acceptance", minimal: liveMinimal, maximal: liveMaximal, preamble: livePreamble},
	}

	var files []File
	for _, suite := range suites {
		// A replayed body is already the smallest and fullest accepted create,
		// so it renders whole; a derived fixture still selects by presence.
		minForm, maxForm := fixtures.ConfigMinimal, fixtures.ConfigMaximal
		if suite.name == "acceptance" && replayed {
			minForm, maxForm = fixtures.ConfigMaximal, fixtures.ConfigMaximal
		}
		minimal, err := hclBlock(source, blockHeader, suite.preamble, suite.minimal.HCL(minForm), nil)
		if err != nil {
			return nil, err
		}
		maximal, err := hclBlock(source, blockHeader, suite.preamble, suite.maximal.HCL(maxForm), suite.maximal.Omissions)
		if err != nil {
			return nil, err
		}
		files = append(files,
			rawFile(path.Join(dir, "tests/terraform", suite.name, "resource_minimal.tf"), source, minimal),
			rawFile(path.Join(dir, "tests/terraform", suite.name, "resource_maximal.tf"), source, maximal),
		)
	}
	files = append(files,
		rawFile(path.Join(dir, "tests/responses/resource_minimal.json"), source, spec.WireJSON(fixtures.ResponseMinimal)),
		rawFile(path.Join(dir, "tests/responses/resource_maximal.json"), source, spec.WireJSON(fixtures.ResponseMaximal)),
	)

	exampleHeader := fmt.Sprintf("resource %q %q", r.Names.TerraformType, "example")
	example, err := hclBlock(source, exampleHeader, "", spec.HCL(fixtures.ConfigMaximal), nil)
	if err != nil {
		return nil, err
	}
	files = append(files, rawFile(path.Join("examples/resources", r.Names.TerraformType, "resource.tf"), source, example))

	importID := fixtures.NamePrefix + "id"
	importSh, err := importScript(source, r.Names.TerraformType, importID)
	if err != nil {
		return nil, err
	}
	files = append(files, rawFile(path.Join("examples/resources", r.Names.TerraformType, "import.sh"), source, importSh))

	return files, nil
}

// importScript renders the two-line import example.
func importScript(source, terraformType, id string) ([]byte, error) {
	header, err := hashHeader(source)
	if err != nil {
		return nil, err
	}
	body := fmt.Sprintf("%s\n# Import an existing object by its API identifier.\nterraform import %s.example %s\n",
		header, terraformType, id)
	return []byte(body), nil
}
