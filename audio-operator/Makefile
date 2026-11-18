# `make test` runs the same commands as CI, so a change that passes
# here passes there. The coverage floor is in .testcoverage.yml, which
# CI also reads.

# A recipe that fails or is killed can leave its target partly written
# and newer than its inputs. This setting makes make delete that
# target, so the next run rebuilds it.
.DELETE_ON_ERROR:

.PHONY: test
test: test-go test-docs

# A package with no test file adds no rows to the coverage profile, so
# the coverage gate cannot fail it. test-go fails when this lists one.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

.PHONY: test-go
test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	go vet ./...
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml

# A test fails when the committed OpenAPI document differs from the
# one rendered from the route table in apiroutes.go in anything but
# servers. Run this after a route change, and commit the result.
.PHONY: openapi
openapi:
	go test -run TestTheServedAndCommittedDocumentsDifferOnlyInServers -update-openapi .

.PHONY: test-docs
test-docs:
# The reference pages are generated and committed. A fresh checkout
# gives every file the time git wrote it, so make can judge a stale
# page current, and a change to a generator in brand/ would publish
# the stale page. -B regenerates every page. The check fails when the
# result differs from the index or adds an untracked file.
	$(MAKE) -C docs -B generate
	git diff --quiet -- docs/content/docs/reference \
		&& test -z "$$(git ls-files --others --exclude-standard -- docs/content/docs/reference)" \
		|| { git status --short -- docs/content/docs/reference; exit 1; }
	$(MAKE) -C docs test
# skills/ is generated from the guides and committed. The check fails
# when a guide changed without a run of `make -C docs skills`. It
# compares with the index, not with the last commit, so a commit that
# stages a guide together with its regenerated skill passes.
	$(MAKE) -C docs skills
	git diff --quiet -- skills && test -z "$$(git ls-files --others --exclude-standard -- skills)" \
		|| { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# Renders the coverage profile as one HTML page for the site. `test`
# does not depend on it, so a report that fails to render cannot fail
# a build. coverage is a tool of the docs module, so the command runs
# from docs/.
.PHONY: coverage-report
coverage-report: coverage.out
	cd docs && go tool coverage \
		-title audio-operator \
		-root .. \
		-out ../coverage.html \
		-label Go ../coverage.out

coverage.out:
	$(MAKE) test-go
