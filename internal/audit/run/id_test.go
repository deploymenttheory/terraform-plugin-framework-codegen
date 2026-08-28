package run

import (
	"net/http"
	"testing"
)

// TestUnit_LearnID covers the create-response shapes real APIs use to name a new
// object, in the order extractID resolves them.
func TestUnit_LearnID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		entity string
		res    *httpResult
		want   string
	}{
		{
			name:   "top-level id",
			entity: "monitor",
			res:    &httpResult{body: []byte(`{"id":"abc","name":"x"}`)},
			want:   "abc",
		},
		{
			name:   "entity-scoped id key",
			entity: "monitor",
			res:    &httpResult{body: []byte(`{"monitorId":"m7"}`)},
			want:   "m7",
		},
		{
			name:   "entity-scoped snake id key",
			entity: "monitor",
			res:    &httpResult{body: []byte(`{"monitor_id":"m8"}`)},
			want:   "m8",
		},
		{
			name:   "entity-scoped all-caps id key",
			entity: "monitor",
			res:    &httpResult{body: []byte(`{"monitorID":"m9"}`)},
			want:   "m9",
		},
		{
			name:   "nested data envelope",
			entity: "monitor",
			res:    &httpResult{body: []byte(`{"data":{"id":"d1","name":"x"}}`)},
			want:   "d1",
		},
		{
			name:   "nested result envelope",
			entity: "monitor",
			res:    &httpResult{body: []byte(`{"result":{"id":"r1"}}`)},
			want:   "r1",
		},
		{
			name:   "entity-named envelope",
			entity: "monitor",
			res:    &httpResult{body: []byte(`{"monitor":{"id":"e1"}}`)},
			want:   "e1",
		},
		{
			name:   "Location header",
			entity: "monitor",
			res: &httpResult{
				body:   []byte(`{"status":"created"}`),
				header: http.Header{"Location": {"https://api.example.test/monitors/loc1"}},
			},
			want: "loc1",
		},
		{
			name:   "Location header with trailing slash and query",
			entity: "monitor",
			res: &httpResult{
				body:   []byte(`{}`),
				header: http.Header{"Location": {"/monitors/loc2/?v=1"}},
			},
			want: "loc2",
		},
		{
			name:   "Content-Location header",
			entity: "monitor",
			res: &httpResult{
				body:   []byte(`{}`),
				header: http.Header{"Content-Location": {"/monitors/cl3"}},
			},
			want: "cl3",
		},
		{
			name:   "self link string",
			entity: "monitor",
			res:    &httpResult{body: []byte(`{"self":"/monitors/self4","name":"x"}`)},
			want:   "self4",
		},
		{
			name:   "links.self link",
			entity: "monitor",
			res:    &httpResult{body: []byte(`{"links":{"self":"https://api.example.test/monitors/self5"}}`)},
			want:   "self5",
		},
		{
			name:   "location beats body",
			entity: "monitor",
			res: &httpResult{
				body:   []byte(`{"id":"bodyId"}`),
				header: http.Header{"Location": {"/monitors/headerId"}},
			},
			want: "headerId",
		},
		{
			name:   "no id anywhere",
			entity: "monitor",
			res:    &httpResult{body: []byte(`{"status":"ok","name":"x"}`)},
			want:   "",
		},
		{
			name:   "non-object body, no header",
			entity: "monitor",
			res:    &httpResult{body: []byte(`[1,2,3]`)},
			want:   "",
		},
		{
			name:   "nil result",
			entity: "monitor",
			res:    nil,
			want:   "",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := extractID(testCase.entity, testCase.res); got != testCase.want {
				t.Errorf("extractID(%q) = %q, want %q", testCase.name, got, testCase.want)
			}
		})
	}
}

// TestUnit_LearnID_Deterministic: a body with several id-spelled keys always
// yields the same one, because the scan is over sorted keys.
func TestUnit_LearnID_Deterministic(t *testing.T) {
	t.Parallel()
	body := []byte(`{"widget_id":"w1","gadget_id":"g1"}`)
	first := extractID("thing", &httpResult{body: body})
	for i := 0; i < 20; i++ {
		if got := extractID("thing", &httpResult{body: body}); got != first {
			t.Fatalf("extractID not deterministic: %q then %q", first, got)
		}
	}
}
