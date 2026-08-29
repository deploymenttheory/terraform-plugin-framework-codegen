package sdkbind

import (
	"fmt"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
	"go/types"
	"sort"
)

// Prune resolves every drafted binding against the generated SDK under
// sdkDir, and deletes what the SDK cannot carry.
//
// A draft is derived from the document alone, and a document routinely
// promises what the generated SDK does not deliver: a model deduplicated
// under another name, an integer carried narrower than declared, an
// enumeration minted from an inline value set the document never named.
// Where the SDK's truth admits exactly one answer, the draft is repaired
// in place — the response type is read off the real method signature, a
// mangled accessor gains its Escaped suffix, an enumeration gains its
// Parse companion. Everything else is deleted with the SDK's own reason:
// an attribute-level problem removes the attribute, and a problem in the
// call chain removes the entity, because nothing smaller leaves a
// buildable binding. Nothing is invented and nothing is widened — every
// surviving binding names symbols the SDK demonstrably has.
//
// The removals are returned sorted and recorded on the binding set, so a
// generation run reports what the provider will not carry.
func Prune(b *Bindings, sdkDir string) ([]Removal, error) {
	l, err := loadSDK(sdkDir)
	if err != nil {
		return nil, err
	}
	client, err := l.lookupType(b.SDK.ImportPath, b.SDK.ClientTypeName)
	if err != nil {
		return nil, fmt.Errorf("%w: sdk.client_type_name: %s", ErrBindings, unwrapDetail(err))
	}

	p := &pruner{l: l, info: b.SDK, client: client, bindings: b}

	for _, key := range sortedKeys(b.Resources) {
		if !p.resource(b.Resources[key]) {
			delete(b.Resources, key)
		}
	}
	for _, key := range sortedKeys(b.Datasources) {
		if !p.datasource(b.Datasources[key]) {
			delete(b.Datasources, key)
		}
	}
	for _, key := range sortedKeys(b.ListResources) {
		if !p.listResource(b.ListResources[key]) {
			delete(b.ListResources, key)
		}
	}
	for _, key := range sortedKeys(b.Actions) {
		if !p.action(b.Actions[key]) {
			delete(b.Actions, key)
		}
	}

	sort.Slice(p.removed, func(i, j int) bool {
		a, z := p.removed[i], p.removed[j]
		if a.Kind != z.Kind {
			return a.Kind < z.Kind
		}
		if a.Key != z.Key {
			return a.Key < z.Key
		}
		return a.Attribute < z.Attribute
	})
	sort.Slice(p.reconciled, func(i, j int) bool {
		a, z := p.reconciled[i], p.reconciled[j]
		switch {
		case a.Kind != z.Kind:
			return a.Kind < z.Kind
		case a.Key != z.Key:
			return a.Key < z.Key
		case a.Attribute != z.Attribute:
			return a.Attribute < z.Attribute
		default:
			return a.Drafted < z.Drafted
		}
	})
	b.Reconciled = p.reconciled
	b.Removed = p.removed
	return p.removed, nil
}

type pruner struct {
	l        *loader
	info     SDKInfo
	client   types.Type
	bindings *Bindings
	removed  []Removal
	// subject is the entity being resolved. A reconciliation deep in a
	// call chain has no argument naming the entity it belongs to, and the
	// pruner walks one entity at a time, so it is held here rather than
	// threaded through every resolver that cannot use it.
	subject    struct{ kind, key string }
	reconciled []Reconciliation
}

// resolving names the entity every reconciliation recorded until the next
// call belongs to.
func (p *pruner) resolving(kind, key string) {
	p.subject.kind, p.subject.key = kind, key
}

// reconcile records one draft the SDK settled. A no-op change is not a
// reconciliation: nothing disagreed.
func (p *pruner) reconcile(attribute, drafted, settled string) {
	if drafted == settled {
		return
	}
	p.reconciled = append(p.reconciled, Reconciliation{
		Kind:      p.subject.kind,
		Key:       p.subject.key,
		Attribute: attribute,
		Drafted:   drafted,
		Settled:   settled,
	})
}

// resolveType resolves a type expression and records the package it was
// found under, so the emitter can import whatever the expression names.
func (p *pruner) resolveType(expr string) (*types.Named, error) {
	named, importPath, err := p.l.typeAndPackageFromExpr(p.info, expr)
	if err != nil {
		return nil, err
	}
	if goPackage := named.Obj().Pkg(); goPackage != nil {
		p.bindings.recordPackage(goPackage.Name(), importPath)
	}
	return named, nil
}

func (p *pruner) remove(kind, key, attribute, reason string) {
	p.removed = append(p.removed, Removal{Kind: kind, Key: key, Attribute: attribute, Reason: reason})
}

// resource resolves one resource's calls and fields; false removes it.
func (p *pruner) resource(rb *ResourceBinding) bool {
	const kind = string(specmodel.KindResource)
	p.resolving(kind, rb.Key)
	calls := []struct {
		name string
		call *Call
	}{
		{"create", rb.Create}, {"read", rb.Read}, {"update", rb.Update}, {"delete", rb.Delete},
	}
	for _, c := range calls {
		if c.call == nil {
			continue
		}
		if why := p.resolveCall(c.call); why != "" {
			p.remove(kind, rb.Key, "", fmt.Sprintf("its %s call cannot be made: %s", c.name, why))
			return false
		}
	}

	// The read payload is what state maps from; create's is the fallback
	// for an API that only answers on create.
	rb.ReadModel = ""
	if rb.Read != nil {
		rb.ReadModel = rb.Read.ResponseType
	}
	if rb.ReadModel == "" && rb.Create != nil {
		rb.ReadModel = rb.Create.ResponseType
	}
	if rb.ReadModel == "" {
		p.remove(kind, rb.Key, "", "no lifecycle call yields a payload to map state from")
		return false
	}

	// The body the practitioner's configuration is written into: the
	// create's, or the update's for a singleton, which has no create.
	writeSource := rb.Create
	if writeSource == nil {
		writeSource = rb.Update
	}
	rb.WriteModel, rb.WriteConstructor = "", ""
	if writeSource != nil && writeSource.RequestType != "" {
		write, constructor, why := p.writeModelFor(writeSource.RequestType)
		if why != "" {
			p.remove(kind, rb.Key, "", fmt.Sprintf("its request body cannot be constructed: %s", why))
			return false
		}
		rb.WriteModel, rb.WriteConstructor = write, constructor
	}

	read, err := p.resolveType(rb.ReadModel)
	if err != nil {
		p.remove(kind, rb.Key, "", unwrapDetail(err))
		return false
	}
	var write types.Type
	if rb.WriteModel != "" {
		w, err := p.resolveType(rb.WriteModel)
		if err != nil {
			p.remove(kind, rb.Key, "", unwrapDetail(err))
			return false
		}
		write = w
	}

	rb.Fields = p.fields(kind, rb.Key, "", rb.Fields, read, write)
	liftKeptFromPlan(rb.Fields)

	if why := unbuildableReason(rb.Fields, writeSource != nil); why != "" {
		p.remove(kind, rb.Key, "", why)
		return false
	}

	p.settleCreateID(rb, read)
	p.settleUpdateBody(rb, read)
	return true
}

// settleCreateID finds the accessor a create response answers the id through
// when that response is not the read model.
//
// A create that answers its own type still names the object it made; taking
// the id from it is what lets the settling read address the object at all.
func (p *pruner) settleCreateID(rb *ResourceBinding, read types.Type) {
	if rb.Create == nil || rb.Create.ResponseType == "" || rb.Create.ResponseType == rb.ReadModel || read == nil {
		return
	}
	var idAccess string
	for i := range rb.Fields {
		if rb.Fields[i].Attr == "id" {
			idAccess = rb.Fields[i].Access.Get
			break
		}
	}
	if idAccess == "" {
		return
	}
	created, err := p.resolveType(rb.Create.ResponseType)
	if err != nil {
		return
	}
	fromCreate, ok := methodOn(created, idAccess)
	if !ok {
		return
	}
	fromRead, ok := methodOn(read, idAccess)
	if !ok {
		return
	}
	// The same accessor on both, answering the same type: the id the state
	// mapper already converts from the read is the id this takes from the
	// create, so the conversion settled for one is right for the other.
	if fromCreate.Results().Len() != 1 || fromRead.Results().Len() != 1 ||
		!types.Identical(fromCreate.Results().At(0).Type(), fromRead.Results().At(0).Type()) {
		return
	}
	rb.CreateIDAccess = idAccess
}

// settleUpdateBody gives the update its own request body where the create's
// cannot serve it.
//
// The two are the same request often enough that one body was assumed, and
// where an API declares them separately the generated SDK types them
// separately: passing the create body to the update does not compile. The
// update's fields are the same attributes resolved against the update's own
// model, so whatever it cannot carry is dropped from the update alone. The
// two bodies usually differ by only a handful of fields.
//
// A resource whose update body cannot be constructed keeps its create-only
// body rather than being removed: the update will not compile against it,
// but that is a smaller loss than the whole entity, and prune records why.
func (p *pruner) settleUpdateBody(rb *ResourceBinding, read types.Type) {
	if rb.Update == nil || rb.Update.RequestType == "" ||
		rb.Create == nil || rb.Update.RequestType == rb.Create.RequestType {
		return
	}

	model, constructor, why := p.writeModelFor(rb.Update.RequestType)
	if why != "" {
		p.remove("resource", rb.Key, "", fmt.Sprintf("its update body cannot be constructed: %s", why))
		return
	}
	write, err := p.resolveType(model)
	if err != nil {
		p.remove("resource", rb.Key, "", unwrapDetail(err))
		return
	}

	// The create's resolved fields are the starting point, deep-copied so
	// resolving against a second model cannot disturb the first.
	fields := p.fields("resource", rb.Key, "update", copyFieldBindings(rb.Fields), read, write)
	if len(fields) == 0 {
		return
	}
	rb.UpdateWriteModel, rb.UpdateWriteConstructor, rb.UpdateFields = model, constructor, fields
}

// copyFieldBindings deep-copies a field binding tree, so resolving it a
// second time against another model leaves the original untouched.
func copyFieldBindings(fbs []FieldBinding) []FieldBinding {
	if fbs == nil {
		return nil
	}
	out := make([]FieldBinding, len(fbs))
	copy(out, fbs)
	for i := range out {
		out[i].Nested = copyFieldBindings(fbs[i].Nested)
	}
	return out
}

// datasource resolves a lookup's read or a companion's list; false
// removes it.
func (p *pruner) datasource(db *DatasourceBinding) bool {
	const kind = string(specmodel.KindDatasource)
	p.resolving(kind, db.Key)
	if db.Read != nil {
		if why := p.resolveCall(db.Read); why != "" {
			p.remove(kind, db.Key, "", fmt.Sprintf("its read call cannot be made: %s", why))
			return false
		}
		db.ReadModel = db.Read.ResponseType
	}

	var element types.Type
	if db.List != nil {
		resolved, why := p.resolveListElement(db.List, &db.ElementType, &db.CollectionAccess, db.ListWrapperKey)
		if why != "" {
			p.remove(kind, db.Key, "", why)
			return false
		}
		element = resolved
	}

	model := element
	if model == nil {
		named, err := p.resolveType(db.ReadModel)
		if err != nil {
			p.remove(kind, db.Key, "", unwrapDetail(err))
			return false
		}
		model = named
	}

	db.Fields = p.fields(kind, db.Key, "", db.Fields, model, nil)
	if why := unbuildableReason(db.Fields, false); why != "" {
		p.remove(kind, db.Key, "", why)
		return false
	}
	return true
}

func (p *pruner) listResource(lb *ListResourceBinding) bool {
	const kind = string(specmodel.KindListResource)
	p.resolving(kind, lb.Key)
	if lb.List == nil {
		p.remove(kind, lb.Key, "", "it has no list call")
		return false
	}
	element, why := p.resolveListElement(lb.List, &lb.ElementType, &lb.CollectionAccess, lb.EnvelopeKey)
	if why != "" {
		p.remove(kind, lb.Key, "", why)
		return false
	}
	lb.Fields = p.fields(kind, lb.Key, "", lb.Fields, element, nil)
	if why := unbuildableReason(lb.Fields, false); why != "" {
		p.remove(kind, lb.Key, "", why)
		return false
	}
	return true
}

func (p *pruner) action(ab *ActionBinding) bool {
	const kind = string(specmodel.KindAction)
	p.resolving(kind, ab.Key)
	if ab.Invoke == nil {
		p.remove(kind, ab.Key, "", "it has no invoke call")
		return false
	}
	if why := p.resolveCall(ab.Invoke); why != "" {
		p.remove(kind, ab.Key, "", fmt.Sprintf("its invoke call cannot be made: %s", why))
		return false
	}

	ab.WriteModel, ab.WriteConstructor = "", ""
	var write types.Type
	if ab.Invoke.RequestType != "" {
		model, constructor, why := p.writeModelFor(ab.Invoke.RequestType)
		if why != "" {
			p.remove(kind, ab.Key, "", fmt.Sprintf("its request body cannot be constructed: %s", why))
			return false
		}
		ab.WriteModel, ab.WriteConstructor = model, constructor
		w, err := p.resolveType(model)
		if err != nil {
			p.remove(kind, ab.Key, "", unwrapDetail(err))
			return false
		}
		write = w

		ab.Fields = p.fields(kind, ab.Key, "", ab.Fields, nil, write)
		settable := false
		for _, fb := range ab.Fields {
			if fb.Access.Set != "" {
				settable = true
				break
			}
		}
		if !settable {
			p.remove(kind, ab.Key, "",
				"no argument survives that can be sent in its request body, so there is nothing to invoke it with")
			return false
		}
	}
	return true
}

// unbuildableReason names the missing direction after pruning, or is
// empty when the binding still has something to generate: an entity with
// nothing readable maps nothing into state, and a resource that sends a
// body with nothing settable gives an operator nothing to configure.
func unbuildableReason(fbs []FieldBinding, needsWrite bool) string {
	var readable, settable bool
	for _, fb := range fbs {
		if fb.Access.Get != "" {
			readable = true
		}
		if fb.Access.Set != "" {
			settable = true
		}
	}
	switch {
	case !readable:
		return "no attribute survives that can be read back from a response, so there is nothing to map into state"
	case needsWrite && !settable:
		return "no attribute survives that can be sent in a request body, so there is nothing an operator could configure"
	}
	return ""
}
