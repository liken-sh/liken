# The root Makefile names the checks a change must pass, and delegates
# each one to the domain that owns it. `make test` runs every check CI
# runs, in the same commands, so a change that passes here passes
# there. The Go operator is the module at the root, so its checks are
# here; the media browser and the docs are their own domains with their
# own Makefiles.
#
# The coverage floors are the one number each gate enforces: the Go
# floor is in .testcoverage.yml, and the Rust floor is in
# media-browser/Makefile. CI reads the same files, so a floor moves in
# one place.

.PHONY: test
test: test-go test-media-browser test-docs

# The coverage gate measures on its own run, on a pinned toolchain.
# Go 1.27 splits a basic block into one profile row per run of code
# inside it, and repeats the whole block's statement count on every
# row. Every reader sums those rows, `go tool cover` included, so a
# block counts once more for each comment that interrupts it. Go 1.26
# counts each block once, which is what .testcoverage.yml's threshold
# was set against. Move this pin to the newest toolchain that counts
# each block once.
#
# go-test-coverage is a pinned tool dependency (the `tool` directive
# in go.mod), so the gate needs nothing installed beyond the Go
# toolchain.
COVERAGE_TOOLCHAIN := go1.26.7

.PHONY: test-go
test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
	go test -race ./...
	GOTOOLCHAIN=$(COVERAGE_TOOLCHAIN) go test -coverprofile=coverage.out ./...
	GOTOOLCHAIN=$(COVERAGE_TOOLCHAIN) go tool go-test-coverage --config=.testcoverage.yml

.PHONY: test-media-browser
test-media-browser:
	$(MAKE) -C media-browser test

.PHONY: test-docs
test-docs:
	$(MAKE) -C docs test
	$(MAKE) -C docs build

# The report is the coverage data as one page the site publishes at
# /coverage.html. It reads what the gates already wrote: the Go
# profile from test-go, and the Cobertura file from
# test-media-browser. Run `make test` first, or the inputs are stale
# or missing.
#
# `test` does not depend on this, because a gate and a report are
# separate acts: the gate says pass or fail, and the report says
# where the lines are.
#
# The tool is the brand repository's, pinned as a tool dependency of
# the docs module the way Hugo and crdref are, so it runs from docs/
# and needs nothing installed. -root names the tree the inputs
# describe, which is this directory.
COVERAGE_INPUTS := coverage.out coverage-media-browser.xml

.PHONY: coverage-report
coverage-report:
	cd docs && go tool coverage -title library-operator -root .. \
		-label Go -label "Rust (media-browser)" \
		-out ../coverage.html \
		$(addprefix ../,$(COVERAGE_INPUTS))
