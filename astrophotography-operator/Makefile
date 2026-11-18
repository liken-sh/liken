# `make test` runs the same checks as CI with the same commands, so a
# change that passes here passes in CI. The coverage floor is in
# .testcoverage.yml, which CI reads too.

# A recipe that fails or is killed can leave its target partly written
# and newer than its inputs. make then deletes the target, so the next
# run builds it again.
.DELETE_ON_ERROR:

.PHONY: test
test: test-go test-docs

# -race with -covermode=atomic runs the race check and writes the
# coverage profile in one test run. go-test-coverage is a `tool`
# dependency in go.mod, so the gate needs only the Go toolchain.

# A package with no test file writes no rows to the coverage profile,
# so the gate cannot fail it. test-go fails when this lists a package.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

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
# The reference pages are generated and committed. In a fresh checkout
# every file has the time git wrote it, so make can treat a page as
# current after its generator in brand/ changed. -B regenerates every
# page. The check fails when a page differs from the index or is new
# and untracked.
	$(MAKE) -C docs -B generate
	git diff --quiet -- docs/content/docs/reference \
		&& test -z "$$(git ls-files --others --exclude-standard -- docs/content/docs/reference)" \
		|| { git status --short -- docs/content/docs/reference; exit 1; }
	$(MAKE) -C docs test
# skills/ is generated from the guides and committed. The check fails
# when a guide changed without a run of `make -C docs skills`. It
# compares with the index, not with the last commit, so a commit that
# stages a guide and its regenerated skill together passes.
	$(MAKE) -C docs skills
	git diff --quiet -- skills && test -z "$$(git ls-files --others --exclude-standard -- skills)" \
		|| { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# Writes the coverage profile as one HTML page for the site. `test`
# does not depend on it, because only the gate decides whether a change
# passes. coverage is a `tool` of the docs module, so the command runs
# from docs/ with paths relative to it.
.PHONY: coverage-report
coverage-report: coverage.out
	cd docs && go tool coverage \
		-title astrophotography-operator \
		-root .. \
		-out ../coverage.html \
		-label Go ../coverage.out

# Runs the tests first when there is no profile to draw.
coverage.out:
	$(MAKE) test-go
