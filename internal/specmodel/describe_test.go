package specmodel

import "testing"

// TestUnit_Specmodel_DescribeMeasuresWhatAPinRecords proves Describe reports
// the three values a pin is written from, and reports nothing rather
// than a guess for bytes it cannot read — a truncated download must not pass
// as a document with a plausible shape.
func TestUnit_Specmodel_DescribeMeasuresWhatAPinRecords(t *testing.T) {
	const document = `openapi: 3.0.3
info: {title: T, version: "4.5.6"}
paths:
  /widgets:
    get:
      responses:
        "200": {description: ok}
    post:
      responses:
        "201": {description: made}
  /widgets/{id}:
    delete:
      responses:
        "204": {description: gone}
`
	version, paths, operations := Describe([]byte(document))
	if version != "4.5.6" || paths != 2 || operations != 3 {
		t.Errorf("Describe = %q, %d paths, %d operations; want 4.5.6, 2, 3", version, paths, operations)
	}

	version, paths, operations = Describe([]byte("\t{[ not a document"))
	if version != "" || paths != 0 || operations != 0 {
		t.Errorf("unreadable bytes described as %q, %d, %d", version, paths, operations)
	}
}
