# This Makefile names the checks a change must pass, and delegates each
# one to the domain that owns it. `make test` runs every check CI runs,
# in the same commands, so a change that passes here passes there. The
# Go operator is the module in this directory, so its checks are here;
# the docs are their own domain with their own Makefile.
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

# The report is a reader's view of the same profile the gate
# measures, as one self-contained HTML page. The site serves it at
# /coverage.html, and the publish job in ci.yaml renders it from the
# profile the test job uploaded.
#
# `test` does not depend on this. The gate says pass or fail, the
# report says where the holes are, and a failing gate must not wait
# on a page.
#
# coverage is the report tool the brand component publishes, pinned
# as a tool of the docs module beside Hugo and crdref, so the command
# runs from docs/ and names this directory with -root.
.PHONY: coverage-report
coverage-report: coverage.out
	cd docs && go tool coverage \
		-title display-operator -label Go \
		-root .. -out ../coverage.html ../coverage.out

# The profile is the gate's own output, so a report on a tree that
# has never run the tests runs them first.
coverage.out:
	$(MAKE) test-go
