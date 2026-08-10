# Local mirror of ci.yml — the same commands, in the same order, so a green
# `make check` predicts a green CI run.

.PHONY: build vet fmt test cover hygiene check

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
