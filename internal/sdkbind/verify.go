package sdkbind

import (
	"fmt"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
	"go/types"
	"sort"
	"strings"
)

// Problem is one binding that does not match the SDK, named against the
// intermediate representation rather than as a compile error.
type Problem struct {
	// Kind is "resource", "datasource", "list_resource" or "action".
	Kind string
	// Key is the entity key.
	Key string
	// Path names the binding field at fault, e.g. "read.expr" or
	// "fields[display_name].get".
	Path string
	// Detail says what was expected and what the SDK actually has.
	Detail string
}

func (p Problem) String() string {
	return fmt.Sprintf("%s %s: %s: %s", p.Kind, p.Key, p.Path, p.Detail)
}

// Report is the outcome of verifying a binding set.
type Report struct {
	Problems []Problem
	// Checked counts the bindings that resolved, so a run that verified
	// nothing is distinguishable from one that verified everything.
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

// Verify type-checks every binding against the generated SDK under
// sdkDir. It mutates nothing and reports all problems rather than
// stopping at the first: a binding set wrong against an SDK is usually
// wrong in several places at once, and fixing them one run at a time is
// miserable. The returned error is for a set that could not be checked at
// all; per-binding failures are Problems.
func Verify(b *Bindings, sdkDir string) (Report, error) {
	l, err := loadSDK(sdkDir)
	if err != nil {
		return Report{}, err
	}
	return verifyLoaded(l, b), nil
}

func verifyLoaded(l *loader, b *Bindings) Report {
	var r Report

	client, err := l.lookupType(b.SDK.ImportPath, b.SDK.ClientTypeName)
	if err != nil {
		r.Problems = append(r.Problems, Problem{
			Kind: "provider", Key: "client", Path: "sdk.client_type_name",
			Detail: unwrapDetail(err),
		})
		// Every call is rooted in the client type; without it the
		// remaining checks would each repeat the same failure.
		return r
	}
	clientType := types.Type(client)

	v := &verifier{l: l, info: b.SDK, client: clientType, r: &r}

	for _, key := range sortedKeys(b.Resources) {
		v.resource(b.Resources[key])
	}
	for _, key := range sortedKeys(b.Datasources) {
		v.datasource(b.Datasources[key])
	}
	for _, key := range sortedKeys(b.ListResources) {
		v.listResource(b.ListResources[key])
	}
	for _, key := range sortedKeys(b.Actions) {
		v.action(b.Actions[key])
	}

	return r
}

func sortedKeys[T any](m map[string]*T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type verifier struct {
	l      *loader
	info   SDKInfo
	client types.Type
	r      *Report
}

func (v *verifier) problem(kind, key, path, format string, args ...any) {
	v.r.Problems = append(v.r.Problems, Problem{
		Kind: kind, Key: key, Path: path, Detail: fmt.Sprintf(format, args...),
	})
}

func (v *verifier) resource(rb *ResourceBinding) {
	const kind = string(specmodel.KindResource)
	for name, call := range map[string]*Call{
		"create": rb.Create, "read": rb.Read, "update": rb.Update, "delete": rb.Delete,
	} {
		if call != nil {
			v.call(kind, rb.Key, name, call)
		}
	}
	read, write := v.fieldModels(kind, rb.Key, rb.ReadModel, rb.WriteModel)
	v.fields(kind, rb.Key, "fields", rb.Fields, read, write)
}

func (v *verifier) datasource(db *DatasourceBinding) {
	const kind = string(specmodel.KindDatasource)
	if db.Read != nil {
		v.call(kind, db.Key, "read", db.Read)
	}
	if db.List != nil {
		v.call(kind, db.Key, "list", db.List)
		v.collection(kind, db.Key, db.List, db.ElementType, db.CollectionAccess)
	}
	model := db.ElementType
	if model == "" {
		model = db.ReadModel
	}
	read, _ := v.fieldModels(kind, db.Key, model, "")
	v.fields(kind, db.Key, "fields", db.Fields, read, nil)
}

func (v *verifier) listResource(lb *ListResourceBinding) {
	const kind = string(specmodel.KindListResource)
	if lb.List != nil {
		v.call(kind, lb.Key, "list", lb.List)
		v.collection(kind, lb.Key, lb.List, lb.ElementType, lb.CollectionAccess)
	}
	read, _ := v.fieldModels(kind, lb.Key, lb.ElementType, "")
	v.fields(kind, lb.Key, "fields", lb.Fields, read, nil)
}

func (v *verifier) action(ab *ActionBinding) {
	const kind = string(specmodel.KindAction)
	if ab.Invoke != nil {
		v.call(kind, ab.Key, "invoke", ab.Invoke)
	}
	_, write := v.fieldModels(kind, ab.Key, "", ab.WriteModel)
	v.fields(kind, ab.Key, "fields", ab.Fields, nil, write)
}

// fieldModels resolves the entity's declared read and write models,
// reporting each miss once — the per-field checks then skip a model that
// did not resolve, so one bad type name is one problem, not one per
// attribute.
func (v *verifier) fieldModels(kind, key, readExpr, writeExpr string) (types.Type, types.Type) {
	var read, write types.Type
	if readExpr != "" {
		named, err := v.l.typeFromExpr(v.info, readExpr)
		if err != nil {
			v.problem(kind, key, "read_model", "%s: %s", readExpr, unwrapDetail(err))
		} else {
			read = named
			v.r.Checked++
		}
	}
	if writeExpr != "" {
		named, err := v.l.typeFromExpr(v.info, writeExpr)
		if err != nil {
			v.problem(kind, key, "write_model", "%s: %s", writeExpr, unwrapDetail(err))
		} else {
			write = named
			v.r.Checked++
		}
	}
	return read, write
}

// call walks a Call's segments from the client type, holding every hop to
// the SDK's real method set and the final hop to the declared results.
func (v *verifier) call(kind, key, name string, c *Call) {
	path := name + ".expr"
	sig, hop, err := resolveChain(v.client, c.Segments)
	if err != nil {
		v.problem(kind, key, path, "%s: hop %d: %s", c.Expr, hop, unwrapDetail(err))
		return
	}
	v.r.Checked++

	if len(c.Results) > 0 && sig.Results().Len() != len(c.Results) {
		v.problem(kind, key, name+".results", "declared %d result(s) but the call returns %d: %s",
			len(c.Results), sig.Results().Len(), shortSignature(sig))
		return
	}
	if c.ResponseType != "" && sig.Results().Len() > 0 {
		got := shortType(sig.Results().At(0).Type())
		if got != c.ResponseType {
			v.problem(kind, key, name+".response_type",
				"declared %s but the call returns %s", c.ResponseType, got)
			return
		}
		v.r.Checked++
	}
}

// collection holds the recorded element access against the list call's
// real result: the slice the generated code ranges over must actually be
// reachable the way the binding spells it.
func (v *verifier) collection(kind, key string, list *Call, elementType, access string) {
	if elementType == "" {
		v.problem(kind, key, "element_type", "empty: the list's element type is unresolved")
		return
	}
	sig, _, err := resolveChain(v.client, list.Segments)
	if err != nil || sig.Results().Len() == 0 {
		return // the call check already reported it
	}
	result := sig.Results().At(0).Type()

	element, err := elementOf(result, access)
	if err != nil {
		v.problem(kind, key, "collection_access", "%s", unwrapDetail(err))
		return
	}
	if got := shortType(element); got != elementType {
		v.problem(kind, key, "element_type", "declared %s but the list carries %s", elementType, got)
		return
	}
	v.r.Checked++
}

// elementOf resolves the element type a collection access reaches on a
// list result.
func elementOf(result types.Type, access string) (types.Type, error) {
	current := result
	if access != "" {
		if m, ok := strings.CutSuffix(access, "()"); ok {
			sig, found := methodOn(current, m)
			if !found || sig.Results().Len() != 1 {
				return nil, fmt.Errorf("%w: %s has no single-result method %s%s",
					ErrBindings, shortType(current), m, didYouMean(m, methodNamesOf(current)))
			}
			current = sig.Results().At(0).Type()
		} else {
			st, err := structUnder(current)
			if err != nil {
				return nil, err
			}
			f, ok := fieldByName(st, access)
			if !ok {
				return nil, fmt.Errorf("%w: %s has no field %s%s",
					ErrBindings, shortType(current), access, didYouMean(access, fieldNames(st)))
			}
			current = f.Type()
		}
	}
	slice, ok := current.Underlying().(*types.Slice)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a slice, so nothing can range over it", ErrBindings, shortType(current))
	}
	return slice.Elem(), nil
}

// fields holds every field binding to the models it reads from and
// writes to, recursing through nested objects.
func (v *verifier) fields(kind, key, path string, fbs []FieldBinding, read, write types.Type) {
	for _, fb := range fbs {
		at := fmt.Sprintf("%s[%s]", path, fb.Attr)

		if fb.Access.Get != "" && read != nil {
			if result, ok := v.accessor(kind, key, at+".get", read, fb.Access.Get, 0); ok {
				v.checkFieldType(kind, key, at, fb, result)
			}
		}
		if fb.Access.Set != "" && write != nil {
			v.accessor(kind, key, at+".set", write, fb.Access.Set, 1)
		}
		if fb.Access.ParseFunc != "" {
			v.checkParseFunc(kind, key, at, fb.Access.ParseFunc)
		}

		if len(fb.Nested) > 0 {
			if fb.NestedModel == "" {
				v.problem(kind, key, at+".nested_model",
					"empty: the nested object's SDK model is unresolved")
				continue
			}
			nr := v.nestedType(kind, key, at+".nested_model", fb.NestedModel)
			var nw types.Type
			if fb.NestedWriteModel != "" {
				nw = v.nestedType(kind, key, at+".nested_write_model", fb.NestedWriteModel)
			}
			v.fields(kind, key, at, fb.Nested, nr, nw)
		}
	}
}

// accessor resolves one accessor method, holding it to the accessor shape
// (0 parameters and 1 result for a getter, exactly parameters for a setter). It
// returns the getter's result type or the setter's parameter type.
func (v *verifier) accessor(kind, key, path string, model types.Type, name string, parameters int) (types.Type, bool) {
	sig, ok := methodOn(model, name)
	if !ok {
		v.problem(kind, key, path, "%s has no method %s%s",
			shortType(model), name, didYouMean(name, methodNamesOf(model)))
		return nil, false
	}
	if sig.Params().Len() != parameters || (parameters == 0 && sig.Results().Len() != 1) {
		v.problem(kind, key, path, "%s.%s is not an accessor: %s",
			shortType(model), name, shortSignature(sig))
		return nil, false
	}
	v.r.Checked++
	if parameters == 0 {
		return sig.Results().At(0).Type(), true
	}
	return sig.Params().At(0).Type(), true
}

// checkFieldType holds a scalar field's recorded SDK type to the getter's
// real result. Nested objects skip it: their shape is the nested walk.
func (v *verifier) checkFieldType(kind, key, at string, fb FieldBinding, result types.Type) {
	if len(fb.Nested) > 0 || fb.Access.SDKType == "" {
		return
	}
	if got := shortType(result); got != fb.Access.SDKType {
		v.problem(kind, key, at+".sdk_type", "recorded %s but the SDK carries %s",
			fb.Access.SDKType, got)
		return
	}
	v.r.Checked++
}

func (v *verifier) checkParseFunc(kind, key, at, parseFunc string) {
	name := parseFunc
	importPath := v.info.ImportPath
	if i := strings.LastIndex(name, "."); i >= 0 {
		if name[:i] == "models" {
			importPath = v.info.ModelsImportPath
		}
		name = name[i+1:]
	}
	if !v.l.functionExists(importPath, name) {
		v.problem(kind, key, at+".parse_func", "the SDK declares no function %s", parseFunc)
		return
	}
	v.r.Checked++
}

func (v *verifier) nestedType(kind, key, path, expr string) types.Type {
	named, err := v.l.typeFromExpr(v.info, expr)
	if err != nil {
		v.problem(kind, key, path, "%s: %s", expr, unwrapDetail(err))
		return nil
	}
	v.r.Checked++
	return named
}

// resolveChain walks a call's segments from a starting type: a field
// segment selects a struct field, a call segment resolves a method with
// the declared argument count, and every hop before the last must yield
// exactly one value for the next hop to resolve against. It returns the
// final method's signature and, on failure, the index of the hop at
// fault.
func resolveChain(start types.Type, segs []Segment) (*types.Signature, int, error) {
	current := start
	for i, seg := range segs {
		last := i == len(segs)-1
		if !seg.Call {
			st, err := structUnder(current)
			if err != nil {
				return nil, i, fmt.Errorf("%w: %s is not a struct, so it has no field %s",
					ErrBindings, shortType(current), seg.Name)
			}
			f, ok := fieldByName(st, seg.Name)
			if !ok {
				return nil, i, fmt.Errorf("%w: %s has no field %s%s",
					ErrBindings, shortType(current), seg.Name, didYouMean(seg.Name, fieldNames(st)))
			}
			if last {
				return nil, i, fmt.Errorf("%w: the call ends on field %s rather than a method", ErrBindings, seg.Name)
			}
			current = f.Type()
			continue
		}

		sig, ok := methodOn(current, seg.Name)
		if !ok {
			return nil, i, fmt.Errorf("%w: %s has no method %s%s",
				ErrBindings, shortType(current), seg.Name, didYouMean(seg.Name, methodNamesOf(current)))
		}
		if got, want := len(seg.Args), sig.Params().Len(); got != want {
			return nil, i, fmt.Errorf("%w: %s takes %d argument(s) but the call passes %d: %s",
				ErrBindings, seg.Name, want, got, shortSignature(sig))
		}
		if last {
			return sig, i, nil
		}
		if sig.Results().Len() != 1 {
			return nil, i, fmt.Errorf("%w: %s returns %d values; a builder hop must return exactly one",
				ErrBindings, seg.Name, sig.Results().Len())
		}
		current = sig.Results().At(0).Type()
	}
	return nil, 0, fmt.Errorf("%w: the call has no segments", ErrBindings)
}

// shortSignature renders a signature for messages that show what the SDK
// actually offers rather than only that the guess was wrong.
func shortSignature(sig *types.Signature) string {
	parameters := make([]string, 0, sig.Params().Len())
	for i := range sig.Params().Len() {
		parameters = append(parameters, shortType(sig.Params().At(i).Type()))
	}
	results := make([]string, 0, sig.Results().Len())
	for i := range sig.Results().Len() {
		results = append(results, shortType(sig.Results().At(i).Type()))
	}
	return fmt.Sprintf("(%s) (%s)", strings.Join(parameters, ", "), strings.Join(results, ", "))
}

// unwrapDetail strips the sentinel prefix so a Problem's detail is not
// prefixed with the same sentence on every line.
func unwrapDetail(err error) string {
	return strings.TrimPrefix(err.Error(), ErrBindings.Error()+": ")
}

// DropProblems removes every entity a verification problem names, and
// answers what was dropped, with the reason, sorted by key. An entity whose
// binding the SDK cannot answer must not be emitted — code written against
// it would name surface that does not exist — but the entities beside it are
// unaffected, and refusing all of them for one is a cost with no benefit.
//
// One entity may collect several problems; the first reason stands for the
// entity, and the rest are folded in behind it so nothing is lost.
func (b *Bindings) DropProblems(problems []Problem) []Dropped {
	if b == nil || len(problems) == 0 {
		return nil
	}

	reasons := map[string][]string{}
	kinds := map[string]string{}
	for _, p := range problems {
		if _, seen := reasons[p.Key]; !seen {
			kinds[p.Key] = p.Kind
		}
		reasons[p.Key] = append(reasons[p.Key], p.Path+": "+p.Detail)
	}

	keys := make([]string, 0, len(reasons))
	for key := range reasons {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]Dropped, 0, len(keys))
	for _, key := range keys {
		delete(b.Resources, key)
		delete(b.Datasources, key)
		delete(b.ListResources, key)
		delete(b.Actions, key)
		out = append(out, Dropped{
			Key:    key,
			Kind:   kinds[key],
			Reason: "the generated SDK does not carry this " + kinds[key] + "'s binding — " + strings.Join(reasons[key], "; "),
		})
	}
	return out
}

// Dropped is one entity verification removed from the bindings.
type Dropped struct {
	Key    string
	Kind   string
	Reason string
}
