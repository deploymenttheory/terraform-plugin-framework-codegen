package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

const (
	usageBindingsCheck = "bindings check -blueprint DIR -module DIR"
	usageBindingsFacts = "bindings facts -blueprint DIR -module DIR -out FILE [-check]"
)

// bindingsVerbs is the bindings group's verb table.
var bindingsVerbs = []command{
	{
		name:    "check",
		summary: "check blueprint SDK bindings against the pinned SDK",
		usage:   usageBindingsCheck,
		run:     runBindingsCheck,
	},
	{
		name:    "facts",
		summary: "derive static facts from the pinned SDK's types",
		usage:   usageBindingsFacts,
		run:     runBindingsFacts,
	},
}

func runBindingsGroup(args []string) error {
	return runVerbs("bindings", bindingsVerbs, "", args)
}

// bindingsFlags is the flag set the two bindings verbs share.
func bindingsFlags(verb, usage string) (*flag.FlagSet, *string, *string) {
	fs, _ := newFlagSet(verb, usage)
	blueprintPath := fs.String("blueprint", "", "blueprint file or directory (required)")
	module := fs.String("module", "", "directory of a module that depends on the SDK (required)")
	return fs, blueprintPath, module
}

func runBindingsCheck(args []string) error {
	fs, blueprintPath, module := bindingsFlags("bindings check", usageBindingsCheck)

	if err := parse(fs, args); err != nil {
		return err
	}

	bp, err := loadBindingsBlueprint(*blueprintPath, *module)
	if err != nil {
		return err
	}

	return checkBindings(bp, *module)
}

func runBindingsFacts(args []string) error {
	fs, blueprintPath, module := bindingsFlags("bindings facts", usageBindingsFacts)
	out := fs.String("out", "", "write the statically derived facts (zero-value unsendable) here (required)")
	check := fs.Bool("check", false,
		"write nothing; re-derive the facts and fail when -out's file differs -- the "+
			"drift gate for a committed static facts document")

	if err := parse(fs, args); err != nil {
		return err
	}

	if *out == "" {
		return usagef("-out is required: it names the static facts document to write or, with -check, to compare against")
	}

	bp, err := loadBindingsBlueprint(*blueprintPath, *module)
	if err != nil {
		return err
	}

	if *check {
		return staticFacts(bp, *module, "", *out)
	}
	return staticFacts(bp, *module, *out, "")
}

// loadBindingsBlueprint validates the shared flags and loads the blueprint.
func loadBindingsBlueprint(blueprintPath, module string) (blueprint.Blueprint, error) {
	if blueprintPath == "" {
		return blueprint.Blueprint{}, usagef("-blueprint is required")
	}
	// The SDK is resolved from a module that depends on it, so the version checked
	// is the version the provider will actually compile against rather than
	// whatever happens to be in a local checkout. The toolkit's own module
	// deliberately depends on no provider SDK, so it cannot be used here.
	if module == "" {
		return blueprint.Blueprint{}, usagef("-module is required: it names a module whose go.mod pins the SDK, " +
			"normally the generated provider's own directory")
	}

	return blueprint.LoadDir(blueprintPath)
}

// checkBindings verifies every declared binding against the pinned SDK.
func checkBindings(bp blueprint.Blueprint, module string) error {

	// Counted per kind rather than as one number, because this said "resource binding
	// set(s)" while verifying only resources -- so a blueprint whose data sources and actions
	// were entirely unchecked still reported a reassuring count.
	lists := 0
	for _, r := range bp.Resources {
		if r.List != nil {
			lists++
		}
	}

	log.Printf(
		"verifying bindings against the SDK pinned by %s: "+
			"%d resource(s), %d data source(s), %d list facet(s), %d action(s), %d ephemeral(s)",
		module, len(bp.Resources), len(bp.DataSources), lists, len(bp.Actions),
		len(bp.Ephemerals),
	)

	report := sdkbind.Verify(sdkbind.NewLoader(module), bp)

	if err := report.Err(); err != nil {
		// A one-line annotation for the checks UI. The detail is left to main, so
		// the same list is not printed twice.
		fmt.Fprintf(os.Stderr, "::error::%d blueprint binding(s) do not match the pinned SDK\n",
			len(report.Problems))
		return err
	}

	// A run that verified nothing must not look like a run that verified
	// everything: that is how a silently-skipped check stays green for months.
	if report.Checked == 0 {
		return fmt.Errorf("%w: no bindings were verified, which means the blueprint declares none", errNothingToDo)
	}

	log.Printf("✅ %d binding(s) match the SDK", report.Checked)

	// Named warnings, not failures: a value-typed struct the provider can never
	// omit is an SDK defect to fix, and the resource may be deliberately gated on
	// it -- but nobody should meet it first as a live 400 naming nothing.
	for _, w := range sdkbind.UnsendableStructWarnings(sdkbind.NewLoader(module), bp) {
		fmt.Fprintf(os.Stderr, "::warning::%s\n", w)
	}

	return nil
}

// staticFacts derives the SDK-type facts and writes or checks the committed document.
//
// Derivation and drift-check share one code path deliberately: the check is "would a
// fresh derivation produce these bytes", which is exactly what CI needs to prove the
// committed document still matches the pinned SDK.
func staticFacts(bp blueprint.Blueprint, module, out, check string) error {
	facts, err := sdkbind.StaticFacts(sdkbind.NewLoader(module), bp)
	if err != nil {
		return err
	}

	log.Printf("%d static fact(s) derived from the SDK's types", len(facts))

	if out != "" {
		if err := writeJSONFile(out, facts); err != nil {
			return err
		}
		log.Printf("✅ static facts written to %s", out)
	}

	if check != "" {
		committed, err := readFacts(check)
		if err != nil {
			return err
		}

		fresh, _ := json.Marshal(facts)
		have, _ := json.Marshal(committed)
		if !bytes.Equal(fresh, have) {
			fmt.Fprintf(os.Stderr,
				"::error::the committed static facts have drifted from the pinned SDK\n")
			return fmt.Errorf("static facts drift: %s no longer matches a fresh derivation; "+
				"regenerate with `bindings facts -out` and review the diff", check)
		}
		log.Printf("✅ static facts match the pinned SDK")
	}

	return nil
}
