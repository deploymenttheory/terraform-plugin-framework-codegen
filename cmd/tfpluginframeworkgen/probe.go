package main

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
)

const usageProbe = "probe [-mode record|replay|verify|sweep] -blueprint DIR " +
	"[-resource KEY] [-only PROBE] [-list] [--allow-mutations]"

// Probe modes. The default is replay, deliberately: the safe mode is what you get by
// typing less, and the mode that can change somebody's tenant has to be spelled out.
const (
	modeReplay = "replay"
	modeRecord = "record"
	modeVerify = "verify"
	modeSweep  = "sweep"
)

func runProbe(args []string) error {
	fs, _ := newFlagSet("probe", usageProbe)

	var (
		mode          = fs.String("mode", modeReplay, "record, replay, verify or sweep")
		blueprintPath = fs.String("blueprint", "", "blueprint file or directory (required)")
		resource      = fs.String("resource", "", "probe one resource, by blueprint key")
		only          = fs.String("only", "", "run one probe, by name")
		list          = fs.Bool("list", false, "print the probe catalogue and exit")

		allowMutations = fs.Bool("allow-mutations", false,
			"permit probes that create, update and delete; requires -mode record and a sandbox profile")
	)

	if err := parse(fs, args); err != nil {
		return err
	}

	if *blueprintPath == "" {
		return usagef("-blueprint is required")
	}

	switch *mode {
	case modeRecord, modeReplay, modeVerify, modeSweep:
	default:
		return usagef("-mode %q is not one of record, replay, verify or sweep", *mode)
	}

	bp, err := blueprint.LoadDir(*blueprintPath)
	if err != nil {
		return err
	}

	if *resource != "" {
		if err := keepOnlyResource(&bp, *resource); err != nil {
			return err
		}
	}

	subjects, notes, err := subjectsOf(bp)
	if err != nil {
		return err
	}

	// Printed before anything else: a resource the prober cannot work on at all is
	// something the operator needs to know regardless of what the run then does.
	for _, n := range notes {
		fmt.Fprintf(os.Stderr, "  %s\n", n)
	}

	if len(subjects) == 0 {
		return fmt.Errorf("%w: no resource in %s can be probed", errNothingToDo, *blueprintPath)
	}

	if *only != "" {
		if _, ok := probe.Lookup(*only); !ok {
			return usagef("-only %q is not a registered probe; run `probe -list` to see them", *only)
		}
	}

	if *list {
		return listProbes(subjects, *only)
	}

	// Everything past here needs a transport, a session and -- for the mutating tier --
	// the gating conjunction. Refusing explicitly rather than doing nothing, so that a
	// scripted run cannot mistake an unbuilt mode for a clean one.
	_ = allowMutations

	return fmt.Errorf("probe -mode %s: %w", *mode, errNotImplemented)
}

// keepOnlyResource narrows the blueprint to one resource.
//
// A separate flag from -only, which selects a probe. Both exist because the two axes are
// independent: probing one resource with the whole catalogue and probing every resource
// with one protocol are both things an operator wants, and conflating them into one flag
// would make the common case ambiguous.
//
// Naming a single resource *file* on -blueprint does not work, and deliberately: a
// resource file carries no provider block, so LoadDir refuses it rather than silently
// probing against a half-loaded document.
func keepOnlyResource(bp *blueprint.Blueprint, key string) error {
	for _, r := range bp.Resources {
		if r.Key == key {
			bp.Resources = []blueprint.Resource{r}
			return nil
		}
	}

	return usagef("-resource %q matches no resource key in the blueprint", key)
}

// subjectsOf flattens every probeable resource, collecting a note for each one that
// cannot be probed rather than failing the run.
//
// A blueprint with twenty resources of which two have no read operation should still
// probe the other eighteen. Refusing the whole run because one resource is unprobeable
// would make the prober useless on any real provider.
func subjectsOf(bp blueprint.Blueprint) ([]probe.Subject, []string, error) {
	var (
		subjects []probe.Subject
		notes    []string
	)

	for _, res := range bp.Resources {
		if res.Drop {
			continue
		}

		subj, err := probe.SubjectOf(bp, res)
		if err != nil {
			if errors.Is(err, probe.ErrNotProbeable) {
				notes = append(notes, err.Error())
				continue
			}
			return nil, nil, err
		}

		subjects = append(subjects, subj)
	}

	return subjects, notes, nil
}

// listProbes prints the catalogue with its worst-case costs.
//
// This works with no credentials, no cassettes and no network, which is what makes it
// the first useful thing the prober does: the costs and the mutating/read split are
// reviewable against a real blueprint before anybody points it at a tenant.
func listProbes(subjects []probe.Subject, only string) error {
	for _, subj := range subjects {
		canMutate, why := subj.CanMutate()

		fmt.Printf("\n%s  (%s)\n", subj.Resource, subj.CollectionTemplate)
		fmt.Printf("  %d field(s), %d writable", len(subj.Fields), len(subj.WritableFields()))
		if subj.NameField != "" {
			fmt.Printf(", name prefix goes in %q", subj.NameField)
		}
		fmt.Println()

		if !canMutate {
			// Stated per subject rather than per probe, because it is a property of
			// the resource and would otherwise be repeated nine times.
			fmt.Printf("  mutating probes refused: %s\n", why)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "\n  PROBE\tKIND\tREQUESTS\tCREATES")

		var totalRequests, totalCreates int

		for _, e := range probe.Catalogue(subj) {
			if only != "" && e.Name != only {
				continue
			}

			// A mutating probe against a subject that cannot be mutated costs nothing,
			// because it will not run. Showing its nominal cost would overstate the
			// budget by an order of magnitude.
			requests, creates := e.Cost, e.Creates
			if e.Kind == probe.KindMutating && !canMutate {
				requests, creates = 0, 0
			}

			fmt.Fprintf(w, "  %s\t%s\t%d\t%d\n", e.Name, e.Kind, requests, creates)

			totalRequests += requests
			totalCreates += creates
		}

		fmt.Fprintf(w, "  %s\t%s\t%d\t%d\n", "TOTAL", "", totalRequests, totalCreates)

		if err := w.Flush(); err != nil {
			return fmt.Errorf("writing the catalogue: %w", err)
		}

		printBudgetVerdict(totalRequests, totalCreates)
	}

	fmt.Println()

	return nil
}

// printBudgetVerdict compares the catalogue's worst case against the default caps.
//
// This is the whole reason -list prints costs rather than just names. The unbounded
// catalogue against the pilot wants roughly 500 requests and 226 creates, against
// defaults of 200 and 25 -- so the full run does not fit, and an operator needs to know
// that *before* pointing it at a tenant rather than discovering it as an exit 4 partway
// through.
//
// It is not a defect in either number. The per-field probes scale with the count of
// writable fields, and the answer is to narrow them with a plan: candidates, fixtures
// and a deny list are how an operator says which fields are worth the requests. Saying
// so here is more useful than quietly showing a total nobody compares to anything.
func printBudgetVerdict(requests, creates int) {
	budget := probe.Budget{}.WithDefaults()

	overRequests := requests > budget.MaxRequests
	overCreates := creates > budget.MaxCreates

	if !overRequests && !overCreates {
		fmt.Printf("\n  Fits the default budget (%d/%d requests, %d/%d creates).\n",
			requests, budget.MaxRequests, creates, budget.MaxCreates)
		return
	}

	fmt.Printf("\n  Does NOT fit the default budget: %d/%d requests, %d/%d creates.\n",
		requests, budget.MaxRequests, creates, budget.MaxCreates)
	fmt.Println("  The per-field probes scale with the number of writable fields, so a full run")
	fmt.Println("  needs a plan that narrows them -- candidates for the fields worth probing, and")
	fmt.Println("  a deny list for the rest. Without one, a record run would stop at exit 4.")
}
