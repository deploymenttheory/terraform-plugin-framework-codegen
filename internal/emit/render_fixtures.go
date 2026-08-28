package emit

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
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
	// What the probe got the API to accept, where it cleared this entity.
	// The acceptance suite replays those; the unit suite keeps the derivation,
	// because its mock is built from the same derived values.
	accMinimal, accMaximal := spec, spec
	replayed := false
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
	liveMinimal := accMinimal.WithInventedNames().WithRunSuffix()
	liveMaximal := accMaximal.WithInventedNames().WithRunSuffix()
	suites := []struct {
		name     string
		minimal  fixtures.Fixture
		maximal  fixtures.Fixture
		preamble string
	}{
		{name: "unit", minimal: spec, maximal: spec},
		{name: "acceptance", minimal: liveMinimal, maximal: liveMaximal, preamble: fixtures.RunSuffixBlock},
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
