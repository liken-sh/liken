# `make test` runs the same commands as CI, so a change that passes
# here passes there. The coverage floor is in .testcoverage.yml.

# A recipe that fails or is killed can leave a partly written target
# that is newer than its inputs, so make deletes it.
.DELETE_ON_ERROR:

.PHONY: test
test: test-go test-docs

# A package with no test file adds nothing to the coverage profile, so
# the coverage gate never counts it. This command lists such packages,
# and test-go fails when the list is not empty.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

# One test run does both the race check and the coverage profile,
# because -race works with -covermode=atomic. go-test-coverage is a
# `tool` dependency in go.mod, so the gate needs only the Go toolchain.
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
# gives every file the time that git wrote it, so make cannot tell from
# file times whether a change to a generator in brand/ left a page
# stale. -B regenerates every page, and the check fails when the result
# differs from what git has staged or adds an untracked file.
	$(MAKE) -C docs -B generate
	git diff --quiet -- docs/content/docs/reference \
		&& test -z "$$(git ls-files --others --exclude-standard -- docs/content/docs/reference)" \
		|| { git status --short -- docs/content/docs/reference; exit 1; }
	$(MAKE) -C docs test
# skills/ is generated from the guides and committed. The check fails
# when a guide changed and nobody ran `make -C docs skills`. It
# compares with the index, not with the last commit, so a commit that
# stages a guide and its regenerated skill together passes.
	$(MAKE) -C docs skills
	git diff --quiet -- skills && test -z "$$(git ls-files --others --exclude-standard -- skills)" \
		|| { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# The coverage report renders the coverage profile as one HTML page for
# the site to publish. `test` does not depend on it, so a failure to
# render the report never fails a build. coverage is a tool of the docs
# module, so the command runs from docs/ with paths relative to it.
.PHONY: coverage-report
coverage-report: coverage.out
	cd docs && go tool coverage \
		-title firmware-operator \
		-root .. \
		-out ../coverage.html \
		-label Go ../coverage.out

# When there is no coverage profile yet, this runs the tests first.
coverage.out:
	$(MAKE) test-go
