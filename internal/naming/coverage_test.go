package naming

import "testing"

// TestUnit_Naming_ExtraInitialismsExtendTheDefaults lets a target add a domain
// acronym without changing this package.
func TestUnit_Naming_ExtraInitialismsExtendTheDefaults(t *testing.T) {
	t.Parallel()

	o := Options{ExtraInitialisms: []string{"BGP", "TLD"}}

	if got := o.GoFieldName("tldName"); got != "TLDName" {
		t.Errorf("GoFieldName = %q, want TLDName", got)
	}
	// The defaults still apply alongside the extras.
	if got := o.GoFieldName("targetUrl"); got != "TargetURL" {
		t.Errorf("GoFieldName = %q, want TargetURL", got)
	}
}

// TestUnit_Naming_PluralInitialisms pins Go convention: LabelIDs, not LabelIDS or
// LabelIds, either of which reads as a typo in generated code.
func TestUnit_Naming_PluralInitialisms(t *testing.T) {
	t.Parallel()

	o := Options{}

	tests := map[string]string{
		"labelIds": "LabelIDs",
		"urls":     "URLs",
		"testIds":  "TestIDs",
		// Not a plural initialism, just a word ending in s.
		"ideas": "Ideas",
	}

	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			if got := o.GoFieldName(in); got != want {
				t.Errorf("GoFieldName(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestUnit_Naming_LowerFirst(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"Tag":     "tag",
		"TagName": "tagName",
		// An acronym run is left alone: lowering only its first rune gives "iD",
		// which reads as a typo.
		"ID":   "ID",
		"URL":  "URL",
		"":     "",
		"X":    "x",
		"V7Ta": "v7Ta",
	}

	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			if got := lowerFirst(in); got != want {
				t.Errorf("lowerFirst(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestUnit_Naming_SafeIdentifierOnEmpty(t *testing.T) {
	t.Parallel()

	if got := SafeIdentifier(""); got != "value" {
		t.Errorf("SafeIdentifier(\"\") = %q, want value", got)
	}
}
