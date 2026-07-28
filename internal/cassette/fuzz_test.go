package cassette

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzCassette_Redaction plants a secret at an arbitrary depth in an arbitrary JSON value
// and asserts it does not survive.
//
// Fuzzing earns its keep here specifically because the traversal is recursive and the
// failure is silent: a scrubber that handles maps but not slices-of-maps, or strings but
// not map *keys*, passes every hand-written test somebody thought to write and then leaks
// the one shape they did not. The generated shapes are what a hand-written table cannot
// enumerate.
//
// Run in CI with a short fuzztime; the seeds alone are worth keeping as regression cases.
func FuzzCassette_Redaction(f *testing.F) {
	const secret = "sekrit-0123456789abcdef"

	// Seeds cover the shapes a hand-written test would reach for, so that even the
	// non-fuzzing run exercises them.
	f.Add(`{"a":"SECRET"}`)
	f.Add(`{"a":{"b":{"c":{"d":"SECRET"}}}}`)
	f.Add(`["x",["y",["SECRET"]]]`)
	f.Add(`{"SECRET":"value"}`)
	f.Add(`{"a":[{"b":"prefix SECRET suffix"}]}`)
	f.Add(`"SECRET"`)
	f.Add(`{"a":1,"b":null,"c":true,"d":"SECRET"}`)
	f.Add(`{}`)

	r, err := NewRedactor(map[string]string{"secret": secret}, nil)
	if err != nil {
		f.Fatalf("NewRedactor: %v", err)
	}

	f.Fuzz(func(t *testing.T, template string) {
		// The template names a placeholder rather than carrying the secret, so the corpus
		// files themselves never contain something secret-shaped.
		input := strings.ReplaceAll(template, "SECRET", secret)

		var v any
		if err := json.Unmarshal([]byte(input), &v); err != nil {
			// Not JSON, so not something a cassette body could hold.
			return
		}

		out := r.Apply(v)

		encoded, err := marshalCanonical(out)
		if err != nil {
			t.Fatalf("marshalCanonical: %v", err)
		}

		if strings.Contains(string(encoded), secret) {
			t.Errorf("the secret survived redaction of %s:\n%s", input, encoded)
		}

		// Scan must no longer object *on account of the declared secret*. It may still
		// object to something else in the generated value, and that is correct rather than
		// a hole: Apply substitutes what was declared, while Scan catches anything
		// secret-shaped including things nobody declared. The two layers are supposed to
		// differ, and an earlier version of this test asserted they should not -- which the
		// fuzzer disproved with forty zeroes.
		for _, f := range Scan("fuzz", encoded, map[string]string{"secret": secret}) {
			if strings.Contains(f.Shape, "declared secret") {
				t.Errorf("Scan still found the declared secret after Apply in %s: %v", input, f)
			}
		}
	})
}
