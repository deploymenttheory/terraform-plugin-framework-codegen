package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint/merge"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
)

const usageMerge = "merge -blueprint DIR -facts FILE [-strategy annotate|apply] [-check] " +
	"[-accept-conflicts] [-github-summary PATH]"

func runMerge(args []string) error {
	fs, _ := newFlagSet("merge", usageMerge)

	var (
		blueprintPath = fs.String("blueprint", "", "blueprint file or directory (required)")
		factsPath     = fs.String("facts", "", "facts JSON to fold in (required)")
		strategy      = fs.String("strategy", string(merge.StrategyAnnotate),
			"annotate writes behaviour and descriptions; apply may also widen presence")
		check = fs.Bool("check", false,
			"write nothing and exit 1 if merging would change anything")
		acceptConflicts = fs.Bool("accept-conflicts", false,
			"suppress the conflict exit code; conflicts are still reported and still not applied")
		snapshotID = fs.String("snapshot-id", "",
			"identifies the evidence in the description marker; defaults to the facts file's directory")
		summaryPath = fs.String("github-summary", os.Getenv("GITHUB_STEP_SUMMARY"),
			"append a summary here")
	)

	if err := parse(fs, args); err != nil {
		return err
	}

	if *blueprintPath == "" {
		return usagef("-blueprint is required")
	}
	if *factsPath == "" {
		return usagef("-facts is required")
	}

	switch merge.Strategy(*strategy) {
	case merge.StrategyAnnotate, merge.StrategyApply:
	default:
		return usagef("-strategy %q is not annotate or apply", *strategy)
	}

	bp, err := blueprint.LoadDir(*blueprintPath)
	if err != nil {
		return err
	}

	facts, err := readFacts(*factsPath)
	if err != nil {
		return err
	}

	if len(facts) == 0 {
		return fmt.Errorf("%w: %s holds no facts", errNothingToDo, *factsPath)
	}

	id := *snapshotID
	switch {
	case id != "":
	case allStatic(facts):
		// Static facts write their own description channel under a fixed id: they are not
		// tied to any recording, and letting them default to a directory name made them
		// overwrite every live block -- after which re-merging the snapshot read as drift.
		id = merge.StaticSnapshotID
	default:
		// The snapshot directory name, which is what cassette.Write produced. Using it means
		// re-merging the same evidence is a no-op while newer evidence produces a visible
		// one-line diff.
		id = filepath.Base(filepath.Dir(*factsPath))
	}

	result, err := merge.Apply(&bp, facts, merge.Options{
		Strategy:   merge.Strategy(*strategy),
		SnapshotID: id,
	})
	if err != nil {
		return err
	}

	printMergeResult(result)

	if *summaryPath != "" {
		if err := appendMergeSummary(*summaryPath, result); err != nil {
			return err
		}
	}

	// -check is the CI gate: it writes nothing and fails if merging would change anything,
	// exactly parallel to `verify`. A blueprint that would change means somebody recorded
	// evidence and did not fold it in.
	if *check {
		if result.Changed() {
			fmt.Fprintf(os.Stderr,
				"::error::merging %s would change the blueprint; run merge and commit the result\n",
				*factsPath)
			return fmt.Errorf("%w: %d change(s) are not folded in", errDrift, len(result.Changes))
		}

		log.Printf("✅ the blueprint is up to date with %s", *factsPath)

		return conflictErr(result, *acceptConflicts)
	}

	if !result.Changed() {
		log.Printf("the blueprint already reflects %s", *factsPath)
		return conflictErr(result, *acceptConflicts)
	}

	if err := writeBlueprints(*blueprintPath, bp); err != nil {
		return err
	}

	log.Printf("folded %d change(s) into %s", len(result.Changes), *blueprintPath)

	return conflictErr(result, *acceptConflicts)
}

// conflictErr returns the conflict error unless it was accepted.
//
// -accept-conflicts suppresses the exit code only. It never applies anything: a flag that made
// merge overrule a human's curated decision would defeat the entire precedence design, so the
// only thing it can turn off is the non-zero exit.
func conflictErr(result merge.Result, accepted bool) error {
	if accepted {
		return nil
	}
	return result.Err()
}

// writeBlueprints writes the merged blueprint back.
//
// One file per resource plus the provider block, matching the layout LoadDir reads. Deliberately
// not one combined file: the split is how a twenty-resource provider stays reviewable, and
// collapsing it on the first merge would be a surprising side effect.
func writeBlueprints(root string, bp blueprint.Blueprint) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("reading %s: %w", root, err)
	}

	// A single file stays a single file.
	if !info.IsDir() {
		return blueprint.Save(root, bp)
	}

	providerPart := blueprint.Blueprint{
		FormatVersion: bp.FormatVersion,
		Provider:      bp.Provider,
		Source:        bp.Source,
	}
	if err := blueprint.Save(filepath.Join(root, "provider"+blueprint.Ext), providerPart); err != nil {
		return err
	}

	for _, res := range bp.Resources {
		part := blueprint.Blueprint{
			FormatVersion: bp.FormatVersion,
			Resources:     []blueprint.Resource{res},
		}
		path := filepath.Join(root, "resources", res.Key+blueprint.Ext)
		if err := blueprint.Save(path, part); err != nil {
			return err
		}
	}

	for _, ds := range bp.DataSources {
		part := blueprint.Blueprint{
			FormatVersion: bp.FormatVersion,
			DataSources:   []blueprint.DataSource{ds},
		}
		path := filepath.Join(root, "datasources", ds.Key+blueprint.Ext)
		if err := blueprint.Save(path, part); err != nil {
			return err
		}
	}

	return nil
}

// printMergeResult writes the outcome to stderr.
//
// Stderr with fmt rather than the log package, following printNotes: -q silences progress, and a
// conflict a human has to resolve is not progress.
func printMergeResult(result merge.Result) {
	fmt.Fprintf(os.Stderr, "\n%d change(s), %d conflict(s)", len(result.Changes), len(result.Conflicts))
	if result.Ignored > 0 {
		// So "nothing was observed" is distinguishable from "nothing observed was strong
		// enough to act on".
		fmt.Fprintf(os.Stderr, ", %d fact(s) too weak to act on", result.Ignored)
	}
	if result.Conditional > 0 {
		// Held-back evidence somebody paid a live run for must not be invisible: a
		// conditional fact goes into the description rather than the schema, and this is
		// the only place the operator learns that happened.
		fmt.Fprintf(os.Stderr, ", %d conditional fact(s) recorded but not applied",
			result.Conditional)
	}
	fmt.Fprintln(os.Stderr)

	if len(result.Changes) > 0 {
		fmt.Fprintln(os.Stderr, "\nchanges:")
		for _, c := range result.Changes {
			fmt.Fprintf(os.Stderr, "  %s\n", c)
		}
	}

	if len(result.Conflicts) > 0 {
		fmt.Fprintln(os.Stderr, "\nconflicts (not applied):")
		for _, c := range result.Conflicts {
			fmt.Fprintf(os.Stderr, "%s\n", indent(c.String()))
		}
		fmt.Fprintf(os.Stderr, "::warning::%d probe fact(s) disagree with the curated blueprint\n",
			len(result.Conflicts))
	}

	if len(result.Recommendations) > 0 {
		fmt.Fprintln(os.Stderr, "\nrecommended, not applied:")
		for _, r := range result.Recommendations {
			fmt.Fprintf(os.Stderr, "  %s\n", r)
		}
	}

	fmt.Fprintln(os.Stderr)
}

func indent(s string) string {
	var out string
	for line := range splitLines(s) {
		if line == "" {
			continue
		}
		out += "  " + line + "\n"
	}
	return out
}

// splitLines yields the lines of s without a trailing empty one.
func splitLines(s string) func(func(string) bool) {
	return func(yield func(string) bool) {
		start := 0
		for i := range len(s) {
			if s[i] == '\n' {
				if !yield(s[start:i]) {
					return
				}
				start = i + 1
			}
		}
		if start < len(s) {
			yield(s[start:])
		}
	}
}

func appendMergeSummary(path string, result merge.Result) error {
	var b []byte

	b = append(b, "### Probe facts merged\n\n"...)
	b = append(b, fmt.Sprintf("%d change(s), %d conflict(s).\n\n",
		len(result.Changes), len(result.Conflicts))...)

	if result.Conditional > 0 {
		b = append(b, fmt.Sprintf(
			"%d conditional fact(s) recorded in descriptions, not applied to the schema.\n\n",
			result.Conditional)...)
	}

	if len(result.Conflicts) > 0 {
		b = append(b, "<details><summary>Conflicts (not applied)</summary>\n\n```\n"...)
		for _, c := range result.Conflicts {
			b = append(b, c.String()...)
			b = append(b, '\n')
		}
		b = append(b, "```\n\n</details>\n"...)
	}

	return appendFile(path, string(b))
}

// errDrift marks a -check run whose blueprint is out of date.
var errDrift = errors.New("the blueprint has drifted from the recorded facts")

// allStatic reports whether every fact came from a static derivation rather than a
// recording -- which is what selects the static description channel.
func allStatic(facts []probe.Fact) bool {
	for _, f := range facts {
		if !strings.HasPrefix(f.Probe, "static.") {
			return false
		}
	}
	return len(facts) > 0
}
