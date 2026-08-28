package run

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rs/zerolog"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/strategy"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/testapiserver"
)

// streamSpec is the honest-but-partial document for the test API server's stream
// shape resource: format and mode are declared plain required enums, with no
// hint that format value-gates which mode is valid. The audit discovers the
// value-conditional rule by cycling mode against the format when the API refuses
// the first synthesised combination in prose the refusal grammar cannot parse.
const streamSpec = `openapi: 3.0.3
info:
  title: stream fixture
  version: "1.0"
paths:
  /streams:
    get:
      operationId: listStreams
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/Stream'
    post:
      operationId: createStream
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/StreamCreate'
      responses:
        "201":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Stream'
  /streams/{streamId}:
    parameters:
      - name: streamId
        in: path
        schema:
          type: string
    get:
      operationId: getStream
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Stream'
    put:
      operationId: updateStream
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/StreamCreate'
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Stream'
    delete:
      operationId: deleteStream
      responses:
        "204":
          description: gone
components:
  schemas:
    StreamCreate:
      type: object
      required: [name, format, mode]
      properties:
        name:
          type: string
        format:
          type: string
          enum: [avro, json, locked]
        mode:
          type: string
          enum: [batch, streaming]
    Stream:
      allOf:
        - $ref: '#/components/schemas/StreamCreate'
        - type: object
          properties:
            id:
              type: string
              readOnly: true
`

// streamOptions builds a strategy-driven run against the stream fixture, so Run
// replaces the uniform program with the compiled per-resource strategy — the
// path value-cycling lives on.
func streamOptions(t *testing.T, s *testapiserver.Server, logs *bytes.Buffer) Options {
	t.Helper()
	document, err := specmodel.Load([]byte(streamSpec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	configuration := &config.Config{
		Audit: config.Audit{NamePrefix: "tfpfgen", MaxObjects: 25, RateLimitRPS: 2},
	}
	p, err := plan.Derive(document, configuration, nil)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	shrinkPolls(p)
	if logs == nil {
		logs = &bytes.Buffer{}
	}
	return Options{
		Plan:         p,
		Doc:          document,
		Config:       configuration,
		BaseURL:      s.BaseURL(),
		Auth:         Auth{Method: config.AuthBearerToken},
		NamePrefix:   "tfpfgen",
		RateLimitRPS: 500,
		RunsDir:      t.TempDir(),
		SpecHash:     "testspechash",
		Logger:       zerolog.New(logs).Level(zerolog.DebugLevel),
		Lookup:       lookupOf(testEnv()),
		RunID:        "testrun1",
	}
}

// hasString reports whether xs contains s.
func hasString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// validWhenFor finds the validWhen observation for a subject field scoped to a
// specific gate value, or nil.
func validWhenFor(obs []observe.Observation, entity, subject, gateField, gateValue string) *observe.Observation {
	for i := range obs {
		o := &obs[i]
		if o.Entity != entity || o.Attribute != subject || o.Kind != observe.KindValidWhen {
			continue
		}
		if o.Condition != nil && o.Condition.Attribute == gateField && o.Condition.Equals == gateValue {
			return o
		}
	}
	return nil
}

// TestUnit_Adaptive_StreamCyclesValueAndConfirmsConfiguration is the headline
// value-cycling behaviour: the API refuses the first synthesised body
// (format=avro, mode=batch) in free-form prose the grammar cannot classify, the
// executor cycles mode to the value the format accepts, the entity completes
// rather than blocking, and the triangulating inference confirms a
// validConfiguration edge on format from the both-direction evidence — mode=batch
// accepted under json, refused under avro.
func TestUnit_Adaptive_StreamCyclesValueAndConfirmsConfiguration(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})

	obs, summary := mustRun(t, streamOptions(t, s, nil))

	got := entityStatus(t, summary, "stream")
	if got.Outcome == observe.OutcomeBlocked {
		t.Fatalf("stream blocked despite value-cycling: %+v", got)
	}

	o := findObs(obs, "stream", "format", observe.KindValidConfiguration)
	if o == nil || o.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("validConfiguration(format) = %+v, want a confirmed edge", o)
	}
	values, ok := o.Value.([]string)
	if !ok {
		t.Fatalf("validConfiguration value = %T, want []string", o.Value)
	}
	if !hasString(values, "avro") || !hasString(values, "json") {
		t.Fatalf("validConfiguration values = %v, want both avro and json", values)
	}
	if summary.EdgesConfirmed == 0 {
		t.Errorf("summary reports no confirmed edges: %+v", summary.ByKind)
	}
	if collectionCount(t, s.BaseURL(), "/streams", "streams") != 0 {
		t.Errorf("streams remain after the run: cleanup did not level the tenant")
	}
}

// TestUnit_Adaptive_StreamUnsatisfiableFormatRecordsInconclusive drives the
// no-combination-works branch: the `locked` format supports no mode, so cycling
// exhausts every alternative without an accepted body. The variant records an
// inconclusive validWhen edge for mode under format=locked rather than blocking,
// and the entity still finishes the probes it can — its confirmed
// validConfiguration among them.
func TestUnit_Adaptive_StreamUnsatisfiableFormatRecordsInconclusive(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})

	obs, summary := mustRun(t, streamOptions(t, s, nil))

	if got := entityStatus(t, summary, "stream"); got.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("stream = %+v, want audited despite the unsatisfiable locked format", got)
	}
	inc := validWhenFor(obs, "stream", "mode", "format", "locked")
	if inc == nil {
		t.Fatalf("no validWhen(mode | format=locked) observation; have %v", observationIndex(obs))
	}
	if inc.Outcome != observe.OutcomeInconclusive {
		t.Fatalf("validWhen(mode | format=locked) outcome = %s, want inconclusive", inc.Outcome)
	}
	// The entity still finished its other probes: the confirmed configuration
	// edge is there alongside the inconclusive one.
	if o := findObs(obs, "stream", "format", observe.KindValidConfiguration); o == nil || o.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("the entity did not finish its other probes: validConfiguration = %+v", o)
	}
}

// TestUnit_Adaptive_StreamRunIsDeterministic runs the same audit twice and
// asserts the observations are identical in identity and outcome — value-cycling
// introduces no run-to-run wobble.
func TestUnit_Adaptive_StreamRunIsDeterministic(t *testing.T) {
	t.Parallel()
	a := testapiserver.New(t, testapiserver.Quirks{})
	obsA, _ := mustRun(t, streamOptions(t, a, nil))
	b := testapiserver.New(t, testapiserver.Quirks{})
	obsB, _ := mustRun(t, streamOptions(t, b, nil))

	if !reflect.DeepEqual(observationIndex(obsA), observationIndex(obsB)) {
		t.Fatalf("two runs produced different observations:\n%v\n%v",
			observationIndex(obsA), observationIndex(obsB))
	}
}

// TestUnit_ClassifyRefusal_GeneralizedFieldExtraction covers the fallback the
// classifier reaches for when the four-clause grammar does not match: scanning a
// free-form refusal for any field the entity declares. The anchoring case is a
// real API's wording, which names the field in prose no clause matches.
func TestUnit_ClassifyRefusal_GeneralizedFieldExtraction(t *testing.T) {
	t.Parallel()
	known := func(fields ...string) map[string]strategy.SyntheticValueRules {
		out := map[string]strategy.SyntheticValueRules{}
		for _, f := range fields {
			out[f] = strategy.SyntheticValueRules{Field: f}
		}
		return out
	}
	cases := []struct {
		name    string
		message string
		known   map[string]strategy.SyntheticValueRules
		want    []string
	}{
		{
			name:    "real API dynamic-tag refusal",
			message: "type: Dynamic tags are not supported for the provided object type",
			known:   known("type", "objectType", "name"),
			want:    []string{"type"},
		},
		{
			name:    "free-form value-conditional naming two fields",
			message: "mode: streaming is not supported for the json format",
			known:   known("name", "format", "mode"),
			want:    []string{"format", "mode"},
		},
		{
			name:    "an unknown field named is not a signal",
			message: "serial number must be provided",
			known:   known("name", "mode"),
			want:    nil,
		},
		{
			name:    "a field name is matched on word boundaries only",
			message: "the model number is invalid",
			known:   known("mode"),
			want:    nil,
		},
		{
			name:    "underscore field names match whole",
			message: "agent_id could not be resolved",
			known:   known("agent_id", "name"),
			want:    []string{"agent_id"},
		},
		{
			name:    "empty message names nothing",
			message: "",
			known:   known("mode"),
			want:    nil,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := declaredFieldsNamedIn(testCase.message, testCase.known)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("namedKnownFields(%q) = %v, want %v", testCase.message, got, testCase.want)
			}
		})
	}
}

// TestUnit_Adaptive_StreamRedactionHolds runs a full strategy-driven stream
// audit and greps every file it produced, plus the debug log, for the bearer
// token — the value-cycling probes must not leak it into an excerpt.
func TestUnit_Adaptive_StreamRedactionHolds(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})

	var logs bytes.Buffer
	obs, _ := mustRun(t, streamOptions(t, s, &logs))

	dir := t.TempDir()
	if err := observe.Write(dir, obs); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(testToken)) {
			t.Fatalf("%s contains the bearer token", e.Name())
		}
	}
	if bytes.Contains(logs.Bytes(), []byte(testToken)) {
		t.Fatal("the debug log contains the bearer token")
	}
}
