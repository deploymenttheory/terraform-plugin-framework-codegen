// Package revise materializes the revised spec: the pinned upstream
// document with every accepted correction applied, written deterministically
// to spec/revised.yaml — the single source of truth for all generation.
//
// Revision has two halves. This package holds the materializing half:
// Materialize verifies the pin, refuses while any proposed correction awaits
// a human decision, applies the accepted corrections, and writes the revised
// document. The other half — compiling audit observations into proposed
// corrections — lands here later as its own entry point beside Materialize;
// nothing needs renaming when it does.
//
// Downstream always reads spec/revised.yaml, even when no corrections exist:
// the zero-correction revision is the block-style-normalized round trip of
// the upstream document, so consumers never branch on "was anything
// corrected". The imported document's bytes and hash never change.
package revise

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/correction"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/store"
)

const (
	// OutputName is the revised document's file name inside the spec
	// directory. Always YAML, whatever format the vendor published in.
	OutputName = "revised.yaml"
	// ProposedDirName is the subdirectory of the corrections directory where
	// pipeline-compiled corrections wait for a human decision.
	ProposedDirName = "proposed"
	// RejectedDirName is the subdirectory of the corrections directory
	// holding markers of the proposals a human rejected.
	RejectedDirName = "rejected"
)

// Result reports one materialization.
type Result struct {
	// Lock is the verified pin the revision was taken from.
	Lock store.Lock
	// Applied lists the accepted correction files in application order;
	// empty when the revised spec is the upstream document unchanged.
	Applied []string
	// OutputPath is where the revised document was written.
	OutputPath string
}

// Materialize writes dir/revised.yaml from the pinned upstream document plus
// every accepted correction in dir/corrections.
//
// It refuses a tampered pin, and it refuses while dir/corrections/proposed
// holds any correction awaiting a decision — there is deliberately no flag
// to look away. Unchanged inputs produce byte-identical output.
func Materialize(dir string) (Result, error) {
	lock, err := store.Verify(dir)
	if err != nil {
		return Result{}, err
	}

	correctionsDir := filepath.Join(dir, correction.DirName)
	if err := refusePending(correctionsDir); err != nil {
		return Result{}, err
	}

	corrections, err := correction.Load(correctionsDir)
	if err != nil {
		return Result{}, err
	}

	upstream, err := os.ReadFile(filepath.Join(dir, store.DocumentName)) //nolint:gosec // the fixed name under the operator-supplied dir
	if err != nil {
		return Result{}, err
	}

	// A stale correction (its change already present upstream) refuses here,
	// naming the file to delete.
	revised, err := correction.Apply(upstream, corrections)
	if err != nil {
		return Result{}, err
	}

	res := Result{Lock: lock, OutputPath: filepath.Join(dir, OutputName)}
	if err := os.WriteFile(res.OutputPath, revised, 0o600); err != nil {
		return Result{}, fmt.Errorf("writing the revised document: %w", err)
	}
	for _, c := range corrections {
		res.Applied = append(res.Applied, c.File)
	}
	return res, nil
}

// refusePending fails while any proposed correction awaits a decision,
// naming each file and the two legal resolutions. No flag bypasses this:
// v1's -allow-conflicts taught that an ignorable gate is an ignored gate,
// so the only ways forward are the decisions themselves.
func refusePending(correctionsDir string) error {
	proposedDir := filepath.Join(correctionsDir, ProposedDirName)
	entries, err := os.ReadDir(proposedDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	var pending []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), correction.Suffix) {
			continue
		}
		pending = append(pending, filepath.Join(proposedDir, e.Name()))
	}
	if len(pending) == 0 {
		return nil
	}

	noun := "corrections await"
	if len(pending) == 1 {
		noun = "correction awaits"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d proposed %s a decision:\n", len(pending), noun)
	for _, p := range pending {
		fmt.Fprintf(&b, "  %s\n", p)
	}
	fmt.Fprintf(&b, "resolve every one, then rerun:\n")
	fmt.Fprintf(&b, "  accept — move the file into %s%c\n", correctionsDir, filepath.Separator)
	fmt.Fprintf(&b, "  reject — add a marker in %s%c and delete the proposal\n",
		filepath.Join(correctionsDir, RejectedDirName), filepath.Separator)
	b.WriteString("no flag skips this; a pending proposal is a human decision that has not been made")
	return errors.New(b.String())
}
