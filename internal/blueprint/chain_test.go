package blueprint

import (
	"strings"
	"testing"
)

// problemsOf runs one Operation's validation and returns the joined messages.
func problemsOf(o Operation) string {
	var p problems
	o.validate("op", &p)
	return renderProblems(p)
}

func renderProblems(p problems) string {
	if err := p.err(); err != nil {
		return err.Error()
	}
	return ""
}

func TestUnit_Blueprint_ChainRules(t *testing.T) {
	t.Parallel()

	ctx := Argument{Kind: ArgContext}
	nilCfg := Argument{Kind: ArgLiteral, Expr: "nil"}
	id := Argument{Kind: ArgStateField, Field: "ID"}

	goodChain := []ChainSegment{
		{Method: "Tags"},
		{Method: "ByTagId", Args: []Argument{id}},
		{Method: "Get", Args: []Argument{ctx, nilCfg}},
	}

	for name, tc := range map[string]struct {
		op   Operation
		want string // substring of a problem; empty means only the reserved-style refusal may appear
	}{
		"a well-formed fluent chain has only the reserved refusal": {
			op: Operation{Style: CallStyleFluent, Chain: goodChain, Return: ReturnResultError, ResultType: "models.Tagable"},
		},
		"a chain on a method-style call is refused": {
			op:   Operation{Style: CallStyleMethod, Method: "GetTag", Chain: goodChain, Return: ReturnResultError, ResultType: "T"},
			want: "chained call is style fluent",
		},
		"a one-segment chain is refused": {
			op:   Operation{Style: CallStyleFluent, Chain: goodChain[:1], Return: ReturnError},
			want: "at least a builder segment and a verb segment",
		},
		"a method on a fluent call is refused": {
			op:   Operation{Style: CallStyleFluent, Method: "Get", Chain: goodChain, Return: ReturnResultError, ResultType: "T"},
			want: "takes its verb from the chain's last segment",
		},
		"operation-level args on a fluent call are refused": {
			op:   Operation{Style: CallStyleFluent, Chain: goodChain, Args: []Argument{ctx}, Return: ReturnResultError, ResultType: "T"},
			want: "carries arguments on chain segments",
		},
		"a transport arity on a fluent call is refused": {
			op:   Operation{Style: CallStyleFluent, Chain: goodChain, Return: ReturnResultTransportError, ResultType: "T"},
			want: "does not return",
		},
		"an unnamed segment is refused": {
			op: Operation{Style: CallStyleFluent, Return: ReturnError, Chain: []ChainSegment{
				{Method: "Tags"}, {Method: ""},
			}},
			want: "chain[1].method",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := problemsOf(tc.op)
			if tc.want == "" {
				// Until the emitter implements fluent calls the style itself is
				// refused; a structurally sound chain must add nothing beyond that.
				if !strings.Contains(got, "1 problem(s)") || !strings.Contains(got, "reserved") {
					t.Fatalf("want only the reserved-style refusal, got: %s", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("problems = %q, want them to mention %q", got, tc.want)
			}
		})
	}
}

func TestUnit_Blueprint_SDKModeRules(t *testing.T) {
	t.Parallel()

	base := func() Provider {
		var pr Provider
		pr.Name = "x"
		pr.GoModule = "example.com/prov"
		pr.TypePrefix = "x"
		pr.SDK = SDKModule{Dialect: DialectRestyService, ModulePath: "example.com/sdk", ClientType: "*c.C"}
		return pr
	}

	t.Run("embed demands a module-internal path", func(t *testing.T) {
		t.Parallel()
		pr := base()
		pr.SDK.Mode = SDKModeEmbed
		var p problems
		pr.validate(&p)
		if got := renderProblems(p); !strings.Contains(got, "mode embed requires") {
			t.Fatalf("embed with a foreign module path must be refused, got: %s", got)
		}
	})

	t.Run("embed with the provider module passes", func(t *testing.T) {
		t.Parallel()
		pr := base()
		pr.SDK.Mode = SDKModeEmbed
		pr.SDK.ModulePath = pr.GoModule
		var p problems
		pr.validate(&p)
		if got := renderProblems(p); got != "" {
			t.Fatalf("unexpected problems: %s", got)
		}
	})

	t.Run("external with the provider module is really embed", func(t *testing.T) {
		t.Parallel()
		pr := base()
		pr.SDK.ModulePath = pr.GoModule
		var p problems
		pr.validate(&p)
		if got := renderProblems(p); !strings.Contains(got, "which is mode embed") {
			t.Fatalf("an external declaration of the provider's own module must be named, got: %s", got)
		}
	})

	t.Run("an unknown mode is refused", func(t *testing.T) {
		t.Parallel()
		pr := base()
		pr.SDK.Mode = "sideways"
		var p problems
		pr.validate(&p)
		if got := renderProblems(p); !strings.Contains(got, "not embed or external") {
			t.Fatalf("unknown mode must be refused, got: %s", got)
		}
	})
}
