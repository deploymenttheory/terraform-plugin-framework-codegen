package probe

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/cassette"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe/quirkserver"
)

// transportForTest is a real transport, to localhost, at a server the test just started -- the
// same exemption the read-tier helper takes.
func transportForTest(t *testing.T) http.RoundTripper {
	t.Helper()

	return &http.Transport{}
}

// goodProfile is a profile that passes every static condition.
//
// Every test below starts here and breaks exactly one thing, so a refusal is unambiguously
// attributable and a condition that stops being checked shows up as a test that no longer fails.
func goodProfile() Profile {
	return Profile{
		Endpoint:        "https://api.example.com/v7",
		TokenEnv:        "TFPFGEN_PROBE_TOKEN",
		Sandbox:         true,
		SandboxEvidence: "A disposable tenant created for this work and holding nothing else.",
		NamePrefix:      "tfpfgen-probe",
		Assertions: Assertions{
			EndpointHostSuffix: "example.com",
			MaxExistingObjects: 25,
		},
	}
}

func goodEnv() MapEnviron {
	return MapEnviron{"TFPFGEN_PROBE_TOKEN": "a-token-value"}
}

func goodOptions() GateOptions {
	return GateOptions{
		Mode:           ModeRecord,
		AllowMutations: true,
		Subject:        quirkSubject(),
		Plan: Plan{Fixtures: []Fixture{
			{Name: "minimal", Body: map[string]any{"key": "x"}},
		}},
	}
}

// TestUnit_Probe_GateRefusesEachConditionOnItsOwn.
//
// A table over the same shape the gate is, so every row is one thing wrong with an otherwise
// valid setup. The condition name is asserted, not just the failure: a refusal that fires for the
// wrong reason is a gate that passes for the wrong reason on the next change.
func TestUnit_Probe_GateRefusesEachConditionOnItsOwn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		condition string
		profile   func(*Profile)
		opts      func(*GateOptions)
		env       MapEnviron
	}{
		{
			condition: "mode",
			opts:      func(o *GateOptions) { o.Mode = ModeReplay },
		},
		{
			condition: "allowMutations",
			opts:      func(o *GateOptions) { o.AllowMutations = false },
		},
		{
			condition: "sandbox",
			profile:   func(p *Profile) { p.Sandbox = false },
		},
		{
			condition: "sandboxEvidence",
			profile:   func(p *Profile) { p.SandboxEvidence = "" },
		},
		{
			// Long enough, but not a reason. Writing the sentence is the check.
			condition: "sandboxEvidence",
			profile:   func(p *Profile) { p.SandboxEvidence = "yesyesyesyesyesyesyesyesyes" },
		},
		{
			condition: "namePrefix",
			profile:   func(p *Profile) { p.NamePrefix = "tf" },
		},
		{
			// Long enough, but nothing identifies the tool that made the objects.
			condition: "namePrefix",
			profile:   func(p *Profile) { p.NamePrefix = "some-long-prefix" },
		},
		{
			condition: "tokenEnv",
			profile:   func(p *Profile) { p.TokenEnv = "" },
		},
		{
			condition: "tokenEnv",
			env:       MapEnviron{},
		},
		{
			// The mistake this exists to catch: the token pasted in rather than the name of
			// the variable holding it. No shape heuristic would see it -- the value is
			// hyphenated and short -- but the gate knows what the token is.
			condition: "noCredentialInProfile",
			profile:   func(p *Profile) { p.SandboxEvidence = "A sandbox reached with a-token-value only." },
		},
		{
			condition: "noCredentialInProfile",
			profile: func(p *Profile) {
				p.RedactValues = map[string]string{"a-token-value": "REDACTED"}
			},
		},
		{
			condition: "https",
			profile:   func(p *Profile) { p.Endpoint = "http://api.example.com" },
		},
		{
			condition: "https",
			profile:   func(p *Profile) { p.Endpoint = "not a url at all" },
		},
		{
			condition: "endpointHostSuffix",
			profile:   func(p *Profile) { p.Endpoint = "https://api.somewhere-else.com/v7" },
		},
		{
			condition: "canMutate",
			opts:      func(o *GateOptions) { o.Subject.Create = nil },
		},
		{
			condition: "canMutate",
			opts:      func(o *GateOptions) { o.Subject.IDField = "" },
		},
		{
			condition: "plan",
			opts: func(o *GateOptions) {
				o.Plan.Deny = []string{"nonexistent-field"}
			},
		},
		{
			// `probe -list` works with no fixture; a mutating run cannot.
			condition: "plan",
			opts:      func(o *GateOptions) { o.Plan.Fixtures = nil },
		},
		{
			condition: "noSnapshotOverwrite",
			opts:      func(o *GateOptions) { o.EquivalentSnapshotExists = true },
		},
	}

	for _, tc := range tests {
		t.Run(tc.condition+"/"+describeBreakage(tc.profile != nil), func(t *testing.T) {
			t.Parallel()

			profile := goodProfile()
			if tc.profile != nil {
				tc.profile(&profile)
			}

			opts := goodOptions()
			if tc.opts != nil {
				tc.opts(&opts)
			}

			env := goodEnv()
			if tc.env != nil {
				env = tc.env
			}

			// No session: a refusal on static grounds must not need one, which is itself the
			// two-tier property.
			_, _, err := Authorise(context.Background(), nil, profile, opts, env)
			if !errors.Is(err, ErrRefused) {
				t.Fatalf("error = %v, want ErrRefused", err)
			}
			if !strings.Contains(err.Error(), tc.condition+":") {
				t.Errorf("the refusal does not name %q:\n%v", tc.condition, err)
			}
		})
	}
}

func describeBreakage(profile bool) string {
	if profile {
		return "profile"
	}
	return "options"
}

// TestUnit_Probe_GateReportsEveryUnmetConditionAtOnce.
//
// An operator fixing a profile one refusal per attempt is an operator making several more
// requests against a tenant the tool has already decided it does not want to write to.
func TestUnit_Probe_GateReportsEveryUnmetConditionAtOnce(t *testing.T) {
	t.Parallel()

	// Nothing set at all, which is what running the command with no profile looks like.
	_, _, err := Authorise(context.Background(), nil, Profile{}, GateOptions{
		Mode:    ModeReplay,
		Subject: Subject{},
	}, MapEnviron{})

	if !errors.Is(err, ErrRefused) {
		t.Fatalf("error = %v, want ErrRefused", err)
	}

	for _, want := range []string{
		"mode:", "allowMutations:", "sandbox:", "sandboxEvidence:", "namePrefix:",
		"tokenEnv:", "https:", "canMutate:",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal omits %q:\n%v", want, err)
		}
	}

	// And it says how many, so a reader knows whether they are looking at the whole list.
	if !strings.Contains(err.Error(), "condition(s) were not met") {
		t.Errorf("the refusal should count itself:\n%v", err)
	}
}

// TestUnit_Probe_EveryStaticConditionIsReachable is the completeness test.
//
// The gate is a table so that a condition dropped in a refactor cannot pass unnoticed. This
// asserts the table's own shape: every entry has a name and a check, no name repeats, and every
// entry applies to at least one mode -- an entry applying to none would be decorative.
func TestUnit_Probe_EveryStaticConditionIsReachable(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for i, c := range staticConditions {
		if c.name == "" {
			t.Errorf("condition %d has no name, so no refusal could identify it", i)
		}
		if c.check == nil {
			t.Errorf("condition %q has no check, so it is documentation rather than a gate", c.name)
		}
		if len(c.modes) == 0 {
			t.Errorf("condition %q applies to no mode, so it never runs", c.name)
		}
		if seen[c.name] {
			// Two entries with the same name are allowed -- namePrefix and tokenEnv both have
			// several failure modes -- but they must be adjacent, or a reader scanning the
			// table will believe they have seen all of one condition when they have not.
			if staticConditions[i-1].name != c.name {
				t.Errorf("condition %q reappears away from its siblings", c.name)
			}
		}
		seen[c.name] = true
	}

	// The conditions a sweep is gated on, stated here so a change to the filter has to be
	// deliberate. A sweep must NOT require allowMutations -- demanding the mutation flag to
	// clean up after yourself is perverse -- nor maxExistingObjects, because a tenant that now
	// fails it may be failing it precisely because it holds your orphans.
	wantSweep := map[string]bool{
		"namePrefix": true, "tokenEnv": true, "noCredentialInProfile": true,
		"https": true, "endpointHostSuffix": true,
	}

	for _, c := range staticConditions {
		if c.appliesTo(ModeSweep) != wantSweep[c.name] {
			t.Errorf("condition %q: appliesTo(sweep) = %v, want %v",
				c.name, c.appliesTo(ModeSweep), wantSweep[c.name])
		}
	}
}

// TestUnit_Probe_TheGateIssuesNoRequestUntilTheStaticTierIsClean.
//
// The two tiers exist because "report every unmet condition" and "read the tenant before writing
// to it" pull against each other. Spending somebody's rate-limit allowance to enumerate runtime
// failures against a tenant already refused is spending it to learn nothing.
func TestUnit_Probe_TheGateIssuesNoRequestUntilTheStaticTierIsClean(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	read, err := NewReadSession(SessionConfig{
		Transport:          transportForTest(t),
		BaseURL:            srv.BaseURL(),
		CollectionTemplate: "/things",
		ItemTemplate:       "/things/{id}",
	})
	if err != nil {
		t.Fatalf("NewReadSession: %v", err)
	}

	profile := goodProfile()
	profile.Sandbox = false

	if _, _, err := Authorise(context.Background(), read, profile, goodOptions(), goodEnv()); !errors.Is(err, ErrRefused) {
		t.Fatalf("error = %v, want ErrRefused", err)
	}

	if srv.Requests() != 0 {
		t.Errorf("%d request(s) were issued against a tenant refused on static grounds",
			srv.Requests())
	}
}

// TestUnit_Probe_MaxExistingObjectsIsTheEmpiricalAssertion.
//
// Profile.Sandbox is a claim. This is evidence: a tenant holding four objects is a sandbox, one
// holding nine hundred is production, and no amount of configuration can misrepresent that.
func TestUnit_Probe_MaxExistingObjectsIsTheEmpiricalAssertion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		seed    int
		max     int
		refused bool
	}{
		{"an empty tenant", 0, 3, false},
		{"a small tenant", 2, 3, false},
		{"a tenant at the limit", 3, 3, true},
		{"a full tenant", 10, 3, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := quirkserver.New(t, quirkserver.Quirks{})
			for i := range tc.seed {
				srv.Seed(map[string]any{"key": "existing", "value": string(rune('a' + i))})
			}

			read, err := NewReadSession(SessionConfig{
				Transport:          transportForTest(t),
				BaseURL:            srv.BaseURL(),
				CollectionTemplate: "/things",
				ItemTemplate:       "/things/{id}",
			})
			if err != nil {
				t.Fatalf("NewReadSession: %v", err)
			}

			profile := goodProfile()
			profile.Assertions.MaxExistingObjects = tc.max

			grant, passed, err := Authorise(context.Background(), read, profile, goodOptions(), goodEnv())

			if tc.refused {
				if !errors.Is(err, ErrRefused) {
					t.Fatalf("error = %v, want ErrRefused", err)
				}
				if !strings.Contains(err.Error(), "not a sandbox") {
					t.Errorf("the refusal should say what it concluded:\n%v", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("Authorise: %v", err)
			}
			if grant == nil {
				t.Fatal("a passing gate must issue a grant")
			}
			if grant.NamePrefix() != profile.NamePrefix {
				t.Errorf("the grant carries %q, want the profile's prefix", grant.NamePrefix())
			}

			// The report records what evidence stood behind the run. A report saying only
			// "sandbox: true" would be recording the claim rather than the evidence.
			var found bool
			for _, p := range passed {
				if strings.HasPrefix(p, "maxExistingObjects") {
					found = true
					if !strings.Contains(p, "<") {
						t.Errorf("the passed assertion should carry the numbers: %q", p)
					}
				}
			}
			if !found {
				t.Errorf("passed = %v, want maxExistingObjects among them", passed)
			}
		})
	}
}

// TestUnit_Probe_AnUncheckedAssertionDoesNotReadAsAPassedOne: without a session the runtime tier
// cannot check anything, and silently treating that as success is how a gate becomes decoration.
func TestUnit_Probe_AnUncheckedAssertionDoesNotReadAsAPassedOne(t *testing.T) {
	t.Parallel()

	_, _, err := Authorise(context.Background(), nil, goodProfile(), goodOptions(), goodEnv())
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("error = %v, want ErrRefused", err)
	}
	if !strings.Contains(err.Error(), "maxExistingObjects") {
		t.Errorf("the refusal should name the assertion it could not check:\n%v", err)
	}
}

// TestUnit_Probe_AccountGroupRecordsTheWeakerOutcomeVerbatim.
//
// An empty sandbox cannot confirm the scope from object data, so the assertion rests on the
// scoped read alone -- and says so. A gate recording the strong claim from the weak evidence
// would be worse than one recording nothing.
func TestUnit_Probe_AccountGroupRecordsTheWeakerOutcomeVerbatim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// seed is the object the tenant holds, or nil for an empty one.
		seed     map[string]any
		jsonPath string
		param    string
		want     string
		refused  string
	}{
		{
			name:  "an empty tenant confirms nothing",
			param: "aid", jsonPath: "aid",
			want: "the tenant is empty",
		},
		{
			name:  "no json path means no object can confirm it",
			seed:  map[string]any{"key": "x"},
			param: "aid",
			want:  "no accountGroupJsonPath was given",
		},
		{
			name:  "an object carrying the group id confirms it",
			seed:  map[string]any{"key": "x", "aid": "1234"},
			param: "aid", jsonPath: "aid",
			want: "confirmed by an object's aid",
		},
		{
			name:  "an object carrying a different group id is a refusal",
			seed:  map[string]any{"key": "x", "aid": "9999"},
			param: "aid", jsonPath: "aid",
			refused: "reaching a different account group",
		},
		{
			name:     "no parameter means the assertion is unimplementable",
			seed:     map[string]any{"key": "x"},
			jsonPath: "aid",
			refused:  "names no accountGroupParam",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := quirkserver.New(t, quirkserver.Quirks{IgnoresUnknownQueryParams: true})
			if tc.seed != nil {
				srv.Seed(tc.seed)
			}

			read, err := NewReadSession(SessionConfig{
				Transport:          transportForTest(t),
				BaseURL:            srv.BaseURL(),
				CollectionTemplate: "/things",
				ItemTemplate:       "/things/{id}",
			})
			if err != nil {
				t.Fatalf("NewReadSession: %v", err)
			}

			profile := goodProfile()
			profile.Assertions.AccountGroupID = "1234"
			profile.Assertions.AccountGroupParam = tc.param
			profile.Assertions.AccountGroupJSONPath = tc.jsonPath

			_, passed, err := Authorise(context.Background(), read, profile, goodOptions(), goodEnv())

			if tc.refused != "" {
				if !errors.Is(err, ErrRefused) {
					t.Fatalf("error = %v, want ErrRefused", err)
				}
				if !strings.Contains(err.Error(), tc.refused) {
					t.Errorf("the refusal should say %q:\n%v", tc.refused, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("Authorise: %v", err)
			}

			var outcome string
			for _, p := range passed {
				if strings.HasPrefix(p, "accountGroupId") {
					outcome = p
				}
			}
			if !strings.Contains(outcome, tc.want) {
				t.Errorf("outcome = %q, want it to say %q", outcome, tc.want)
			}
		})
	}
}

// TestUnit_Probe_AuthoriseSweepIsDeliberatelyWeaker.
//
// Demanding --allow-mutations in order to clean up after yourself is perverse, and an operator
// staring at an orphan table should not have to re-read the documentation. maxExistingObjects is
// dropped for a subtler reason: a tenant that now fails it may be failing it precisely because it
// is holding your orphans.
func TestUnit_Probe_AuthoriseSweepIsDeliberatelyWeaker(t *testing.T) {
	t.Parallel()

	profile := goodProfile()
	profile.Assertions.MaxExistingObjects = 1

	opts := GateOptions{Mode: ModeSweep, AllowMutations: false}

	grant, err := AuthoriseSweep(profile, opts, goodEnv())
	if err != nil {
		t.Fatalf("AuthoriseSweep: %v", err)
	}
	if grant.NamePrefix() != profile.NamePrefix {
		t.Errorf("the sweep grant must carry the prefix, got %q", grant.NamePrefix())
	}

	// It is not a free pass. A sweep still needs a credential and a prefix, because without
	// either it cannot do anything except fail confusingly.
	for _, tc := range []struct {
		name    string
		mutate  func(*Profile)
		env     MapEnviron
		wantsay string
	}{
		{"no prefix", func(p *Profile) { p.NamePrefix = "" }, goodEnv(), "namePrefix"},
		{"no token", nil, MapEnviron{}, "tokenEnv"},
		{"plain http", func(p *Profile) { p.Endpoint = "http://api.example.com" }, goodEnv(), "https"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := goodProfile()
			if tc.mutate != nil {
				tc.mutate(&p)
			}

			_, err := AuthoriseSweep(p, opts, tc.env)
			if !errors.Is(err, ErrRefused) {
				t.Fatalf("error = %v, want ErrRefused", err)
			}
			if !strings.Contains(err.Error(), tc.wantsay) {
				t.Errorf("the refusal should name %q:\n%v", tc.wantsay, err)
			}
		})
	}
}

// TestUnit_Probe_ACredentialInTheProfileIsRefused.
//
// The profile is a file somebody writes down and often commits. Two independent checks, because
// they catch different mistakes: a key *named* like a credential, and a credential-shaped *value*
// under an innocent key.
func TestUnit_Probe_ACredentialInTheProfileIsRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		want string
	}{
		{
			name: "a key named like a credential",
			json: `{"endpoint":"https://api.example.com","apiKey":"anything"}`,
			want: "apiKey",
		},
		{
			name: "a nested key named like a credential",
			json: `{"redactValues":{"x":"y"},"assertions":{"bearerToken":"x"}}`,
			want: "bearerToken",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var generic map[string]any
			if err := json.Unmarshal([]byte(tc.json), &generic); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}

			if got := credentialNamedKey(generic); got != tc.want {
				t.Errorf("credentialNamedKey = %q, want %q", got, tc.want)
			}
		})
	}

	// tokenEnv and secretEnv hold variable *names*, which is the design. Refusing them would
	// make the safe pattern the one you cannot express.
	safe := map[string]any{"tokenEnv": "TFPFGEN_PROBE_TOKEN", "secretEnv": []any{"OTHER"}}
	if got := credentialNamedKey(safe); got != "" {
		t.Errorf("credentialNamedKey refused the safe pattern: %q", got)
	}

	// redactValues legitimately holds secret-shaped strings -- that is its entire purpose -- so
	// it must not trip the shape scanner it feeds.
	profile := goodProfile()
	profile.RedactValues = map[string]string{
		"AKIAIOSFODNN7EXAMPLE": "REDACTED",
	}

	if why := credentialShaped(profile, "a-different-token"); why != "" {
		t.Errorf("redactValues tripped the scanner it feeds: %s", why)
	}

	// The exact-token check is the one a shape heuristic cannot do: this value is hyphenated and
	// well under the forty unbroken base64 characters cassette.Scan's unstructured rule needs.
	token := "01ab-abcdef01-2345-6789-abcd-ef0123456789"

	if findings := cassette.Scan("profile", []byte(token), nil); len(findings) != 0 {
		t.Fatalf("this test assumes the shape scanner does not see this value; it found %v",
			findings)
	}

	leaked := goodProfile()
	leaked.RedactValues = map[string]string{token: "REDACTED"}

	if why := credentialShaped(leaked, token); !strings.Contains(why, "TFPFGEN_PROBE_TOKEN") {
		t.Errorf("the token in the profile was not caught: %q", why)
	}

	// A short value is not looked for verbatim: a match would be far likelier to be a
	// coincidence than a credential, and refusing on one would be unfixable.
	if why := credentialShaped(goodProfile(), "api"); why != "" {
		t.Errorf("a three-character token should not be matched verbatim: %s", why)
	}
}

// TestUnit_Probe_OSEnvironReadsTheProcess: the production Environ, checked once so the interface
// is not the only thing ever exercised.
func TestUnit_Probe_OSEnvironReadsTheProcess(t *testing.T) {
	t.Setenv("TFPFGEN_TEST_GATE_VAR", "present")

	if v, ok := (OSEnviron{}).Lookup("TFPFGEN_TEST_GATE_VAR"); !ok || v != "present" {
		t.Errorf("Lookup = %q, %v", v, ok)
	}
	if _, ok := (OSEnviron{}).Lookup("TFPFGEN_TEST_GATE_ABSENT"); ok {
		t.Error("an unset variable must report absent")
	}

	// A nil Environ falls back to the process, so a caller cannot accidentally build a gate
	// that reads nothing and passes.
	profile := goodProfile()
	profile.TokenEnv = "TFPFGEN_TEST_GATE_VAR"

	if _, _, err := Authorise(context.Background(), nil, profile, goodOptions(), nil); err == nil {
		t.Fatal("this should still be refused on the runtime tier")
	} else if strings.Contains(err.Error(), "tokenEnv:") {
		t.Errorf("a nil Environ did not fall back to the process:\n%v", err)
	}
}
