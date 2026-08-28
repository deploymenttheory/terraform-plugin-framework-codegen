# Local mirror of ci.yml — the same commands, in the same order, so a green
# `make check` predicts a green CI run.

.PHONY: build vet fmt test cover hygiene check diagrams

build:
	go build ./...

vet:
	go vet ./...

fmt:
	@unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then \
		echo "gofmt would rewrite:"; echo "$$unformatted"; exit 1; fi

test:
	go test -race -covermode=atomic -coverprofile=coverage.out ./internal/...

cover: test
	bash scripts/coverage_gate.sh coverage.out

hygiene:
	bash scripts/repo_hygiene_gate.sh

check: fmt build vet cover hygiene

# Re-render docs/diagrams from their JSON sources. Deliberately not part of
# `check`: rendering needs Node and a local archify clone, and CI has neither.
# See docs/diagrams/README.md.
# Diagrams exempt from the fit-on-screen check. The workflow renderer fixes a
# lane at 104px and a lane row at 640px wide, so a six-lane diagram only fits a
# 900px-tall viewport at a viewBox wide enough to push node text under the 6px
# floor that `validate` enforces — the two checks cannot both hold. Six lanes
# stay because each names the workflow file that runs that stage; three would
# file a stage under a workflow that does not run it.
DIAGRAMS_MAY_SCROLL ?= ci.workflow

# The archify skill, installed into the repo by `npx skills add tt-a1i/archify`.
ARCHIFY ?= .agents/skills/archify

diagrams:
	@test -f "$(ARCHIFY)/bin/archify.mjs" || { \
		echo "archify not found at $(ARCHIFY) — see docs/diagrams/README.md"; exit 1; }
	@for src in docs/diagrams/*.json; do \
		kind=$$(sed -n 's/.*"diagram_type": *"\([a-z]*\)".*/\1/p' "$$src" | head -1); \
		out="$${src%.json}.html"; \
		node "$(ARCHIFY)/bin/archify.mjs" deliver "$$kind" "$$src" "$$out" --quality showcase || exit 1; \
		case " $(DIAGRAMS_MAY_SCROLL) " in *" $$(basename "$$src" .json) "*) continue;; esac; \
		node "$(ARCHIFY)/bin/archify.mjs" visual-check "$$out" >/dev/null; status=$$?; \
		rm -f "$${out%.html}".visual-check.*; \
		test $$status -eq 0 || { echo "$$out does not fit the viewport"; exit 1; }; \
	done
