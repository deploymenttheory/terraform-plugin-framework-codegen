package plan

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

// The caps. Each bounds one derivation rule, so a wide schema widens the
// plan sub-linearly and the per-entity request budget stays honest.
const (
	// entityRequestBudget is the default request spend one entity's steps
	// may make, bisection and read-back polling included.
	entityRequestBudget = 60
	// maxUpdateFields caps the per-field update checks.
	maxUpdateFields = 12
	// maxOmitRequired caps the omit-one-required negative checks.
	maxOmitRequired = 6
	// maxConditionalValues caps the enum values conditional creates pin.
	maxConditionalValues = 6
	// defaultPollTimeout and pollInterval bound a readBack with no
	// x-tfpfgen-eventual-consistency hint.
	defaultPollTimeout = 30 * time.Second
	pollInterval       = 2 * time.Second
)

// Derive computes the audit plan for a document: which entities get
// audited, in what order, with which steps, under which budgets. It is
// pure — the same document, config and inputs always derive the same plan.
//
// Entities are audited when they classify as resources (full lifecycle)
// or as lookup-by-key datasources (read checks). Parents order before the
// children whose paths embed them; classification's collection-path
// ordering already guarantees that, because a parent's collection path is
// a strict prefix of its child's.
func Derive(doc *specmodel.Document, cfg *config.Config, inputs *Inputs) (*Plan, error) {
	cls := specmodel.Classify(doc)

	byKey := make(map[string]specmodel.Classification, len(cls.Entities))
	byCollection := make(map[string]string, len(cls.Entities))
	known := make([]string, 0, len(cls.Entities))
	for _, c := range cls.Entities {
		byKey[c.Key] = c
		byCollection[c.CollectionPath] = c.Key
		known = append(known, c.Key)
	}

	if err := checkInputEntities(inputs, byKey, known); err != nil {
		return nil, err
	}

	excluded := map[string]bool{}
	for _, e := range cfg.Services.Exclude {
		excluded[e] = true
	}

	d := &deriver{
		doc:          doc,
		cfg:          cfg,
		inputs:       inputs,
		byKey:        byKey,
		byCollection: byCollection,
		planned:      map[string]bool{},
	}

	p := &Plan{}
	add := func(ep EntityPlan, skip *Skipped) {
		if skip != nil {
			p.Skipped = append(p.Skipped, *skip)
			return
		}
		p.Entities = append(p.Entities, ep)
	}

	// Resources first: they create the objects everything else, children
	// included, depends on.
	for _, c := range cls.Entities {
		if !hasKind(c, specmodel.KindResource) {
			continue
		}
		if skip := d.adminSkip(c.Key, excluded); skip != nil {
			add(EntityPlan{}, skip)
			continue
		}
		ep, skip := d.resourcePlan(c)
		if skip == nil {
			d.planned[c.Key] = true
		}
		add(ep, skip)
	}

	// Then the datasources that exist only because the item read does:
	// nothing to create, so their checks are reads by operator-supplied
	// key.
	for _, c := range cls.Entities {
		if hasKind(c, specmodel.KindResource) || !c.LookupByKey {
			continue
		}
		if skip := d.adminSkip(c.Key, excluded); skip != nil {
			add(EntityPlan{}, skip)
			continue
		}
		add(d.lookupPlan(c))
	}

	p.Budget = runBudget(cfg, len(p.Entities))
	return p, nil
}

// checkInputEntities refuses an inputs file naming an entity the document
// does not classify — the same strictness config applies to its own keys,
// because a typo here silently un-supplies a value.
func checkInputEntities(inputs *Inputs, byKey map[string]specmodel.Classification, known []string) error {
	if inputs == nil {
		return nil
	}
	keys := make([]string, 0, len(inputs.Entities))
	for k := range inputs.Entities {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := byKey[k]; !ok {
			return fmt.Errorf("%s: unknown entity %q%s", InputsPath, k, didYouMean(k, known))
		}
	}
	return nil
}

// deriver carries the per-run derivation state.
type deriver struct {
	doc          *specmodel.Document
	cfg          *config.Config
	inputs       *Inputs
	byKey        map[string]specmodel.Classification
	byCollection map[string]string
	// planned marks resources whose plans exist, and whose minimal
	// objects children may therefore reference via CreatedRef.
	planned map[string]bool
}

// adminSkip is the operator-decided exclusions, checked before any
// derivation: the config's services.exclude, then the inputs' skip.
func (d *deriver) adminSkip(key string, excluded map[string]bool) *Skipped {
	if excluded[key] {
		return &Skipped{Entity: key, Reason: "excluded by services.exclude in tfpfgen.yaml"}
	}
	if d.inputs.forEntity(key).Skip {
		return &Skipped{Entity: key, Reason: "skipped by " + InputsPath}
	}
	return nil
}

// pathParam matches one {param} segment.
var pathParam = regexp.MustCompile(`\{([^}]+)\}`)

// pathParams lists a path's parameters in path order.
func pathParams(path string) []string {
	var out []string
	for _, m := range pathParam.FindAllStringSubmatch(path, -1) {
		out = append(out, m[1])
	}
	return out
}

// pathValues resolves every parameter of path to a value or token:
// operator parentRefs first — the operator saying "use this existing
// object" outranks creating one — then the created object of an already
// planned parent entity, found by the parameter's enclosing collection
// prefix. selfKey, when non-empty, resolves the trailing item parameter
// to the entity's own created object. A parameter nothing can supply is
// the returned reason.
func (d *deriver) pathValues(path, selfKey string, ei EntityInputs) (map[string]string, []string, string) {
	params := pathParams(path)
	if len(params) == 0 {
		return nil, nil, ""
	}
	values := make(map[string]string, len(params))
	var parents []string
	for i, param := range params {
		if v, ok := ei.ParentRefs[param]; ok {
			values[param] = v
			continue
		}
		if selfKey != "" && i == len(params)-1 && strings.HasSuffix(path, "{"+param+"}") {
			values[param] = CreatedRef(selfKey)
			continue
		}
		if parentKey, ok := d.parentFor(path, param); ok && hasKind(d.byKey[parentKey], specmodel.KindResource) {
			if d.planned[parentKey] {
				values[param] = CreatedRef(parentKey)
				parents = append(parents, parentKey)
				continue
			}
			return nil, nil, fmt.Sprintf(
				"path parameter %q needs parent %q, which is not audited; supply parentRefs.%s in %s",
				param, parentKey, param, InputsPath)
		}
		return nil, nil, fmt.Sprintf(
			"no value for path parameter %q; supply parentRefs.%s in %s", param, param, InputsPath)
	}
	return values, parents, ""
}

// parentFor maps a path parameter to the entity whose collection encloses
// it: for "/projects/{projectId}/tags", parameter projectId's enclosing
// prefix is "/projects", and the entity classified there is the parent.
func (d *deriver) parentFor(path, param string) (string, bool) {
	idx := strings.Index(path, "{"+param+"}")
	if idx <= 1 {
		return "", false
	}
	prefix := strings.TrimRight(path[:idx], "/")
	key, ok := d.byCollection[prefix]
	return key, ok
}

// operation finds the loaded operation a classification role points at.
func (d *deriver) operation(ref *specmodel.Op) *specmodel.Operation {
	if ref == nil {
		return nil
	}
	for pi := range d.doc.Paths {
		p := &d.doc.Paths[pi]
		if p.Path != ref.Path {
			continue
		}
		for oi := range p.Operations {
			if p.Operations[oi].Method == ref.Method {
				return &p.Operations[oi]
			}
		}
	}
	return nil
}

// hasKind reports whether a classification yields the kind.
func hasKind(c specmodel.Classification, k specmodel.Kind) bool {
	for _, have := range c.Kinds {
		if have == k {
			return true
		}
	}
	return false
}

// runBudget derives the plan-wide budget: the summed entity budgets, the
// configured live-object ceiling, and a wall-clock allowance of twice the
// time the request budget takes at the configured rate — headroom for
// retries and read-back polling — with a one-minute floor.
func runBudget(cfg *config.Config, entities int) RunBudget {
	requests := entityRequestBudget * entities

	objects := cfg.Audit.MaxObjects
	if objects < 1 {
		objects = 25
	}
	rps := cfg.Audit.RateLimitRPS
	if rps < 1 {
		rps = 2
	}

	seconds := 2 * ((requests + rps - 1) / rps)
	if seconds < 60 {
		seconds = 60
	}
	return RunBudget{
		Requests: requests,
		Objects:  objects,
		Duration: (time.Duration(seconds) * time.Second).String(),
	}
}
