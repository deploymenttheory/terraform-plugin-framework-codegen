// explain.go holds the prose half of revision. compile.go decides what a
// correction does to the document; this file says, for every observation
// kind, what the live API demonstrably did and what accepting or refusing
// the correction means for the people who will use the generated provider.
//
// The audience is a reviewer who has never heard of tfpfgen. Nothing here
// may explain a finding by naming the extension key it compiles into — the
// key may be mentioned in passing, it may not carry the meaning — and
// nothing here may name a vendor, an endpoint or a product.
package revise

import (
	"fmt"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
)

// Explanation is one observation kind's account of itself: a title to head a
// pull request with, and the five sentences that let someone decide.
//
// Expected, Observed, Means, Merging and Closing may carry the placeholders
// {entity}, {attribute} and {value}; Render fills them. {entity} and
// {attribute} arrive as bare identifiers and are wrapped in a code span by
// the sentence itself; {value} arrives already spelled for display, because
// what a value looks like depends on the kind — a scalar, a list, a record.
type Explanation struct {
	// Title names the finding in the singular, as a noun phrase that reads
	// after a count: "1 server-assigned default".
	Title string `json:"title"`
	// Plural is the same noun phrase counted: "3 server-assigned defaults".
	Plural string `json:"plural"`
	// Expected is what the imported document led the audit to expect.
	Expected string `json:"expected"`
	// Observed is what the live API demonstrably did instead.
	Observed string `json:"observed"`
	// Means is what that costs a Terraform practitioner.
	Means string `json:"means"`
	// Merging says what accepting the correction changes in the generated
	// provider. It speaks for a whole group of findings at once, so — unlike
	// the three above — it may use {entity} but never {attribute} or {value}:
	// naming one member's attribute would be a lie about all the others.
	Merging string `json:"merging"`
	// Closing says what refusing it leaves behind, under the same rule.
	Closing string `json:"closing"`
}

// explanations is the table, one entry per compilable observation kind.
// TestUnit_Revise_ExplanationsCoverEveryCompilableKind holds it and
// compilableKinds to each other in both directions, so a kind cannot ship
// without prose and prose cannot outlive its kind.
var explanations = map[observe.Kind]Explanation{
	observe.KindWritable: {
		Title:  "attribute the API does not store",
		Plural: "attributes the API does not store",
		Expected: "The document lists `{attribute}` as an ordinary writable property, so a value sent " +
			"when the object is created should be there on the next read.",
		Observed: "The API accepted `{attribute}` on create and then read the object back without it — " +
			"the value was taken and discarded.",
		Means: "Terraform plans a value the API never keeps, so the apply ends with state that does not " +
			"match what was planned.",
		Merging: "Merging records the property as read-only, so the generated resource makes the attribute " +
			"Computed instead of Optional and stops asking practitioners for a value that goes nowhere.",
		Closing: "Closing leaves the attribute Optional, and every configuration that sets it ends in a " +
			"provider-produced-inconsistent-result error.",
	},
	observe.KindImmutable: {
		Title:  "attribute fixed at create",
		Plural: "attributes fixed at create",
		Expected: "Nothing in the document says `{attribute}` cannot change, so an update setting a new " +
			"value should be accepted.",
		Observed: "An update naming only `{attribute}` was refused, though the very same value was accepted " +
			"when the object was created.",
		Means: "Terraform would plan an in-place update the API always rejects; the only real way to change " +
			"this value is to replace the object.",
		Merging: "Merging records the attribute as create-only (`x-tfpfgen-immutable`), so the generated " +
			"resource marks it RequiresReplace and Terraform plans a replacement instead of a doomed update.",
		Closing: "Closing leaves the provider planning updates the API will refuse, with no warning until " +
			"apply.",
	},
	observe.KindRequiredByAPI: {
		Title:    "attribute the API requires",
		Plural:   "attributes the API requires",
		Expected: "The document leaves `{attribute}` optional, so a create that omits it should be accepted.",
		Observed: "A create that omitted `{attribute}` was rejected, with an error naming that very " +
			"attribute.",
		Means: "The generated resource would accept a configuration the API always refuses, so the failure " +
			"arrives at apply time rather than at plan time.",
		Merging: "Merging adds the property to the schema's required list, so the generated resource marks " +
			"it Required and a missing value is caught during validation.",
		Closing: "Closing leaves the attribute Optional, and everyone who omits it gets a raw API error " +
			"instead of a clear validation message.",
	},
	observe.KindRequiredWhen: {
		Title:  "attribute required only in one case",
		Plural: "attributes required only in one case",
		Expected: "The document requires `{attribute}` always or never, and says nothing about the sibling " +
			"field that actually decides it.",
		Observed: "A create omitting `{attribute}` was accepted while the sibling held one value and " +
			"rejected while it held another.",
		Means: "Terraform cannot tell a practitioner which combinations are legal, so a configuration only " +
			"fails once the API has seen it.",
		Merging: "Merging records the conditional requirement, so the generated resource emits a config " +
			"validator that refuses the illegal combination at plan time.",
		Closing: "Closing leaves the rule undocumented and unenforced, to be discovered as an apply-time " +
			"error.",
	},
	observe.KindServerDefault: {
		Title:  "server-assigned default",
		Plural: "server-assigned defaults",
		Expected: "The document declares no default for `{attribute}`, so omitting it should leave the " +
			"object without one.",
		Observed: "The API assigned `{attribute}` the value {value} by itself, on a create that never " +
			"mentioned the attribute.",
		Means: "Unrecorded, Terraform sees a value it did not plan for and reports drift on every refresh.",
		Merging: "Merging records each observed value as that property's declared default, so the " +
			"generated resource marks the attribute Optional and Computed and accepts the server's choice.",
		Closing: "Closing leaves the spec claiming there is no default, and practitioners get a perpetual " +
			"diff on this attribute.",
	},
	observe.KindDerivedDefault: {
		Title:  "computed default with no constant",
		Plural: "computed defaults with no constant",
		Expected: "The document declares no default for `{attribute}`, and one lucky create made it look " +
			"as though the API filled in a fixed value.",
		Observed: "Two creates that both omitted `{attribute}` came back holding different values, so " +
			"whatever the API fills in is computed rather than constant.",
		Means: "There is no constant to write down; the attribute is simply something the server decides " +
			"per object.",
		Merging: "Merging is not offered — this finding exists to stop a false fixed default being " +
			"recorded, not to record one.",
		Closing: "Closing discards the evidence that the fixed-default reading was wrong.",
	},
	observe.KindNormalisation: {
		Title:  "value the server rewrites",
		Plural: "values the server rewrites",
		Expected: "The document gives `{attribute}` no formatting rule, so the value sent should be the " +
			"value stored.",
		Observed: "The API stored {value} instead — a recognisable transform of what was sent, " +
			"case-folded, trimmed or reformatted.",
		Means: "Terraform compares the configured spelling against the stored one and calls the difference " +
			"drift, though nothing actually changed.",
		Merging: "Merging is not offered yet: there is no spec form for this, and coining one is the " +
			"repository owner's decision.",
		Closing: "Closing discards the record of why this attribute will look like it drifts.",
	},
	observe.KindIgnoredOnUpdate: {
		Title:  "attribute updates silently ignore",
		Plural: "attributes updates silently ignore",
		Expected: "The document offers `{attribute}` on update like any other property, so a new value " +
			"should take effect.",
		Observed: "An update carrying a new `{attribute}` returned a success status, and the read that " +
			"followed still showed the old value. Nothing was refused; nothing changed.",
		Means: "Terraform records the change as applied, so the next refresh contradicts state and blames " +
			"the practitioner's configuration for drifting.",
		Merging: "Merging records that updates ignore this attribute, so the generated provider stops " +
			"pretending the write took effect.",
		Closing: "Closing leaves an update path that reports success and does nothing — the hardest kind " +
			"of bug for a practitioner to see.",
	},
	observe.KindServerForced: {
		Title:    "attribute the server overrides",
		Plural:   "attributes the server overrides",
		Expected: "The document presents `{attribute}` as a value the caller chooses.",
		Observed: "The API stored a value of its own that neither matched what was sent nor derived from " +
			"it — the same value a create that omits the attribute produces.",
		Means: "Whatever a practitioner writes, the API substitutes its own answer, and Terraform reports " +
			"the difference as drift for as long as the resource exists.",
		Merging: "Merging records that the server forces this value, so the generated resource treats the " +
			"API's answer as authoritative instead of fighting it.",
		Closing: "Closing leaves practitioners an attribute that never holds what they set.",
	},
	observe.KindVolatile: {
		Title:    "attribute that changes on its own",
		Plural:   "attributes that change on their own",
		Expected: "Two identical reads of an untouched object should return the same `{attribute}`.",
		Observed: "They did not: `{attribute}` differed between two consecutive reads with no write of any " +
			"kind in between.",
		Means: "A value that moves by itself is compared against state on every refresh and reported as " +
			"drift no practitioner can fix.",
		Merging: "Merging records the attribute as volatile, so the generated provider leaves it out of " +
			"drift comparison.",
		Closing: "Closing leaves a permanently-diffing attribute in every plan.",
	},
	observe.KindValues: {
		Title:  "value set the document gets wrong",
		Plural: "value sets the document gets wrong",
		Expected: "The document publishes a fixed list of values for `{attribute}` and implies the API " +
			"takes exactly those.",
		Observed: "The API disagreed: {value}.",
		Means: "A documented value the API refuses becomes an apply-time error, and a value the API takes " +
			"but the generated validator rejects is simply unreachable.",
		Merging: "Merging corrects the list in the spec — dropping what the API refuses, adding what it " +
			"accepts — so the generated attribute validator matches the API.",
		Closing: "Closing keeps a validator that blocks working configurations, lets through ones the API " +
			"will reject, or both.",
	},
	observe.KindUpdateStyle: {
		Title:  "how updates treat omitted fields",
		Plural: "findings about how updates treat omitted fields",
		Expected: "The document does not say whether an update merges into the stored object or replaces " +
			"it wholesale.",
		Observed: "Updating one field of `{entity}` and re-reading the rest showed the API treats omitted " +
			"fields as {value}.",
		Means: "Guess this wrong and the generated update either wipes fields the practitioner never " +
			"mentioned or fails to change the one they did.",
		Merging: "Merging records the update style, so the generated update sends the request shape this " +
			"API actually wants.",
		Closing: "Closing leaves the generator guessing, and the wrong guess is what silently deletes " +
			"configuration.",
	},
	observe.KindDeleteNotFoundOK: {
		Title:    "delete that answers 404 when the object is already gone",
		Plural:   "findings about deleting an object that is already gone",
		Expected: "The document does not say what deleting an already-deleted object returns.",
		Observed: "Deleting the same object of `{entity}` a second time answered 404 rather than " +
			"succeeding.",
		Means: "Terraform destroys a resource something else already removed and reports a failure for " +
			"work that is, in fact, done.",
		Merging: "Merging records that a 404 from delete means the object is gone, so destroy becomes " +
			"idempotent.",
		Closing: "Closing leaves `terraform destroy` failing on any object removed out of band, and a " +
			"state entry to remove by hand.",
	},
	observe.KindReadAfterWrite: {
		Title:    "read that lags a write",
		Plural:   "reads that lag a write",
		Expected: "A read taken straight after a successful write should show what was written.",
		Observed: "It did not: polling measured up to {value} before the write became visible on `{entity}`.",
		Means: "The generated create reads back too early, sees stale data or nothing at all, and either " +
			"fails the apply or writes state that does not match.",
		Merging: "Merging records the measured lag, so the generated create and update keep re-reading for " +
			"that long before giving up.",
		Closing: "Closing leaves an intermittent apply failure that reproduces only under load.",
	},
	observe.KindUndocumentedFieldInSpec: {
		Title:    "field the document leaves out",
		Plural:   "fields the document leaves out",
		Expected: "No schema of `{entity}` declares `{attribute}`, so the API should not be returning it.",
		Observed: "Every read of the object carried `{attribute}`, consistently holding a {value}.",
		Means: "A field the spec does not know about cannot reach the Terraform schema at all, so " +
			"practitioners cannot read it.",
		Merging: "Merging adds the property, with the type it was observed holding, to the entity's schema, " +
			"so the generated resource exposes it.",
		Closing: "Closing keeps the field invisible to Terraform.",
	},
	observe.KindValidConfiguration: {
		Title:  "entity with several valid shapes",
		Plural: "entities with several valid shapes",
		Expected: "The document presents `{entity}` as one flat set of properties, with no hint that some " +
			"combinations are incompatible.",
		Observed: "Creates succeeded under several values of `{attribute}` — {value} — each admitting a " +
			"different set of fields.",
		Means: "One schema standing for several shapes means Terraform accepts configurations the API will " +
			"refuse.",
		Merging: "Merging records the deciding field and each variant's field set, so the generated " +
			"resource validates the whole combination at plan time.",
		Closing: "Closing leaves practitioners to learn the variants from API errors.",
	},
	observe.KindValidWhen: {
		Title:    "field valid only under one setting",
		Plural:   "fields valid only under one setting",
		Expected: "The document offers `{attribute}` unconditionally, alongside every other property.",
		Observed: "It was accepted only while its gate field held one specific value, and refused under " +
			"another — both directions observed, not inferred from a single error.",
		Means: "Terraform would let a practitioner set a field the API rejects for the configuration they " +
			"actually wrote.",
		Merging: "Merging records the condition, so the generated resource refuses the combination during " +
			"validation, before anything is sent.",
		Closing: "Closing leaves a plan that looks fine and an apply that is not.",
	},
	observe.KindDependsOn: {
		Title:    "field that needs another field",
		Plural:   "fields that need another field",
		Expected: "The document lists `{attribute}` and {value} as independent optional properties.",
		Observed: "A create carrying `{attribute}` without {value} was refused; adding {value} made the " +
			"same request succeed.",
		Means: "Terraform accepts half the pair and the API rejects the whole request.",
		Merging: "Merging records the co-requirement, so the generated resource asks for the pair together " +
			"at plan time.",
		Closing: "Closing leaves the dependency undiscoverable except by trial and error.",
	},
	observe.KindMutuallyExclusive: {
		Title:    "fields that cannot be combined",
		Plural:   "sets of fields that cannot be combined",
		Expected: "The document offers {value} side by side, with nothing to say they conflict.",
		Observed: "Each was accepted on its own, and a create carrying two of them together was refused, " +
			"reproducibly.",
		Means: "Terraform lets a practitioner set both, and the API rejects the request.",
		Merging: "Merging records the exclusivity, so the generated resource catches the conflict during " +
			"validation.",
		Closing: "Closing leaves the conflict to surface as an apply-time error.",
	},
	observe.KindListWrapper: {
		Title:    "wrapping of the list response",
		Plural:   "list-response wrappings",
		Expected: "The document's list response schema for `{entity}` describes one structure.",
		Observed: "The live collection read returned another: {value}.",
		Means:    "A generated list that unwraps the wrong key returns nothing at all.",
		Merging: "Merging records what the wire actually carried, which the generator reads in preference " +
			"to the document's own list response schema.",
		Closing: "Closing leaves the generated list data source reading a wrapping the API does not send.",
	},
	observe.KindListPagination: {
		Title:    "pagination of the list response",
		Plural:   "list-response pagination styles",
		Expected: "The document says nothing about how `{entity}`'s collection pages.",
		Observed: "The live collection read advertised {value}.",
		Means:    "A generated list that pages the wrong way returns the same page forever.",
		Merging:  "Merging records the style the wire advertised.",
		Closing:  "Closing leaves the generated list data source paging the wrong way, or not at all.",
	},
}

// Explain answers one kind's explanation. The second result is false for a
// kind the table does not cover, which the bidirectional test makes
// unreachable for any compilable kind.
func Explain(kind observe.Kind) (Explanation, bool) {
	e, ok := explanations[kind]
	return e, ok
}

// Render fills the placeholders. entity and attribute are bare identifiers;
// value is already spelled for display — see describeValue.
func (e Explanation) Render(entity, attribute, value string) Explanation {
	r := strings.NewReplacer("{entity}", entity, "{attribute}", attribute, "{value}", value)
	e.Expected = r.Replace(e.Expected)
	e.Observed = r.Replace(e.Observed)
	e.Means = r.Replace(e.Means)
	e.Merging = r.Replace(e.Merging)
	e.Closing = r.Replace(e.Closing)
	return e
}

// Summary counts findings in the kind's own words: "1 server-assigned
// default", "3 server-assigned defaults". It heads the grouped pull request.
func (e Explanation) Summary(n int) string {
	if n == 1 {
		return "1 " + e.Title
	}
	return fmt.Sprintf("%d %s", n, e.Plural)
}
