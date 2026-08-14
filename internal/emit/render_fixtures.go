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
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "hashheader", struct{ Source string }{Source: source}); err != nil {
		return "", fmt.Errorf("rendering the hash header: %w", err)
	}
	return buf.String(), nil
}

// hclBlock renders one terraform block around a fixture body, with the
// shared header above it.
func hclBlock(source, blockHeader, body string, skips []fixtures.Omission) ([]byte, error) {
	header, err := hashHeader(source)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(header + "\n\n")
	b.WriteString(blockHeader + " {\n")
	b.WriteString(body)
	for _, s := range skips {
		fmt.Fprintf(&b, "  # %s skipped: %s\n", s.Name, s.Reason)
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}

// resourceFixtures emits a resource's terraform fixtures, response
// fixtures and examples.
func (e *serviceRenderer) resourceFixtures(r *ir.Resource, spec fixtures.Fixture, dir string) ([]File, error) {
	key := r.Names.Key
	source := key
	blockHeader := fmt.Sprintf("resource %q %q", r.Names.TerraformType, "test")

	minimal, err := hclBlock(source, blockHeader, spec.HCL(fixtures.ConfigMinimal), nil)
	if err != nil {
		return nil, err
	}
	maximal, err := hclBlock(source, blockHeader, spec.HCL(fixtures.ConfigMaximal), spec.Omissions)
	if err != nil {
		return nil, err
	}

	var files []File
	for _, suite := range []string{"unit", "acceptance"} {
		files = append(files,
			rawFile(path.Join(dir, "tests/terraform", suite, "resource_minimal.tf"), source, minimal),
			rawFile(path.Join(dir, "tests/terraform", suite, "resource_maximal.tf"), source, maximal),
		)
	}
	files = append(files,
		rawFile(path.Join(dir, "tests/responses/resource_minimal.json"), source, spec.WireJSON(fixtures.ResponseMinimal)),
		rawFile(path.Join(dir, "tests/responses/resource_maximal.json"), source, spec.WireJSON(fixtures.ResponseMaximal)),
	)

	exampleHeader := fmt.Sprintf("resource %q %q", r.Names.TerraformType, "example")
	example, err := hclBlock(source, exampleHeader, spec.HCL(fixtures.ConfigMaximal), nil)
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
