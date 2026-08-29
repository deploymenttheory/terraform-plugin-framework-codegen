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
	if rewrites != (Rewrites{}) {
		t.Errorf("Rewrites = %+v, want every count zero", rewrites)
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
	if rewrites.UnionsReduced != 0 {
		t.Errorf("reduced %d unions, want none: every branch names a schema", rewrites.UnionsReduced)
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
		SchemaDefaultsStripped:      3,
		AnonymousAllOfsCollapsed:    0,
		ByteArrayCollectionsWidened: 1,
		UnionsReduced:               7,
		ErrorContentDropped:         2,
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
