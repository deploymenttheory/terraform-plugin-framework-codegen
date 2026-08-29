package sdkgen

import (
	"strings"
	"testing"
)

// untouchedDocument gives every rewrite nothing to do: no schema default, no
// anonymous allOf, no byte-formatted array item, no union, and a success
// response that declares its own media type.
const untouchedDocument = `openapi: 3.0.3
info:
  title: quiet
  version: 1.0.0
paths:
  /widgets:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Widget'
components:
  schemas:
    Widget:
      type: object
      properties:
        name:
          type: string
`

// namedBranchUnion is the shape reduceUnion deliberately refuses: a union
// whose branches are all named schemas is polymorphism a generator models,
// and folding it takes the alternatives out from under a discriminator that
// still names them.
const namedBranchUnion = `openapi: 3.0.3
info:
  title: polymorphic
  version: 1.0.0
paths:
  /things:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                oneOf:
                  - $ref: '#/components/schemas/Circle'
                  - $ref: '#/components/schemas/Square'
components:
  schemas:
    Circle:
      type: object
      properties:
        radius:
          type: string
    Square:
      type: object
      properties:
        side:
          type: string
`

// TestUnit_Sdkgen_ADocumentNeedingNoRewritingReportsZero pins the difference
// between a rewrite that found nothing and one that was never measured. The
// pre-normalised copy is a temporary the run removes, so a zero is the only
// evidence the document reached the backend as it was written.
func TestUnit_Sdkgen_ADocumentNeedingNoRewritingReportsZero(t *testing.T) {
	t.Parallel()
	_, rewrites, err := Prenormalise([]byte(untouchedDocument))
	if err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]Rewrite{
		"schema defaults":        rewrites.SchemaDefaultsStripped,
		"anonymous allOf":        rewrites.AnonymousAllOfsCollapsed,
		"byte-array collections": rewrites.ByteArrayCollectionsWidened,
		"unions":                 rewrites.UnionsReduced,
		"error content":          rewrites.ErrorContentDropped,
	} {
		if r.Count != 0 || len(r.Sites) != 0 {
			t.Errorf("%s = %+v, want no changes and no sites", name, r)
		}
	}
}

// TestUnit_Sdkgen_AUnionOfNamedSchemasIsNotReduced holds the count to what
// was actually rewritten rather than to what the document declares. A union
// is present here and survives, so counting union sites instead of
// reductions would overstate what the backend lost.
func TestUnit_Sdkgen_AUnionOfNamedSchemasIsNotReduced(t *testing.T) {
	t.Parallel()
	out, rewrites, err := Prenormalise([]byte(namedBranchUnion))
	if err != nil {
		t.Fatal(err)
	}
	if rewrites.UnionsReduced.Count != 0 {
		t.Errorf("reduced %d unions, want none: every branch names a schema", rewrites.UnionsReduced.Count)
	}
	if !strings.Contains(string(out), "oneOf:") {
		t.Errorf("the union did not survive:\n%s", out)
	}
}

// TestUnit_Sdkgen_RewritesReadInTheOrderTheyAreApplied holds the rendering
// the CLI prints. Every count appears whatever its value, because a rewrite
// reporting nothing is the fact that distinguishes a quiet document from an
// unmeasured one.
func TestUnit_Sdkgen_RewritesReadInTheOrderTheyAreApplied(t *testing.T) {
	t.Parallel()
	rendered := Rewrites{
		SchemaDefaultsStripped:      Rewrite{Count: 3},
		AnonymousAllOfsCollapsed:    Rewrite{Count: 0},
		ByteArrayCollectionsWidened: Rewrite{Count: 1},
		UnionsReduced:               Rewrite{Count: 7},
		ErrorContentDropped:         Rewrite{Count: 2},
	}.String()

	for _, want := range []string{
		"3 schema defaults stripped",
		"0 anonymous allOf collapsed",
		"1 byte-array collections widened",
		"7 unions reduced",
		"2 error responses stripped of content",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendering omits %q: %s", want, rendered)
		}
	}
	if got := strings.Index(rendered, "unions reduced"); got < strings.Index(rendered, "schema defaults") {
		t.Errorf("the counts do not read in the order the rewrites run: %s", rendered)
	}
}

// unionOutsideTheUsualRootsSpec puts a reducible union somewhere neither the
// component schemas nor the paths reach. Union reduction walks the whole
// document, and attributing what it changed must not narrow that.
const unionOutsideTheUsualRootsSpec = `openapi: 3.0.3
info: {title: O, version: "1.0.0"}
paths:
  /widgets:
    get:
      responses:
        "200":
          $ref: '#/components/responses/Widget'
components:
  responses:
    Widget:
      description: ok
      content:
        application/json:
          schema:
            anyOf:
              - type: object
                properties:
                  name: {type: string}
              - type: object
                properties:
                  size: {type: string}
`

// TestUnit_Sdkgen_AUnionOutsideTheComponentSchemasIsStillReduced holds the
// walk to the whole document. Naming where a change happened must not become
// a reason to look in fewer places than the rewrite already looked.
func TestUnit_Sdkgen_AUnionOutsideTheComponentSchemasIsStillReduced(t *testing.T) {
	t.Parallel()
	out, rewrites, err := Prenormalise([]byte(unionOutsideTheUsualRootsSpec))
	if err != nil {
		t.Fatal(err)
	}
	if rewrites.UnionsReduced.Count != 1 {
		t.Fatalf("reduced %d unions, want the one under components.responses", rewrites.UnionsReduced.Count)
	}
	if strings.Contains(string(out), "anyOf:") {
		t.Errorf("the union survived:\n%s", out)
	}
	if len(rewrites.UnionsReduced.Sites) != 1 || rewrites.UnionsReduced.Sites[0].Where != "components.responses" {
		t.Errorf("sites = %+v, want the section the document puts it in", rewrites.UnionsReduced.Sites)
	}
}

// TestUnit_Sdkgen_ARewriteNamesWhereItChangedSomething is the point of the
// sites. A count says a document lost something; only the site says which
// part of it, which is what a reader needs to go and look.
func TestUnit_Sdkgen_ARewriteNamesWhereItChangedSomething(t *testing.T) {
	t.Parallel()
	_, rewrites, err := Prenormalise([]byte(prenormaliseSample))
	if err != nil {
		t.Fatal(err)
	}

	for name, r := range map[string]Rewrite{
		"schema defaults":        rewrites.SchemaDefaultsStripped,
		"anonymous allOf":        rewrites.AnonymousAllOfsCollapsed,
		"byte-array collections": rewrites.ByteArrayCollectionsWidened,
	} {
		if r.Count == 0 {
			t.Errorf("%s changed nothing; the fixture no longer exercises it", name)
			continue
		}
		if len(r.Sites) == 0 {
			t.Errorf("%s changed %d things and named nowhere", name, r.Count)
		}
		total := 0
		for _, site := range r.Sites {
			if site.Where == "" || site.Count <= 0 {
				t.Errorf("%s: unnamed or empty site %+v", name, site)
			}
			if !strings.HasPrefix(site.Where, "components.") && !strings.HasPrefix(site.Where, "paths.") {
				t.Errorf("%s: site %q is not named the way the document names it", name, site.Where)
			}
			total += site.Count
		}
		if total != r.Count {
			t.Errorf("%s: sites account for %d of %d changes", name, total, r.Count)
		}
	}
}
