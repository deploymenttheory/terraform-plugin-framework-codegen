package sdkgen

import (
	"bytes"
	"strings"
	"testing"
)

const requestBodyEnumSample = `openapi: 3.0.3
info:
  title: sample
  version: 1.0.0
paths:
  /widgets:
    post:
      operationId: widgets/create
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                name:
                  type: string
                colour:
                  type: string
                  enum:
                    - red
                    - blue
                settings:
                  type: object
                  properties:
                    mode:
                      type: string
                      enum:
                        - fast
                        - slow
      responses:
        "201":
          description: created
  /widgets/{widgetId}:
    patch:
      operationId: widgets/update
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                colour:
                  type: string
                  enum:
                    - red
                    - blue
      responses:
        "200":
          description: updated
          content:
            application/json:
              schema:
                type: object
                properties:
                  status:
                    type: string
                    enum:
                      - live
                      - retired
components:
  schemas:
    Widget:
      type: object
      properties:
        name:
          type: string
`

func TestUnit_Prenormalize_ExtractsRequestBodyEnumsToNamedComponents(t *testing.T) {
	out, rewrites, err := Prenormalise([]byte(requestBodyEnumSample))
	if err != nil {
		t.Fatal(err)
	}

	if rewrites.RequestBodyEnumsExtracted.Count != 3 {
		t.Errorf("extracted %d request-body enums, want 3", rewrites.RequestBodyEnumsExtracted.Count)
	}
	if sites := rewrites.RequestBodyEnumsExtracted.Sites; len(sites) != 2 ||
		sites[0].Where != "paths./widgets" || sites[0].Count != 2 ||
		sites[1].Where != "paths./widgets/{widgetId}" || sites[1].Count != 1 {
		t.Errorf("sites = %+v, want the two paths in document order", sites)
	}

	text := string(out)
	for _, minted := range []string{"WidgetsCreateColour:", "WidgetsCreateSettingsMode:", "WidgetsUpdateColour:"} {
		if !strings.Contains(text, minted) {
			t.Errorf("no component %s in:\n%s", minted, text)
		}
	}
	for _, ref := range []string{
		"#/components/schemas/WidgetsCreateColour",
		"#/components/schemas/WidgetsCreateSettingsMode",
		"#/components/schemas/WidgetsUpdateColour",
	} {
		if !strings.Contains(text, ref) {
			t.Errorf("no reference %s in:\n%s", ref, text)
		}
	}

	// The response's inline enum is not a request body's and stays where
	// it is.
	if strings.Contains(text, "WidgetsUpdateStatus") {
		t.Errorf("a response enum was extracted:\n%s", text)
	}

	// The rewrite is a fixed point: the extracted document extracts
	// nothing further and re-emerges byte for byte.
	again, rewritesAgain, err := Prenormalise(out)
	if err != nil {
		t.Fatal(err)
	}
	if rewritesAgain.RequestBodyEnumsExtracted.Count != 0 {
		t.Errorf("a second pass extracted %d enums from its own output", rewritesAgain.RequestBodyEnumsExtracted.Count)
	}
	if !bytes.Equal(out, again) {
		t.Error("extraction is not a fixed point")
	}
}

func TestUnit_Prenormalize_ReusesAnIdenticalMintedComponent(t *testing.T) {
	document := `openapi: 3.0.3
info:
  title: sample
  version: 1.0.0
paths:
  /widgets:
    post:
      operationId: widgets/create
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                colour:
                  type: string
                  enum:
                    - red
                    - blue
          application/x-www-form-urlencoded:
            schema:
              type: object
              properties:
                colour:
                  type: string
                  enum:
                    - red
                    - blue
      responses:
        "201":
          description: created
`
	out, rewrites, err := Prenormalise([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	if rewrites.RequestBodyEnumsExtracted.Count != 2 {
		t.Errorf("extracted %d request-body enums, want both media types", rewrites.RequestBodyEnumsExtracted.Count)
	}
	text := string(out)
	if strings.Count(text, "WidgetsCreateColour:") != 1 {
		t.Errorf("an identical enum should share one component:\n%s", text)
	}
	if strings.Count(text, "#/components/schemas/WidgetsCreateColour") != 2 {
		t.Errorf("both bodies should reference the shared component:\n%s", text)
	}
}

func TestUnit_Prenormalize_SuffixesAMintedNameAnExistingComponentHolds(t *testing.T) {
	document := `openapi: 3.0.3
info:
  title: sample
  version: 1.0.0
paths:
  /widgets:
    post:
      operationId: widgets/create
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                colour:
                  type: string
                  enum:
                    - red
                    - blue
      responses:
        "201":
          description: created
components:
  schemas:
    WidgetsCreateColour:
      type: object
      properties:
        name:
          type: string
`
	out, _, err := Prenormalise([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "WidgetsCreateColourEnum:") {
		t.Errorf("the minted name should step aside from the document's own component:\n%s", text)
	}
	if !strings.Contains(text, "#/components/schemas/WidgetsCreateColourEnum") {
		t.Errorf("the body should reference the suffixed component:\n%s", text)
	}
}

func TestUnit_Prenormalize_NamesAnOperationWithoutAnOperationIdByPathAndMethod(t *testing.T) {
	document := `openapi: 3.0.3
info:
  title: sample
  version: 1.0.0
paths:
  /widgets/{widgetId}/modes:
    post:
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                mode:
                  type: string
                  enum:
                    - fast
                    - slow
      responses:
        "202":
          description: accepted
`
	out, rewrites, err := Prenormalise([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	if rewrites.RequestBodyEnumsExtracted.Count != 1 {
		t.Fatalf("extracted %d request-body enums, want 1", rewrites.RequestBodyEnumsExtracted.Count)
	}
	if text := string(out); !strings.Contains(text, "WidgetsModesPostMode:") {
		t.Errorf("the fallback name should join fixed path segments and the method:\n%s", text)
	}
}

func TestUnit_Prenormalize_LeavesABodyThatIsItselfAnEnumAlone(t *testing.T) {
	document := `openapi: 3.0.3
info:
  title: sample
  version: 1.0.0
paths:
  /widgets/{widgetId}/state:
    put:
      operationId: widgets/set-state
      requestBody:
        content:
          application/json:
            schema:
              type: string
              enum:
                - live
                - retired
      responses:
        "200":
          description: set
`
	out, rewrites, err := Prenormalise([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	if rewrites.RequestBodyEnumsExtracted.Count != 0 {
		t.Errorf("extracted %d request-body enums from a propertyless body", rewrites.RequestBodyEnumsExtracted.Count)
	}
	if text := string(out); !strings.Contains(text, "- live") {
		t.Errorf("the body's own enum should stay inline:\n%s", text)
	}
}
