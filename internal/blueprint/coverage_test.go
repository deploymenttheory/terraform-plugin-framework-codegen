package blueprint

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnit_Blueprint_TypeKindPredicates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind                                TypeKind
		collection, nested, nestedCollected bool
	}{
		{KindString, false, false, false},
		{KindBool, false, false, false},
		{KindList, true, false, false},
		{KindSet, true, false, false},
		{KindMap, true, false, false},
		{KindListNested, false, true, true},
		{KindSetNested, false, true, true},
		// A single nested object is nested but holds exactly one, so it needs a
		// different helper shape than a collection does.
		{KindSingleNested, false, true, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()
			if got := tc.kind.IsCollection(); got != tc.collection {
				t.Errorf("IsCollection = %v, want %v", got, tc.collection)
			}
			if got := tc.kind.IsNested(); got != tc.nested {
				t.Errorf("IsNested = %v, want %v", got, tc.nested)
			}
			if got := tc.kind.IsNestedCollection(); got != tc.nestedCollected {
				t.Errorf("IsNestedCollection = %v, want %v", got, tc.nestedCollected)
			}
		})
	}
}

func TestUnit_Blueprint_LoadReportsFileErrors(t *testing.T) {
	t.Parallel()

	if _, err := Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("loading a missing file should fail")
	}

	bad := filepath.Join(t.TempDir(), "bad"+Ext)
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(bad); err == nil {
		t.Error("malformed JSON should fail")
	}

	// A file whose content is valid JSON but not a valid blueprint must fail
	// validation rather than load a half-built document.
	invalid := filepath.Join(t.TempDir(), "invalid"+Ext)
	if err := os.WriteFile(invalid, []byte(`{"formatVersion":"1"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(invalid); !errors.Is(err, ErrInvalid) {
		t.Errorf("error = %v, want ErrInvalid", err)
	}
}

func TestUnit_Blueprint_SaveReportsWriteErrors(t *testing.T) {
	t.Parallel()

	// A path whose parent is a file, not a directory.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := Save(filepath.Join(blocker, "nested", "x.json"), validBlueprint()); err == nil {
		t.Error("saving under a file should fail")
	}
}

func TestUnit_Blueprint_LoadDirRejectsMalformedParts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if err := Save(filepath.Join(dir, "provider"+Ext), validBlueprint()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken"+Ext), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := LoadDir(dir); err == nil {
		t.Error("a malformed part should fail the whole load")
	}

	// A part declaring a format version this build does not understand.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "x"+Ext), []byte(`{"formatVersion":"99"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := LoadDir(other); !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("error = %v, want ErrUnsupportedFormat", err)
	}
}

func TestUnit_Blueprint_LoadDirAcceptsASingleFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "one"+Ext)
	if err := Save(path, validBlueprint()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// -blueprint may name either a tree or one file.
	got, err := LoadDir(path)
	if err != nil {
		t.Fatalf("LoadDir on a file: %v", err)
	}
	if len(got.Resources) != 1 {
		t.Errorf("got %d resources", len(got.Resources))
	}
}

func TestUnit_Blueprint_LoadDirRejectsAMissingRoot(t *testing.T) {
	t.Parallel()

	if _, err := LoadDir(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("a missing root should fail")
	}
}

// TestUnit_Blueprint_ValidateNestedAttributes covers the nested validation
// branches, which the flat pilot blueprint never reaches.
func TestUnit_Blueprint_ValidateNestedAttributes(t *testing.T) {
	t.Parallel()

	nested := func(mutate func(*NestedAttributeObject)) Attribute {
		n := NestedAttributeObject{
			GoTypeName: "M", SDKType: "p.T", AttrTypesVar: "a", ObjectTypeVar: "o",
			ExpandFunc: "e", FlattenFunc: "f",
			Attributes: []Attribute{{
				Name: "x", GoField: "X", ComputedOptionalRequired: Required,
				Type: AttrType{Kind: KindString},
				Wire: WireBinding{Expand: &ConvertCall{Func: "e"}, Flatten: &ConvertCall{Func: "f"}},
			}},
		}
		if mutate != nil {
			mutate(&n)
		}
		return Attribute{
			Name: "items", GoField: "Items", ComputedOptionalRequired: Optional,
			Type: AttrType{Kind: KindSetNested, NestedObject: &n},
			Wire: WireBinding{Expand: &ConvertCall{Func: "e"}, Flatten: &ConvertCall{Func: "f"}},
		}
	}

	tests := []struct {
		name     string
		attr     Attribute
		wantPath string
	}{
		{
			name: "nested set on a scalar kind",
			attr: Attribute{
				Name: "x", GoField: "X", ComputedOptionalRequired: Optional,
				Type: AttrType{Kind: KindString, NestedObject: &NestedAttributeObject{}},
				Wire: WireBinding{Expand: &ConvertCall{Func: "e"}, Flatten: &ConvertCall{Func: "f"}},
			},
			wantPath: "nested",
		},
		{
			name: "nested kind with no nested object",
			attr: Attribute{
				Name: "x", GoField: "X", ComputedOptionalRequired: Optional,
				Type: AttrType{Kind: KindSetNested},
				Wire: WireBinding{Expand: &ConvertCall{Func: "e"}, Flatten: &ConvertCall{Func: "f"}},
			},
			wantPath: "nested",
		},
		{
			name:     "nested object with no attributes",
			attr:     nested(func(n *NestedAttributeObject) { n.Attributes = nil }),
			wantPath: "attributes",
		},
		{
			name:     "nested object missing its generated names",
			attr:     nested(func(n *NestedAttributeObject) { n.ExpandFunc = "" }),
			wantPath: "expandFunc",
		},
		{
			name: "nested kind carrying an element type",
			attr: func() Attribute {
				a := nested(nil)
				a.Type.ElementType = &AttrType{Kind: KindString}
				return a
			}(),
			wantPath: "elem",
		},
		{
			name: "duplicate names inside a nested object",
			attr: nested(func(n *NestedAttributeObject) {
				n.Attributes = append(n.Attributes, n.Attributes[0])
			}),
			wantPath: "used more than once",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b := validBlueprint()
			b.Resources[0].Attributes = append(b.Resources[0].Attributes, tc.attr)

			err := b.Validate()
			if err == nil {
				t.Fatalf("expected a problem mentioning %q", tc.wantPath)
			}
			if !strings.Contains(err.Error(), tc.wantPath) {
				t.Errorf("error omits %q:\n%v", tc.wantPath, err)
			}
		})
	}

	// The valid nested shape must pass, or every case above proves nothing.
	b := validBlueprint()
	b.Resources[0].Attributes = append(b.Resources[0].Attributes, nested(nil))
	if err := b.Validate(); err != nil {
		t.Errorf("a well-formed nested attribute should validate: %v", err)
	}
}

func TestUnit_Blueprint_ValidateImportStyles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Blueprint)
		wantErr bool
	}{
		{"unsupported import is fine", func(b *Blueprint) {
			b.Resources[0].Import = ImportPolicy{Style: ImportUnsupported}
		}, false},
		{"no import policy is fine", func(b *Blueprint) {
			b.Resources[0].Import = ImportPolicy{}
		}, false},
		{"passthrough with no attribute", func(b *Blueprint) {
			b.Resources[0].Import = ImportPolicy{Style: ImportPassthroughID}
		}, true},
		{"unknown style", func(b *Blueprint) {
			b.Resources[0].Import = ImportPolicy{Style: "telepathy"}
		}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b := validBlueprint()
			tc.mutate(&b)

			err := b.Validate()
			if tc.wantErr != (err != nil) {
				t.Errorf("Validate error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestUnit_Blueprint_ValidateArgumentKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []Argument
		wantErr bool
	}{
		{"context and body", []Argument{{Kind: ArgContext}, {Kind: ArgBody}}, false},
		{"state field by name", []Argument{{Kind: ArgStateField, Field: "ID"}}, false},
		{"plan field by expression", []Argument{{Kind: ArgPlanField, Expr: "plan.X"}}, false},
		{"state field with neither", []Argument{{Kind: ArgStateField}}, true},
		{"literal with no expression", []Argument{{Kind: ArgLiteral}}, true},
		{"empty kind", []Argument{{}}, true},
		{"unknown kind", []Argument{{Kind: "telepathy"}}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b := validBlueprint()
			b.Resources[0].Binding.Read.Args = tc.args

			err := b.Validate()
			if tc.wantErr != (err != nil) {
				t.Errorf("Validate error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestUnit_Blueprint_ValidateReplaceOnly(t *testing.T) {
	t.Parallel()

	// replaceOnly with no update operation is the coherent combination.
	b := validBlueprint()
	b.Resources[0].Binding.Update = nil
	b.Resources[0].Policy.UpdateStyle = UpdateReplaceOnly

	if err := b.Validate(); err != nil {
		t.Errorf("replaceOnly with no update should validate: %v", err)
	}
}

func TestUnit_Blueprint_ValidateCatchesAnAbsentCreateIDSource(t *testing.T) {
	t.Parallel()

	b := validBlueprint()
	b.Resources[0].Binding.ID.FromCreate = ""

	err := b.Validate()
	if err == nil || !strings.Contains(err.Error(), "fromCreate") {
		t.Errorf("a create with no ID source should be caught: %v", err)
	}

	// With no create operation there is no identifier to take, so it is optional.
	b2 := validBlueprint()
	b2.Resources[0].Binding.Create = nil
	b2.Resources[0].Binding.ID.FromCreate = ""
	if err := b2.Validate(); err != nil {
		t.Errorf("no create means fromCreate is not required: %v", err)
	}
}
