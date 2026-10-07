# This Makefile names the checks a change must pass. `make test` runs
# every check CI runs, in the same commands, so a change that passes
# here passes there.
#
# The coverage floor is the one number the gate enforces, in
# .testcoverage.yml. CI reads the same file, so the floor moves in one
# place.

.PHONY: test
test: test-go test-docs

# One run of the tests gives both the race check and the coverage
# profile, because -race with -covermode=atomic does both.
#
# go-test-coverage is a pinned tool dependency (the `tool` directive
# in go.mod), so the gate needs nothing installed beyond the Go
# toolchain.

# A package with no test file writes no rows to the profile, so the
# gate never measures it and cannot fail it. This lists such
# packages, and test-go fails on the first one.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

.PHONY: test-go
test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	go vet ./...
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml

# drivers/generated.go maps each driver to the image that holds it, from
# indi/images/ and the tag in indi/package.toml, and holds the tag of
# the weston image from weston/package.toml. A test fails when the file
# is stale; this writes it again.
.PHONY: drivers
drivers:
	go test ./drivers -run TestTheMapIsCurrent -update

.PHONY: test-docs
test-docs:
# The reference pages are generated and committed, so a checkout
# carries them. make decides that a page is current by comparing file
# times, and a fresh checkout gives each file the time that git wrote
# it. So in CI, make cannot be trusted to rebuild a page, and a change
# to a generator in brand/ can leave a stale page that the site
# publishes. The check runs the generate target with -B, which
# regenerates every page whatever the times say. It fails when the
# result differs from what git has staged, or adds an untracked file,
# as the skills check below does.
	$(MAKE) -C docs -B generate
	git diff --quiet -- docs/content/docs/reference \
		&& test -z "$$(git ls-files --others --exclude-standard -- docs/content/docs/reference)" \
		|| { git status --short -- docs/content/docs/reference; exit 1; }
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
# coverage is a tool of the docs module, the way Hugo and crdref are,
# so the command runs from docs/ and names the paths above it.
.PHONY: coverage-report
coverage-report: coverage.out
	cd docs && go tool coverage \
		-title observatory-operator \
		-root .. \
		-out ../coverage.html \
		-label Go ../coverage.out

# The report reads the profile test-go writes, so a tree with no
# profile runs the tests first.
coverage.out:
	$(MAKE) test-go
