# `make test` runs the same commands as CI, so a change that passes here
# passes there. The Go checks are in this Makefile, and the docs checks
# are in docs/Makefile. The coverage threshold is in .testcoverage.yml,
# which CI reads too.

# A recipe that fails can leave a half-written target that is newer
# than its inputs, which make would then treat as up to date.
.DELETE_ON_ERROR:

.PHONY: test
test: test-go test-docs

# A package with no test file adds nothing to the coverage profile, so
# the coverage check would pass it unmeasured. test-go fails when this
# list is not empty.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

# go-test-coverage is a `tool` dependency in go.mod, so test-go needs
# only the Go toolchain.
.PHONY: test-go
test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	go vet ./...
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml

.PHONY: test-docs
test-docs:
# The reference pages are generated and committed. A fresh checkout
# gives every file the time of the checkout, so make could skip a page
# that a change to the generator in brand/ made stale. -B regenerates
# every page. The check fails when a page differs from the git index or
# is a new untracked file.
	$(MAKE) -C docs -B generate
	git diff --quiet -- docs/content/docs/reference \
		&& test -z "$$(git ls-files --others --exclude-standard -- docs/content/docs/reference)" \
		|| { git status --short -- docs/content/docs/reference; exit 1; }
	$(MAKE) -C docs test
# skills/ is generated from the guides and committed. The check fails
# when a guide changed and nobody ran `make -C docs skills`. It compares
# with the git index, not the last commit, so a commit that stages a
# guide together with its regenerated skill passes.
	$(MAKE) -C docs skills
	git diff --quiet -- skills && test -z "$$(git ls-files --others --exclude-standard -- skills)" \
		|| { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# coverage-report renders the coverage profile as an HTML page for the
# site. `test` does not depend on it, so a failure to render the report
# fails no test. coverage is a tool of the docs module, so the command
# runs from docs/.
.PHONY: coverage-report
coverage-report: coverage.out
	cd docs && go tool coverage \
		-title people-operator \
		-root .. \
		-out ../coverage.html \
		-label Go ../coverage.out

coverage.out:
	$(MAKE) test-go
