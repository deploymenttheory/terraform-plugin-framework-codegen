package sdkbind

import (
	"strings"
	"testing"
)

// TestVerifyPass holds a pruned binding set green against the SDK it was
// pruned with, for both backends — the pipeline's happy path.
func TestVerifyPass(t *testing.T) {
	t.Run("kiota", func(t *testing.T) {
		b, _ := prunedKiota(t)
		r, err := Verify(b, testdataDir(t, "kiotasdk"))
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if len(r.Problems) != 0 {
			t.Fatalf("pruned bindings do not verify: %v", r.Err())
		}
		if r.Checked == 0 {
			t.Fatal("Verify checked nothing")
		}
	})

	t.Run("openapi-generator", func(t *testing.T) {
		b, _ := prunedOAG(t)
		r, err := Verify(b, testdataDir(t, "oagsdk"))
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if len(r.Problems) != 0 {
			t.Fatalf("pruned bindings do not verify: %v", r.Err())
		}
		if r.Checked == 0 {
			t.Fatal("Verify checked nothing")
		}
	})
}

// TestVerifyFailuresNameTheCulprit corrupts one binding at a time and
// checks the problem names the entity and the binding field — never a
// pile of compile errors.
func TestVerifyFailuresNameTheCulprit(t *testing.T) {
	cases := []struct {
		name       string
		corrupt    func(b *Bindings)
		wantKind   string
		wantKey    string
		wantPath   string
		wantDetail string
	}{
		{
			name: "a chain hop that does not exist",
			corrupt: func(b *Bindings) {
				rb := b.Resources["tags"]
				rb.Read.Segments[0].Name = "Tagz"
				rb.Read.rerender()
			},
			wantKind: "resource", wantKey: "tags", wantPath: "read.expr",
			wantDetail: "has no method Tagz",
		},
		{
			name: "an argument count the method refuses",
			corrupt: func(b *Bindings) {
				rb := b.Resources["tags"]
				last := len(rb.Delete.Segments) - 1
				rb.Delete.Segments[last].Args = []string{"ctx"}
				rb.Delete.rerender()
			},
			wantKind: "resource", wantKey: "tags", wantPath: "delete.expr",
			wantDetail: "takes 2 argument(s) but the call passes 1",
		},
		{
			name: "a response type the call does not return",
			corrupt: func(b *Bindings) {
				b.Resources["tags"].Read.ResponseType = "models.Widgetable"
			},
			wantKind: "resource", wantKey: "tags", wantPath: "read.response_type",
			wantDetail: "the call returns models.Tagable",
		},
		{
			name: "a getter the model does not carry",
			corrupt: func(b *Bindings) {
				fs := b.Resources["tags"].Fields
				for i := range fs {
					if fs[i].Attr == "name" {
						fs[i].Access.Get = "GetTitle"
					}
				}
			},
			wantKind: "resource", wantKey: "tags", wantPath: "fields[name].get",
			wantDetail: "has no method GetTitle",
		},
		{
			name: "a recorded type the SDK disagrees with",
			corrupt: func(b *Bindings) {
				fs := b.Resources["tags"].Fields
				for i := range fs {
					if fs[i].Attr == "count" {
						fs[i].Access.SDKType = "*int64"
					}
				}
			},
			wantKind: "resource", wantKey: "tags", wantPath: "fields[count].sdk_type",
			wantDetail: "recorded *int64 but the SDK carries *int32",
		},
		{
			name: "a parse companion the SDK does not declare",
			corrupt: func(b *Bindings) {
				fs := b.Resources["tags"].Fields
				for i := range fs {
					if fs[i].Attr == "kind" {
						fs[i].Access.ParseFunc = "models.ParseNothing"
					}
				}
			},
			wantKind: "resource", wantKey: "tags", wantPath: "fields[kind].parse_func",
			wantDetail: "no function models.ParseNothing",
		},
		{
			name: "a read model that does not exist",
			corrupt: func(b *Bindings) {
				b.Resources["tags"].ReadModel = "models.Widgetable"
				// Keep the call checks out of the way of the point.
				b.Resources["tags"].Read.ResponseType = ""
			},
			wantKind: "resource", wantKey: "tags", wantPath: "read_model",
			wantDetail: "has no type Widgetable",
		},
		{
			name: "an unresolved nested model",
			corrupt: func(b *Bindings) {
				fs := b.Resources["tags"].Fields
				for i := range fs {
					if fs[i].Attr == "detail" {
						fs[i].NestedModel = ""
					}
				}
			},
			wantKind: "resource", wantKey: "tags", wantPath: "fields[detail].nested_model",
			wantDetail: "unresolved",
		},
		{
			name: "a collection access the response does not offer",
			corrupt: func(b *Bindings) {
				b.Datasources["tags"].CollectionAccess = "GetItems()"
			},
			wantKind: "datasource", wantKey: "tags", wantPath: "collection_access",
			wantDetail: "no single-result method GetItems",
		},
		{
			name: "an element type the list does not carry",
			corrupt: func(b *Bindings) {
				b.Datasources["tags"].ElementType = "models.Widgetable"
			},
			wantKind: "datasource", wantKey: "tags", wantPath: "element_type",
			wantDetail: "the list carries models.Tagable",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			b, _ := prunedKiota(t)
			testCase.corrupt(b)
			r, err := Verify(b, testdataDir(t, "kiotasdk"))
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			p, ok := problemAt(r, testCase.wantKind, testCase.wantKey, testCase.wantPath)
			if !ok {
				t.Fatalf("no problem at %s %s %s; got %v", testCase.wantKind, testCase.wantKey, testCase.wantPath, r.Problems)
			}
			if !strings.Contains(p.Detail, testCase.wantDetail) {
				t.Errorf("detail = %q, want it to contain %q", p.Detail, testCase.wantDetail)
			}
			if r.Err() == nil {
				t.Error("Report.Err() is nil despite problems")
			}
		})
	}
}

// TestVerifyOAGFieldHopMiss names a service field the client does not
// carry, with the client's real fields beside the guess.
func TestVerifyOAGFieldHopMiss(t *testing.T) {
	b, _ := prunedOAG(t)
	rb := b.Resources["tags"]
	rb.Read.Segments[0].Name = "TagzAPI"
	rb.Read.rerender()
	r, err := Verify(b, testdataDir(t, "oagsdk"))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	p, ok := problemAt(r, "resource", "tags", "read.expr")
	if !ok {
		t.Fatalf("no problem for the field hop; got %v", r.Problems)
	}
	if !strings.Contains(p.Detail, "has no field TagzAPI") || !strings.Contains(p.Detail, "TagsAPI") {
		t.Errorf("detail = %q, want the miss and the real field", p.Detail)
	}
}

// TestVerifyNestedWriteModelMiss reports a nested write model that does
// not resolve.
func TestVerifyNestedWriteModelMiss(t *testing.T) {
	b, _ := prunedKiota(t)
	fs := b.Resources["tags"].Fields
	for i := range fs {
		if fs[i].Attr == "detail" {
			fs[i].NestedWriteModel = "models.NoSuchDetail"
		}
	}
	r, err := Verify(b, testdataDir(t, "kiotasdk"))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if _, ok := problemAt(r, "resource", "tags", "fields[detail].nested_write_model"); !ok {
		t.Fatalf("no problem for the nested write model; got %v", r.Problems)
	}
}

// TestVerifyClientMiss reports a missing client type once, not once per
// binding.
func TestVerifyClientMiss(t *testing.T) {
	b, _ := prunedKiota(t)
	b.SDK.ClientTypeName = "NoSuchClient"
	r, err := Verify(b, testdataDir(t, "kiotasdk"))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(r.Problems) != 1 {
		t.Fatalf("problems = %v, want exactly the client miss", r.Problems)
	}
	if r.Problems[0].Path != "sdk.client_type_name" {
		t.Errorf("path = %q", r.Problems[0].Path)
	}
}

// TestVerifyLoadFailure reports an unloadable SDK as an error, not a
// problem list.
func TestVerifyLoadFailure(t *testing.T) {
	b, _ := prunedKiota(t)
	if _, err := Verify(b, t.TempDir()); err == nil {
		t.Error("Verify accepted a directory with no Go packages")
	}
}

func problemAt(r Report, kind, key, path string) (Problem, bool) {
	for _, p := range r.Problems {
		if p.Kind == kind && p.Key == key && p.Path == path {
			return p, true
		}
	}
	return Problem{}, false
}
