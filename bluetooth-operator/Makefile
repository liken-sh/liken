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

# The coverage gate measures on its own run, on a pinned toolchain.
# Go 1.27 splits a basic block into one profile row per run of code
# inside it, and repeats the whole block's statement count on every
# row. Every reader sums those rows, `go tool cover` included, so a
# block counts once more for each comment that interrupts it. Go 1.26
# counts each block once, which is what .testcoverage.yml's thresholds
# were set against. Move this pin to the newest toolchain that counts
# each block once.
#
# go-test-coverage is a pinned tool dependency (the `tool` directive
# in go.mod), so the gate needs nothing installed beyond the Go
# toolchain.
COVERAGE_TOOLCHAIN := go1.26.7

# A package with no test file writes no rows to the profile, so the
# gate never counts it: its number is not low, it is missing. This
# lists such packages, and test-go fails on the first one.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

.PHONY: test-go
test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	go vet ./...
	go test -race ./...
	GOTOOLCHAIN=$(COVERAGE_TOOLCHAIN) go test -coverprofile=coverage.out ./...
	GOTOOLCHAIN=$(COVERAGE_TOOLCHAIN) go tool go-test-coverage --config=.testcoverage.yml

.PHONY: test-docs
test-docs:
# The reference pages are generated and committed, so a checkout
# carries them. make decides that a page is current by comparing file
# times, and a fresh checkout gives every file about the same time.
# In CI, make then builds nothing, and a change to a generator in
# brand/ leaves a stale page that the site publishes. The check runs
# the generate target with -B, which regenerates every page whatever
# the times say, and fails when the result differs from what git has
# staged, or adds an untracked file, as the skills check below does.
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

# The report is not the gate. `go tool coverage` turns the profile
# test-go leaves in coverage.out into one self-contained HTML page.
# The page holds every source file, and colors the lines the tests
# reached and the lines they missed. The gate answers pass or fail,
# and the page says where the holes are. So `test` does not depend on
# this target, and CI runs it only on the way to a publish.
#
# The tool belongs to the brand component and is a tool dependency of
# the docs module, the same way Hugo and crdref are, so the recipe
# runs from docs/. `-root ..` names the tree the profile describes,
# which is this directory, and the tool reads the source files
# from there to color them.
.PHONY: coverage-report
coverage-report:
	cd docs && go tool coverage -title bluetooth-operator -label Go \
		-root .. -out ../coverage.html ../coverage.out
