# This Makefile names the checks a change must pass, and delegates each
# one to the domain that owns it. `make test` runs every check CI runs,
# in the same commands, so a change that passes here passes there. The
# Go driver is the module in this directory, so its checks are here; the
# docs are their own domain with their own Makefile.
#
# The coverage floors are the one number each gate enforces: the Go
# floor is in .testcoverage.yml. CI reads the same file, so a floor
# moves in one place.

.PHONY: test
test: test-go test-docs

# One run of the tests gives both the race check and the coverage
# profile, because -race with -covermode=atomic does both.
#
# go-test-coverage is a pinned tool dependency (the `tool` directive
# in go.mod), so the gate needs nothing installed beyond the Go
# toolchain.

# A package with no test file writes no rows to the profile, so the
# gate never counts it: its number is not low, it is missing. This
# lists such packages, and test-go fails on the first one.
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
	$(MAKE) -C docs test
# skills/ is generated from the guides and committed, so a checkout
# carries it. The check regenerates it and fails when the result differs
# from what git has staged, or adds an untracked file, which means a
# guide changed and nobody ran `make -C docs skills`. The comparison is
# with the index, not with the last commit, so a commit that stages a
# guide and its regenerated skill together passes.
	$(MAKE) -C docs skills
	git diff --quiet -- skills && test -z "$$(git ls-files --others --exclude-standard -- skills)" \
		|| { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# The coverage report is the profile the gate already measured, drawn
# as one HTML page for the site to publish. `test` does not depend on
# it: the gate decides whether a change passes, and the report only
# shows a reader what the tests reached, so a report that fails to
# render never fails a build.
#
# coverage is a tool of the docs module, the way Hugo is, so the
# command runs from docs/ and names the paths above it.
.PHONY: coverage-report
coverage-report: coverage.out
	cd docs && go tool coverage \
		-title git-csi-driver \
		-root .. \
		-out ../coverage.html \
		-label Go ../coverage.out

# The report reads the profile test-go writes, so a tree with no
# profile runs the tests first.
coverage.out:
	$(MAKE) test-go
