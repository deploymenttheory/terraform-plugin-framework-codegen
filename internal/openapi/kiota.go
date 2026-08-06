package openapi

import (
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// This file is the kiota-shaped half of inference: chain synthesis from path
// templates and the accessor naming a kiota-generated Go SDK actually uses.
//
// The names are derived from the OpenAPI document, not by parsing the
// generated SDK -- inference deliberately runs from the pinned snapshot alone,
// preserving the draft-then-curate pipeline order. A wrong guess here is not
// silent: `bindings check` resolves every name against the real SDK and its
// did-you-mean answers with the true spelling.

// goReservedWords are the identifiers kiota mangles with an "Escaped" suffix:
// Go keywords plus the predeclared names ("error" is the live case -- kiota
// generates GetErrorEscaped).
var goReservedWords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
	"any": true, "bool": true, "byte": true, "comparable": true, "complex64": true,
	"complex128": true, "error": true, "float32": true, "float64": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"rune": true, "string": true, "uint": true, "uint8": true, "uint16": true,
	"uint32": true, "uint64": true, "uintptr": true, "true": true, "false": true,
	"iota": true, "nil": true, "append": true, "cap": true, "clear": true,
	"close": true, "complex": true, "copy": true, "delete": true, "imag": true,
	"len": true, "make": true, "max": true, "min": true, "new": true,
	"panic": true, "print": true, "println": true, "real": true, "recover": true,
}

// kiotaName renders a property or schema name the way kiota's Go generator
// does: each word's first letter upper-cased and the rest kept -- no initialism
// conventions, so accountGroupId becomes AccountGroupId, never AccountGroupID.
func kiotaName(name string) string {
	var b strings.Builder
	upperNext := true
	for _, r := range name {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ':
			upperNext = true
		case upperNext:
			b.WriteString(strings.ToUpper(string(r)))
			upperNext = false
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// kiotaModelName renders a component schema name the way kiota's Go generator
// does: the name is kept verbatim -- underscores included, so Tags_API_TagInfo
// stays Tags_API_TagInfo -- with the first letter upper-cased and separator
// characters Go cannot carry in an identifier replaced by underscores.
//
// This is deliberately not kiotaName: that renders property names, where kiota
// collapses word separators into capitalisation. Schema names pass through
// whole, and collapsing their underscores names types that do not exist.
func kiotaModelName(schemaName string) string {
	var b strings.Builder
	for i, r := range schemaName {
		switch {
		case r == '.' || r == '-' || r == ' ':
			b.WriteRune('_')
		case i == 0:
			b.WriteString(strings.ToUpper(string(r)))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// kiotaAccessorBase is the Get/Set base for one JSON property, including the
// keyword mangling: a property named "error" is reached as GetErrorEscaped.
func kiotaAccessorBase(jsonName string) string {
	base := kiotaName(jsonName)
	if goReservedWords[strings.ToLower(jsonName)] {
		base += "Escaped"
	}
	return base
}

// kiotaChain synthesises the request-builder chain for one operation from its
// path template: a literal segment is a builder method, a parameter segment a
// typed indexer, and the HTTP verb the final segment.
//
//	/tags/{tagId} + GET  ->  Tags().ByTagId(id).Get(ctx, nil)
func kiotaChain(pathTemplate, verb string, verbArgs []blueprint.Argument) []blueprint.ChainSegment {
	return kiotaChainWith(pathTemplate, verb,
		blueprint.Argument{Kind: blueprint.ArgStateField, Field: "ID"}, verbArgs)
}

// kiotaChainWith is kiotaChain with the identifier argument chosen by the
// caller: a resource reads it from state, a data source from configuration.
func kiotaChainWith(pathTemplate, verb string, idArg blueprint.Argument, verbArgs []blueprint.Argument) []blueprint.ChainSegment {
	var chain []blueprint.ChainSegment

	for _, segment := range strings.Split(strings.Trim(pathTemplate, "/"), "/") {
		if segment == "" {
			continue
		}
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			param := strings.Trim(segment, "{}")
			chain = append(chain, blueprint.ChainSegment{
				Method: "By" + kiotaName(param),
				Args:   []blueprint.Argument{idArg},
			})
			continue
		}
		chain = append(chain, blueprint.ChainSegment{Method: kiotaName(segment)})
	}

	return append(chain, blueprint.ChainSegment{Method: verb, Args: verbArgs})
}

// opResultTypes carries each operation's own result model.
type opResultTypes struct {
	create, read, update string
}

// kiotaOpResult names one operation's own response model, falling back to the
// shared response type when the operation declares none of its own.
func kiotaOpResult(d *Document, op *Operation, fallback string) string {
	name := schemaNameOf(d.operationResponseProxy(op))
	if name == "" {
		return fallback
	}
	return "models." + kiotaModelName(name) + "able"
}

// bindOperationsKiota is bindOperations for the fluent dialect: the call is a
// chain, the identifier rides mid-chain, and the trailing argument is the
// per-verb request configuration, nil until somebody curates one.
func bindOperationsKiota(r *blueprint.Resource, c Candidate, results opResultTypes) {
	ctxArg := blueprint.Argument{Kind: blueprint.ArgContext}
	bodyArg := blueprint.Argument{Kind: blueprint.ArgBody}
	nilCfg := blueprint.Argument{Kind: blueprint.ArgLiteral, Expr: "nil"}

	if c.Create != nil {
		r.Binding.Create = &blueprint.Operation{
			Style: blueprint.CallStyleFluent,
			Chain: kiotaChain(c.Create.Path, "Post",
				[]blueprint.Argument{ctxArg, bodyArg, nilCfg}),
			Return: blueprint.ReturnResultError, ResultType: results.create,
			HTTPMethod: c.Create.Method, PathTemplate: c.Create.Path,
		}
	}
	if c.Read != nil {
		r.Binding.Read = &blueprint.Operation{
			Style: blueprint.CallStyleFluent,
			Chain: kiotaChain(c.Read.Path, "Get",
				[]blueprint.Argument{ctxArg, nilCfg}),
			Return: blueprint.ReturnResultError, ResultType: results.read,
			HTTPMethod: c.Read.Method, PathTemplate: c.Read.Path,
		}
	}
	if c.Update != nil {
		verb := kiotaName(strings.ToLower(c.Update.Method))
		r.Binding.Update = &blueprint.Operation{
			Style: blueprint.CallStyleFluent,
			Chain: kiotaChain(c.Update.Path, verb,
				[]blueprint.Argument{ctxArg, bodyArg, nilCfg}),
			Return: blueprint.ReturnResultError, ResultType: results.update,
			HTTPMethod: c.Update.Method, PathTemplate: c.Update.Path,
		}
	}
	if c.Delete != nil {
		// A kiota delete returns error alone; there is no transport value and
		// no body to discard.
		r.Binding.Delete = &blueprint.Operation{
			Style: blueprint.CallStyleFluent,
			Chain: kiotaChain(c.Delete.Path, "Delete",
				[]blueprint.Argument{ctxArg, nilCfg}),
			Return:     blueprint.ReturnError,
			HTTPMethod: c.Delete.Method, PathTemplate: c.Delete.Path,
		}
	}
}
