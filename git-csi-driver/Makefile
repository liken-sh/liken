# `make test` runs every check that CI runs, with the same commands, so
# a change that passes here passes there. The Go driver is the module in
# this directory, so its checks are here. The docs have their own
# Makefile in docs/.
#
# The Go coverage floor is in .testcoverage.yml, which CI reads too, so
# it is set in one place.

# A recipe that fails or is killed leaves its target partly written, and
# make would then treat that file as up to date. This makes make delete
# the target, so the next run builds it again.
.DELETE_ON_ERROR:

.PHONY: test
test: test-go test-docs

# One test run gives both the race check and the coverage profile,
# because -race works with -covermode=atomic.
#
# go-test-coverage is a `tool` dependency in go.mod, so the coverage
# gate needs only the Go toolchain.

# A package with no test file adds no rows to the coverage profile, so
# the gate cannot report it as low. This command lists such packages,
# and test-go fails if it finds any.
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
# skills/ is generated from the guides and committed. This regenerates
# it and fails when the result differs from the index or adds an
# untracked file, which means a guide changed without `make -C docs
# skills` being run. It compares with the index and not with HEAD, so a
# commit that stages a guide and its regenerated skill passes.
	$(MAKE) -C docs skills
	git diff --quiet -- skills && test -z "$$(git ls-files --others --exclude-standard -- skills)" \
		|| { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# The report renders the coverage profile as an HTML page for the site.
# `test` does not depend on it, because the gate decides whether a change
# passes and a report that fails to render should not fail a build.
#
# coverage is a tool dependency of the docs module, as Hugo is, so the
# command runs in docs/ and refers to the files above it.
.PHONY: coverage-report
coverage-report: coverage.out
	cd docs && go tool coverage \
		-title git-csi-driver \
		-root .. \
		-out ../coverage.html \
		-label Go ../coverage.out

# The report reads the profile that test-go writes, so without one this
# runs the tests first.
coverage.out:
	$(MAKE) test-go
