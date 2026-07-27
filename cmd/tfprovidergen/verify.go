package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/emit"
)

// driftError reports that the committed provider no longer matches its
// blueprints. It carries its own exit code so CI can distinguish drift from the
// generator itself failing.
type driftError struct{ n int }

func (e *driftError) Error() string {
	return fmt.Sprintf("%d generated file(s) are out of date", e.n)
}

func (e *driftError) ExitCode() int { return exitError }

func runVerify(args []string) error {
	fs, _ := newFlagSet("verify", "verify -blueprint DIR -out DIR")

	var (
		blueprintPath = fs.String("blueprint", "", "blueprint file or directory (required)")
		out           = fs.String("out", "", "provider root to check (required)")
		summaryPath   = fs.String("github-summary", os.Getenv("GITHUB_STEP_SUMMARY"),
			"write a markdown summary here (defaults to $GITHUB_STEP_SUMMARY)")
	)

	if err := parse(fs, args); err != nil {
		return err
	}

	if *blueprintPath == "" {
		return usagef("-blueprint is required")
	}
	if *out == "" {
		return usagef("-out is required")
	}

	bp, err := blueprint.LoadDir(*blueprintPath)
	if err != nil {
		return err
	}

	gen, err := emit.New()
	if err != nil {
		return err
	}

	plan, err := gen.Build(bp, emit.Options{BlueprintPath: *blueprintPath})
	if err != nil {
		return err
	}

	// Two failure classes, reported separately because they have different
	// causes: drift means somebody edited generated output or changed a template
	// without regenerating, missing means a file was deleted or never written.
	var drifted, missing []string

	for _, f := range plan.Files {
		target := filepath.Join(*out, f.Path)

		existing, err := os.ReadFile(target) //nolint:gosec // the path is operator-supplied by design
		switch {
		case os.IsNotExist(err):
			missing = append(missing, f.Path)
		case err != nil:
			return fmt.Errorf("reading %s: %w", target, err)
		case !bytes.Equal(existing, f.Content):
			drifted = append(drifted, f.Path)
		}
	}

	if len(drifted) == 0 && len(missing) == 0 {
		log.Printf("✅ %d generated file(s) match their blueprints", len(plan.Files))
		return nil
	}

	report := buildDriftReport(*blueprintPath, *out, drifted, missing)

	fmt.Fprint(os.Stderr, report)
	// The GitHub annotation makes the failure visible in the checks UI rather
	// than only in the log.
	fmt.Fprintf(os.Stderr, "::error::Generated code is out of date. Run: tfprovidergen emit -blueprint %s -out %s\n",
		*blueprintPath, *out)

	if *summaryPath != "" {
		if err := appendFile(*summaryPath, report); err != nil {
			// Failing to write the summary must not mask the drift itself.
			log.Printf("warning: could not write the summary to %s: %v", *summaryPath, err)
		}
	}

	return &driftError{n: len(drifted) + len(missing)}
}

func buildDriftReport(blueprintPath, out string, drifted, missing []string) string {
	var b strings.Builder

	b.WriteString("### ❌ Generated code is out of date\n\n")
	b.WriteString("Run this and commit the result:\n\n```bash\ntfprovidergen emit -blueprint ")
	b.WriteString(blueprintPath + " -out " + out + "\n```\n\n")

	if len(drifted) > 0 {
		fmt.Fprintf(&b, "<details><summary>%d file(s) differ from what the blueprints produce</summary>\n\n```\n", len(drifted))
		for _, p := range drifted {
			b.WriteString(p + "\n")
		}
		b.WriteString("```\n\n</details>\n\n")
	}

	if len(missing) > 0 {
		fmt.Fprintf(&b, "<details><summary>%d file(s) are missing</summary>\n\n```\n", len(missing))
		for _, p := range missing {
			b.WriteString(p + "\n")
		}
		b.WriteString("```\n\n</details>\n\n")
	}

	return b.String()
}

func appendFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // operator-supplied
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}
