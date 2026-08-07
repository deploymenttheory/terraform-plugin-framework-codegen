package main

import (
	"fmt"
	"log"
	"sort"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/corpus"
)

const (
	usageCorpusSync    = "corpus sync [-id ID]"
	usageCorpusCheck   = "corpus check [-id ID]"
	usageCorpusRefresh = "corpus refresh [-id ID] [-dry-run]"
)

// corpusVerbs is the corpus group's verb table.
var corpusVerbs = []command{
	{
		name:    "sync",
		summary: "materialise the pinned API documents the tests read, into the local cache",
		usage:   usageCorpusSync,
		run:     runCorpusSync,
	},
	{
		name:    "check",
		summary: "report whether an upstream document still matches its pin; exits 1 when it does not",
		usage:   usageCorpusCheck,
		run:     runCorpusCheck,
	},
	{
		name:    "refresh",
		summary: "take an upstream change deliberately: report the delta and restate the pin",
		usage:   usageCorpusRefresh,
		run:     runCorpusRefresh,
	},
}

func runCorpus(args []string) error {
	return runVerbs("corpus", corpusVerbs, "", args)
}

// selectedIDs resolves -id to one document or to all of them, in a stable order
// so output is comparable between runs.
func selectedIDs(id string) ([]string, error) {
	if id != "" {
		if _, err := corpus.PinFor(id); err != nil {
			return nil, err
		}
		return []string{id}, nil
	}

	ids, err := corpus.IDs()
	if err != nil {
		return nil, err
	}
	sort.Strings(ids)

	return ids, nil
}

// runCorpusSync fills the cache.
//
// One command so CI can pre-warm before the suite runs, and so an offline
// developer has something to run while they still have a connection. It is a
// no-op against a warm cache, which is what makes it safe to put in front of
// every job.
func runCorpusSync(args []string) error {
	fs, _ := newFlagSet("corpus sync", usageCorpusSync)
	id := fs.String("id", "", "sync only this document (default: all of them)")

	if err := parse(fs, args); err != nil {
		return err
	}

	ids, err := selectedIDs(*id)
	if err != nil {
		return err
	}

	for _, name := range ids {
		snap, err := corpus.Ensure(name)
		if err != nil {
			return err
		}
		log.Printf("✅ %-14s %s", name, snap.Dir)
	}

	return nil
}

// runCorpusCheck reports whether upstream still serves what is pinned.
//
// Deliberately separate from sync, and it is the whole reason a vendor's
// release does not break a pull request. "The tests have their reproducible
// input" and "upstream has moved" are different questions: the first is
// answered by the cache and must block, the second is answered here and belongs
// on a schedule, where it opens an issue instead.
func runCorpusCheck(args []string) error {
	fs, _ := newFlagSet("corpus check", usageCorpusCheck)
	id := fs.String("id", "", "check only this document (default: all of them)")

	if err := parse(fs, args); err != nil {
		return err
	}

	ids, err := selectedIDs(*id)
	if err != nil {
		return err
	}

	moved := 0

	for _, name := range ids {
		pin, err := corpus.PinFor(name)
		if err != nil {
			return err
		}

		result, err := corpus.CheckUpstream(name)
		if err != nil {
			// Unreachable is not "moved". Saying so keeps a scheduled run from
			// filing an issue about somebody's DNS.
			log.Printf("⚠️  %-14s could not be checked: %v", name, err)
			continue
		}

		if result.Matches {
			log.Printf("✅ %-14s unchanged: %s", name, pin.Describe())
			continue
		}

		moved++
		log.Printf("📌 %-14s upstream has moved", name)
		log.Printf("   pinned  %s", pin.Describe())
		log.Printf("   serving %s", result.Describe())
		log.Printf("   take it with: tfpfgen corpus refresh -id %s", name)
	}

	if moved > 0 {
		return fmt.Errorf("%d pinned document(s) no longer match upstream", moved)
	}

	return nil
}

// runCorpusRefresh restates a pin against what upstream now serves.
//
// It prints the delta before writing, because the delta is the thing being
// reviewed: a document that gained forty paths is a different input to the
// tests that assert on inference, and the number is what tells you whether to
// expect those tests to move.
func runCorpusRefresh(args []string) error {
	fs, _ := newFlagSet("corpus refresh", usageCorpusRefresh)

	var (
		id     = fs.String("id", "", "refresh only this document (default: all of them)")
		dryRun = fs.Bool("dry-run", false, "report the delta and write nothing")
	)

	if err := parse(fs, args); err != nil {
		return err
	}

	ids, err := selectedIDs(*id)
	if err != nil {
		return err
	}

	changed := 0

	for _, name := range ids {
		pin, err := corpus.PinFor(name)
		if err != nil {
			return err
		}

		result, err := corpus.CheckUpstream(name)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}

		if result.Matches {
			log.Printf("✅ %-14s unchanged: %s", name, pin.Describe())
			continue
		}

		changed++
		log.Printf("📌 %-14s", name)
		log.Printf("   was %s", pin.Describe())
		log.Printf("   now %s", result.Describe())

		if result.MirrorURL == "" {
			log.Printf("   ⚠️  this pin has no mirror, so these bytes stay fetchable only while "+
				"%s serves them", result.SourceURL)
		}
	}

	if *dryRun {
		log.Printf("dry run; the lock was not rewritten")
		return nil
	}

	if changed == 0 {
		return nil
	}

	if err := corpus.RewriteLock(ids); err != nil {
		return err
	}

	log.Printf("✅ rewrote %s", corpus.LockPath())
	log.Printf("   re-mirror the new bytes, then review the diff: it changes what the tests read against")

	return nil
}
