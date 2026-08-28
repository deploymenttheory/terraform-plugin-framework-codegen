package observe

import "testing"

func TestUnit_Observe_NormalisationClassifiesTheStoredForm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		sent, got any
		kind      string
		form      string
	}{
		{"case folded", "MiXeD", "mixed", NormalisationCaseFolded, "mixed"},
		{"upper cased", "shouty", "SHOUTY", NormalisationCaseFolded, "SHOUTY"},
		{"trimmed", "  padded  ", "padded", NormalisationTrimmed, "padded"},
		{"trimmed and folded", " Both ", "both", NormalisationCaseFolded, "both"},
		{"scheme around a host", "www.example.test", "https://www.example.test/", NormalisationExtended, "https://www.example.test/"},
		{"port after a host", "www.example.test", "www.example.test:80", NormalisationExtended, "www.example.test:80"},
		{"timestamp without its zone", "2026-12-31T00:00:00Z", "2026-12-31 00:00:00", NormalisationSameInstant, "2026-12-31 00:00:00"},
		{"timestamp with a fraction", "2026-12-31T10:14:28Z", "2026-12-31T10:14:28.000Z", NormalisationSameInstant, "2026-12-31T10:14:28.000Z"},
		{"timestamp stored to the minute", "2026-08-29T17:03:19Z", "2026-08-29T17:03:00Z", NormalisationSameInstant, "2026-08-29T17:03:00Z"},
		{"timestamp stored to the day", "2026-08-29T17:03:19Z", "2026-08-29", NormalisationSameInstant, "2026-08-29"},
		{"another minute", "2026-08-29T17:04:19Z", "2026-08-29T17:03:00Z", "", ""},
		{"reordered list", []any{"b", "a"}, []any{"a", "b"}, NormalisationReordered, `["a","b"]`},
		{"unrelated string", "alpha", "omega", "", ""},
		{"identical", "same", "same", "", ""},
		{"another instant", "2026-12-31T00:00:00Z", "2026-12-30 00:00:00", "", ""},
		{"masked", "s3cret", "*****", "", ""},
		{"unchanged list", []any{"a", "b"}, []any{"a", "b"}, "", ""},
		{"string became number", "1", float64(1), "", ""},
	}
	for _, testCase := range cases {
		kind, form, ok := Normalisation(testCase.sent, testCase.got)
		if ok != (testCase.kind != "") || kind != testCase.kind || form != testCase.form {
			t.Errorf("%s: Normalisation(%v, %v) = %q, %q, %v; want %q, %q", testCase.name, testCase.sent, testCase.got, kind, form, ok, testCase.kind, testCase.form)
		}
	}
	for kind := range NormalisationKinds {
		if kind == "" {
			t.Error("an empty kind is admitted")
		}
	}
}
