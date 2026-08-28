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
ARCHIFY ?= $(HOME)/GitHub/archify/archify

diagrams:
	@test -f "$(ARCHIFY)/bin/archify.mjs" || { \
		echo "archify not found at $(ARCHIFY) — see docs/diagrams/README.md"; exit 1; }
	@for src in docs/diagrams/*.json; do \
		kind=$$(sed -n 's/.*"diagram_type": *"\([a-z]*\)".*/\1/p' "$$src" | head -1); \
		out="$${src%.json}.html"; \
		node "$(ARCHIFY)/bin/archify.mjs" deliver "$$kind" "$$src" "$$out" --quality showcase || exit 1; \
	done
