package sdkbind

import (
	"fmt"
	"go/types"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// Problem is one binding that does not match the SDK.
type Problem struct {
	// Resource is the blueprint resource key.
	Resource string
	// Path is the blueprint field at fault, so the message names what to edit.
	Path string
	// Detail says what was expected and what the SDK actually has.
	Detail string
}

func (p Problem) String() string {
	return fmt.Sprintf("%s: %s: %s", p.Resource, p.Path, p.Detail)
}

// Report is the outcome of verifying a blueprint.
type Report struct {
	Problems []Problem
	// Checked counts the bindings that resolved, so a run that verified nothing
	// is distinguishable from one that verified everything successfully.
	Checked int
}

// Err folds the problems into a single error listing all of them.
func (r Report) Err() error {
	if len(r.Problems) == 0 {
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d binding problem(s):", len(r.Problems))
	for _, p := range r.Problems {
		fmt.Fprintf(&b, "\n  %s", p)
	}

	return fmt.Errorf("%w: %s", ErrBindings, b.String())
}

// Verify checks every resource's bindings against the SDK.
//
// It reports all problems rather than stopping at the first, because a blueprint
// written against the wrong version of an SDK is usually wrong in several places
// at once, and fixing them one CI run at a time is miserable.
func Verify(l *Loader, bp blueprint.Blueprint) Report {
	var r Report

	clientType, err := resolveClientType(l, bp)
	if err != nil {
		r.Problems = append(r.Problems, Problem{
			Resource: "provider",
			Path:     "provider.sdk.clientType",
			Detail:   err.Error(),
		})
		// Every accessor is rooted in the client type, so without it the
		// remaining checks would each repeat the same failure.
		return r
	}

	for _, res := range bp.Resources {
		if res.Drop {
			continue
		}
		verifyResource(l, bp, res, clientType, &r)
	}

	return r
}

// resolveClientType resolves the declared client type to a real type.
func resolveClientType(l *Loader, bp blueprint.Blueprint) (types.Type, error) {
	sdk := bp.Provider.SDK

	if sdk.ClientImport.Path == "" {
		return nil, fmt.Errorf("%w: provider.sdk.clientImport.path is empty, so the client type cannot be resolved",
			ErrBindings)
	}

	// "*thousandeyes.Client" -> "Client". The package qualifier is carried by
	// clientImport, so only the bare name is looked up.
	name := strings.TrimPrefix(sdk.ClientType, "*")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}

	named, err := l.LookupType(sdk.ClientImport.Path, name)
	if err != nil {
		return nil, err
	}

	return named, nil
}

func verifyResource(l *Loader, bp blueprint.Blueprint, res blueprint.Resource, clientType types.Type, r *Report) {
	svc := res.Binding.Service

	// The accessor. This is the check that pays for the package: it walks the
	// field chain from the client type and catches a service that does not hang
	// where the blueprint claims.
	if chain, ok := accessorChain(svc.Accessor); ok {
		if _, err := l.FieldChain(clientType, chain); err != nil {
			r.Problems = append(r.Problems, Problem{
				Resource: res.Key,
				Path:     "binding.service.accessor",
				Detail:   fmt.Sprintf("%q does not resolve: %v", svc.Accessor, unwrapDetail(err)),
			})
		} else {
			r.Checked++
		}
	} else {
		r.Problems = append(r.Problems, Problem{
			Resource: res.Key,
			Path:     "binding.service.accessor",
			Detail: fmt.Sprintf("%q is not of the form \"r.client.<Field>...\", so it cannot be verified",
				svc.Accessor),
		})
	}

	// The service type itself.
	if _, err := l.LookupType(svc.ImportPath, svc.TypeName); err != nil {
		r.Problems = append(r.Problems, Problem{
			Resource: res.Key,
			Path:     "binding.service.typeName",
			Detail:   unwrapDetail(err),
		})
		// Without the service type there are no methods to check.
		return
	}
	r.Checked++

	ops := map[string]*blueprint.Operation{
		"create": res.Binding.Create,
		"read":   res.Binding.Read,
		"update": res.Binding.Update,
		"delete": res.Binding.Delete,
	}
	for name, op := range ops {
		if op == nil {
			continue
		}
		verifyOperation(l, res, svc, name, *op, r)
	}

	requestOK, responseOK := verifyBodyModels(l, res, r)
	verifyWireFields(l, res, requestOK, responseOK, r)
}

func verifyOperation(
	l *Loader,
	res blueprint.Resource,
	svc blueprint.ServiceRef,
	name string,
	op blueprint.Operation,
	r *Report,
) {
	path := "binding." + name

	m, err := l.LookupMethod(svc.ImportPath, svc.TypeName, op.Method)
	if err != nil {
		r.Problems = append(r.Problems, Problem{
			Resource: res.Key,
			Path:     path + ".method",
			Detail:   unwrapDetail(err),
		})
		return
	}
	r.Checked++

	// The declared return arity must match the method's, because it decides the
	// arity of every error return in the generated body. A mismatch produces code
	// that does not compile, so catching it here is strictly better than in the
	// generated file.
	wantResults := len(m.Results)
	gotArity := arityOf(op.Return)

	if gotArity != wantResults {
		r.Problems = append(r.Problems, Problem{
			Resource: res.Key,
			Path:     path + ".return",
			Detail: fmt.Sprintf("declared %q implies %d result(s), but %s returns %d: %s",
				op.Return, gotArity, op.Method, wantResults, m.Signature()),
		})
		return
	}

	// The declared result type must match the method's first result, or the
	// generated state mapping is handed the wrong type.
	if op.Return.HasResult() && len(m.Results) > 0 {
		want := strings.TrimPrefix(op.ResultType, "*")
		got := strings.TrimPrefix(m.Results[0], "*")
		if want != got {
			r.Problems = append(r.Problems, Problem{
				Resource: res.Key,
				Path:     path + ".resultType",
				Detail: fmt.Sprintf("declared %q but %s returns %s: %s",
					op.ResultType, op.Method, m.Results[0], m.Signature()),
			})
		}
	}
}

// arityOf is the number of results a return arity implies, excluding nothing:
// the error counts, because it is part of the signature the generated assignment
// has to match.
func arityOf(a blueprint.ReturnArity) int {
	switch a {
	case blueprint.ReturnResultTransportError:
		return 3
	case blueprint.ReturnResultError, blueprint.ReturnTransportError:
		return 2
	case blueprint.ReturnError:
		return 1
	default:
		return -1
	}
}

// verifyBodyModels checks the request and response types exist, and reports which
// of them resolved.
//
// The result is used to suppress the per-field checks against a type that does not
// exist. Without that, one bad type name produces a problem per attribute -- which
// is exactly the wall of identical errors this package exists to replace.
func verifyBodyModels(l *Loader, res blueprint.Resource, r *Report) (requestOK, responseOK bool) {
	svc := res.Binding.Service

	check := func(path, expr string) bool {
		name := typeNameOf(expr)
		if name == "" {
			return false
		}
		if _, err := l.LookupType(svc.ImportPath, name); err != nil {
			r.Problems = append(r.Problems, Problem{Resource: res.Key, Path: path, Detail: unwrapDetail(err)})
			return false
		}
		r.Checked++
		return true
	}

	// Both are checked before returning, so a blueprint with two bad type names
	// reports both rather than only the first.
	requestOK = check("binding.body.requestType", res.Binding.Body.RequestType)
	responseOK = check("binding.body.responseType", res.Binding.Body.ResponseType)

	return requestOK, responseOK
}

// verifyWireFields checks that every attribute's SDK field exists on the model it
// is read from or written to.
//
// A wrong field name here is the quietest possible failure: if it happens to
// match another field of the same type the code compiles and the provider maps
// the wrong value.
func verifyWireFields(l *Loader, res blueprint.Resource, requestOK, responseOK bool, r *Report) {
	svc := res.Binding.Service

	// A type that did not resolve is already reported once; checking fields
	// against it would repeat that one cause per attribute.
	var request, response string
	if requestOK {
		request = typeNameOf(res.Binding.Body.RequestType)
	}
	if responseOK {
		response = typeNameOf(res.Binding.Body.ResponseType)
	}

	for _, a := range res.Attributes {
		if a.Drop || a.Wire.SDKField == "" {
			continue
		}

		// An attribute that is written must exist on the request model; one that
		// is read must exist on the response model. Most exist on both.
		checks := map[string]string{}
		if !a.Wire.SkipExpand && a.Wire.Expand != nil && request != "" {
			checks[request] = "expand"
		}
		if !a.Wire.SkipFlatten && a.Wire.Flatten != nil && response != "" {
			checks[response] = "flatten"
		}

		for typeName, direction := range checks {
			if _, err := l.LookupField(svc.ImportPath, typeName, a.Wire.SDKField); err != nil {
				r.Problems = append(r.Problems, Problem{
					Resource: res.Key,
					Path:     fmt.Sprintf("attributes[%s].wire.sdkField", a.Name),
					Detail:   fmt.Sprintf("%s needs it on %s: %s", direction, typeName, unwrapDetail(err)),
				})
				continue
			}
			r.Checked++
		}
	}
}

// typeNameOf reduces a qualified type expression to its bare name.
func typeNameOf(expr string) string {
	name := strings.TrimPrefix(expr, "*")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// accessorChain splits an accessor into the field names after the receiver.
//
// Accessors are of the form "r.client.API.Tags": the first two segments are the
// generated receiver and its client field, which exist by construction, and the
// rest is a chain on the SDK's client type.
func accessorChain(accessor string) ([]string, bool) {
	parts := strings.Split(accessor, ".")
	if len(parts) < 3 || parts[0] != "r" || parts[1] != "client" {
		return nil, false
	}
	return parts[2:], true
}

// unwrapDetail strips the sentinel prefix so a Problem's detail is not prefixed
// with "blueprint bindings do not match the SDK" on every line.
func unwrapDetail(err error) string {
	s := err.Error()
	return strings.TrimPrefix(s, ErrBindings.Error()+": ")
}
