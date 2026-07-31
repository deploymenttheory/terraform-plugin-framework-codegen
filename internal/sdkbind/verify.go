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

		// A list facet has its own service, method and response, none of which the
		// resource's checks touch.
		if res.List != nil {
			verifyListFacet(l, res, *res.List, clientType, &r)
		}
	}

	// Data sources and actions were unverified until actions were generated: this package
	// walked bp.Resources and nothing else, so a data source naming an SDK method that does
	// not exist produced a provider that failed to compile with no warning from here.
	for _, d := range bp.DataSources {
		if d.Drop {
			continue
		}
		verifyDataSource(l, d, clientType, &r)
	}

	for _, a := range bp.Actions {
		if a.Drop {
			continue
		}
		verifyAction(l, a, clientType, &r)
	}

	// Added with the block kind, not after it: the bindings job previously reported a
	// reassuring count while whole kinds went unchecked, and that is the failure this
	// walk-per-kind shape exists to prevent.
	for _, e := range bp.Ephemerals {
		if e.Drop {
			continue
		}
		verifyEphemeral(l, e, clientType, &r)
	}

	return r
}

// verifyEphemeral checks an ephemeral's binding.
//
// Shaped like a data source's, because the binding is: one operation, a response model,
// and only the flatten direction to check. Renew and close are validated away before
// anything reaches here.
func verifyEphemeral(
	l *Loader,
	e blueprint.Ephemeral,
	clientType types.Type,
	r *Report,
) {
	svc := e.Binding.Service

	verifyAccessor(l, clientType, e.Key, "binding.service.accessor", svc, r)

	responseOK := verifyNamedType(
		l,
		e.Key,
		"binding.response.type",
		e.Binding.Response.Type,
		svc,
		r,
	)

	if e.Binding.Open != nil {
		verifyOperation(l, e.Key, svc, "open", *e.Binding.Open, r)
	}

	if !responseOK {
		return
	}

	response := typeNameOf(e.Binding.Response.Type)

	for _, a := range e.Schema.Attributes {
		if a.Drop || a.Wire.SDKField == "" || a.Wire.SkipFlatten || a.Wire.Flatten == nil {
			continue
		}
		verifyFieldOn(
			l, e.Key,
			fmt.Sprintf("schema.attributes[%s].wire.sdkField", a.Name),
			response, a.Wire.SDKField, svc, r,
		)
	}
}

// verifyDataSource checks a data source's binding.
//
// Narrower than a resource's: one operation, and only the flatten direction to check, because
// a data source sends no request body.
func verifyDataSource(
	l *Loader,
	d blueprint.DataSource,
	clientType types.Type,
	r *Report,
) {
	svc := d.Binding.Service

	verifyAccessor(l, clientType, d.Key, "binding.service.accessor", svc, r)

	responseOK := verifyNamedType(
		l,
		d.Key,
		"binding.response.type",
		d.Binding.Response.Type,
		svc,
		r,
	)

	if d.Binding.Read != nil {
		verifyOperation(l, d.Key, svc, "read", *d.Binding.Read, r)
	}

	if !responseOK {
		// The type is already reported once; checking fields against it would repeat that
		// one cause per attribute.
		return
	}

	response := typeNameOf(d.Binding.Response.Type)

	for _, a := range d.Schema.Attributes {
		if a.Drop || a.Wire.SDKField == "" || a.Wire.SkipFlatten || a.Wire.Flatten == nil {
			continue
		}
		verifyFieldOn(
			l, d.Key,
			fmt.Sprintf("schema.attributes[%s].wire.sdkField", a.Name),
			response, a.Wire.SDKField, svc, r,
		)
	}
}

// verifyListFacet checks a list facet's binding.
//
// The element type matters as much as the response: identityFrom and displayNameFrom name
// fields on one element, and a typo there is a compile error in the generated provider.
func verifyListFacet(
	l *Loader,
	res blueprint.Resource,
	lf blueprint.ListFacet,
	clientType types.Type,
	r *Report,
) {
	svc := lf.Service

	verifyAccessor(l, clientType, res.Key, "list.service.accessor", svc, r)
	verifyNamedType(l, res.Key, "list.response.type", lf.Response.Type, svc, r)

	if lf.Read != nil {
		verifyOperation(l, res.Key, svc, "read", *lf.Read, r)
	}

	if !verifyNamedType(l, res.Key, "list.elementType", lf.ElementType, svc, r) {
		return
	}

	element := typeNameOf(lf.ElementType)

	for i, m := range lf.IdentityFrom {
		verifyFieldOn(
			l, res.Key,
			fmt.Sprintf("list.identityFrom[%d].fromSdkField", i),
			element, m.FromSDKField, svc, r,
		)
	}

	verifyFieldOn(l, res.Key, "list.displayNameFrom", element, lf.DisplayNameFrom, svc, r)
}

// verifyAction checks an action's binding.
//
// The narrowest of the four: an accessor and one method. An action sends its arguments as call
// parameters rather than a body and writes nothing back, so there is no request or response
// model to check fields against -- which is why its attributes carry no wire conversions.
func verifyAction(
	l *Loader,
	a blueprint.Action,
	clientType types.Type,
	r *Report,
) {
	svc := a.Binding.Service

	verifyAccessor(l, clientType, a.Key, "binding.service.accessor", svc, r)

	if a.Binding.Invoke != nil {
		verifyOperation(l, a.Key, svc, "invoke", *a.Binding.Invoke, r)
	}
}

// verifyAccessor walks the field chain from the client type to the service.
//
// The check that pays for the package: it catches a service that does not hang where the
// blueprint claims. Extracted from verifyResource once data sources, list facets and actions
// each turned out to need it -- until then this package walked bp.Resources and nothing else,
// so three quarters of the bindings in a blueprint were unverified.
//
// The key and path are parameters rather than read off a resource, because a Problem needs to
// say which block it belongs to and each kind names its binding differently.
func verifyAccessor(
	l *Loader,
	clientType types.Type,
	key, path string,
	svc blueprint.ServiceRef,
	r *Report,
) {
	chain, ok := accessorChain(svc.Accessor)
	if !ok {
		r.Problems = append(r.Problems, Problem{
			Resource: key,
			Path:     path,
			Detail: fmt.Sprintf(
				"%q is not of the form \"<receiver>.client.<Field>...\", so it cannot be verified",
				svc.Accessor,
			),
		})

		return
	}

	if _, err := l.FieldChain(clientType, chain); err != nil {
		r.Problems = append(r.Problems, Problem{
			Resource: key,
			Path:     path,
			Detail:   fmt.Sprintf("%q does not resolve: %v", svc.Accessor, unwrapDetail(err)),
		})

		return
	}

	r.Checked++
}

// verifyNamedType checks that a Go type named in a binding exists in the SDK package.
func verifyNamedType(l *Loader, key, path, expr string, svc blueprint.ServiceRef, r *Report) bool {
	name := typeNameOf(expr)
	if name == "" {
		return false
	}

	if _, err := l.LookupType(svc.ImportPath, name); err != nil {
		r.Problems = append(
			r.Problems,
			Problem{Resource: key, Path: path, Detail: unwrapDetail(err)},
		)

		return false
	}

	r.Checked++

	return true
}

// verifyFieldOn checks that a named field exists on a named SDK type.
//
// A wrong field name is the quietest possible failure: if it happens to match another field
// of the same type the code compiles and the provider moves the wrong value.
func verifyFieldOn(
	l *Loader,
	key, path, typeName, field string,
	svc blueprint.ServiceRef,
	r *Report,
) {
	if typeName == "" || field == "" {
		return
	}

	if _, err := l.LookupField(svc.ImportPath, typeName, field); err != nil {
		r.Problems = append(r.Problems, Problem{
			Resource: key,
			Path:     path,
			Detail:   fmt.Sprintf("%s needs it on %s: %s", field, typeName, unwrapDetail(err)),
		})

		return
	}

	r.Checked++
}

// resolveClientType resolves the declared client type to a real type.
func resolveClientType(l *Loader, bp blueprint.Blueprint) (types.Type, error) {
	sdk := bp.Provider.SDK

	if sdk.ClientImport.Path == "" {
		return nil, fmt.Errorf(
			"%w: provider.sdk.clientImport.path is empty, so the client type cannot be resolved",
			ErrBindings,
		)
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

func verifyResource(
	l *Loader,
	bp blueprint.Blueprint,
	res blueprint.Resource,
	clientType types.Type,
	r *Report,
) {
	svc := res.Binding.Service

	verifyAccessor(l, clientType, res.Key, "binding.service.accessor", svc, r)

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
		verifyOperation(l, res.Key, svc, name, *op, r)
	}

	requestOK, responseOK := verifyBodyModels(l, res, r)
	verifyWireFields(l, res, requestOK, responseOK, r)
}

func verifyOperation(
	l *Loader,
	key string,
	svc blueprint.ServiceRef,
	name string,
	op blueprint.Operation,
	r *Report,
) {
	path := "binding." + name

	m, err := l.LookupMethod(svc.ImportPath, svc.TypeName, op.Method)
	if err != nil {
		r.Problems = append(r.Problems, Problem{
			Resource: key,
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
			Resource: key,
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
				Resource: key,
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
			r.Problems = append(
				r.Problems,
				Problem{Resource: res.Key, Path: path, Detail: unwrapDetail(err)},
			)
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

	for _, a := range res.Schema.Attributes {
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
					Detail: fmt.Sprintf(
						"%s needs it on %s: %s",
						direction,
						typeName,
						unwrapDetail(err),
					),
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

	// The receiver name is whatever the generated method uses -- r for a resource, d for a
	// data source, l for a list resource, a for an action -- so it is not checked, only
	// skipped. It was pinned to "r" while this package walked resources and nothing else,
	// which meant every other kind's accessor came back unverifiable rather than verified.
	//
	// What must hold is the shape: a receiver, a field named client, and at least one field
	// after it. The field chain from client onwards is the part that is resolved against the
	// real client type, and that is where a wrong accessor is actually caught.
	if len(parts) < 3 || parts[0] == "" || parts[1] != "client" {
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
