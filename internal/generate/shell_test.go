package generate

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// baseProvider is the smallest provider block ShellParamsFrom accepts, so each
// test states only the field it is about.
func baseProvider() blueprint.Provider {
	return blueprint.Provider{
		Name:     "thousandeyes",
		GoModule: "github.com/deploymenttheory/terraform-provider-thousandeyes",
		SDK:      blueprint.SDKModule{ClientType: "*sdk.ThousandEyesClient"},
	}
}

func TestUnit_ShellParams_ADeclaredDisplayNameBeatsTheDerivedOne(t *testing.T) {
	t.Parallel()

	p := baseProvider()
	p.DisplayName = "ThousandEyes"

	params, err := ShellParamsFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if params.DisplayName != "ThousandEyes" {
		t.Errorf("DisplayName = %q, want the declared name", params.DisplayName)
	}
}

// A provider that states no display name still has to name itself in prose, and
// the derived name is the only thing available to name it with.
func TestUnit_ShellParams_AnAbsentDisplayNameFallsBackToTheName(t *testing.T) {
	t.Parallel()

	params, err := ShellParamsFrom(baseProvider())
	if err != nil {
		t.Fatal(err)
	}
	if params.DisplayName != "Thousandeyes" {
		t.Errorf("DisplayName = %q, want the title-cased provider name", params.DisplayName)
	}
}

// The auth method reaches the templates as booleans, because a template that
// compared method strings could compare them wrongly and silently emit the
// other branch.
func TestUnit_ShellParams_TheAuthMethodReachesTheTemplatesAsBooleans(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		auth   blueprint.Auth
		method string
		want   [3]bool // bearer, client credentials, username/password
		scopes string
	}{
		{
			name:   "an absent auth block is bearer",
			method: "bearerToken",
			want:   [3]bool{true, false, false},
			scopes: "nil",
		},
		{
			name: "client credentials carry their endpoint and scopes",
			auth: blueprint.Auth{
				Method:   blueprint.AuthClientCredentials,
				TokenURL: "https://auth.example.com/oauth2/token",
				Scopes:   []string{"read", "write"},
			},
			method: "clientCredentials",
			want:   [3]bool{false, true, false},
			scopes: `[]string{"read", "write"}`,
		},
		{
			name:   "username and password",
			auth:   blueprint.Auth{Method: blueprint.AuthUsernamePassword},
			method: "usernamePassword",
			want:   [3]bool{false, false, true},
			scopes: "nil",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := baseProvider()
			p.Auth = tc.auth

			params, err := ShellParamsFrom(p)
			if err != nil {
				t.Fatal(err)
			}

			if params.AuthMethod != tc.method {
				t.Errorf("AuthMethod = %q, want %q", params.AuthMethod, tc.method)
			}
			got := [3]bool{params.IsBearer, params.IsClientCredentials, params.IsUsernamePassword}
			if got != tc.want {
				t.Errorf("(IsBearer, IsClientCredentials, IsUsernamePassword) = %v, want %v", got, tc.want)
			}
			if params.Scopes != tc.scopes {
				t.Errorf("Scopes = %q, want %q", params.Scopes, tc.scopes)
			}
			if params.TokenURL != tc.auth.TokenURL {
				t.Errorf("TokenURL = %q, want %q", params.TokenURL, tc.auth.TokenURL)
			}
		})
	}
}

// Every auth branch has to render and then parse as Go. BuildShell formats every
// .go file it emits, so a branch that renders unbalanced braces or an unused
// import fails here rather than in a scaffolded provider nobody has built yet.
func TestUnit_BuildShell_EveryAuthMethodRendersFormattableGo(t *testing.T) {
	t.Parallel()

	for _, auth := range []blueprint.Auth{
		{},
		{Method: blueprint.AuthClientCredentials, TokenURL: "https://auth.example.com/oauth2/token", Scopes: []string{"read"}},
		{Method: blueprint.AuthUsernamePassword},
	} {
		t.Run(string(auth.Resolved()), func(t *testing.T) {
			t.Parallel()

			p := baseProvider()
			p.Auth = auth

			params, err := ShellParamsFrom(p)
			if err != nil {
				t.Fatal(err)
			}

			plan, err := BuildShell(params, "provider.blueprint.json", "sha")
			if err != nil {
				t.Fatalf("BuildShell: %v", err)
			}
			if len(plan.Files) == 0 {
				t.Fatal("BuildShell emitted nothing")
			}

			// The pilot's name must not survive into what a practitioner reads.
			// Only these two files are held to it: the client and its tests cite
			// ThousandEyes deliberately, as the API whose observed behaviour a
			// middleware decision rests on, and a citation is not a leak.
			facing := map[string]bool{"internal/provider/provider.go": true, "main.go": true}
			for _, f := range plan.Files {
				if facing[f.Path] && strings.Contains(string(f.Content), "ThousandEyes") {
					t.Errorf("%s names ThousandEyes, but this provider is called %q",
						f.Path, params.DisplayName)
				}
			}
		})
	}
}
