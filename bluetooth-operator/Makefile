# The root Makefile names the checks a change must pass, and delegates
# each one to the domain that owns it. `make test` runs every check CI
# runs, in the same commands, so a change that passes here passes
# there. The Go operator is the module at the root, so its checks are
# here; the docs are their own domain with their own Makefile.
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
	$(MAKE) -C docs test
	$(MAKE) -C docs build

# The report is not the gate. `go tool coverage` turns the profile
# test-go leaves in coverage.out into one self-contained HTML page.
# The page holds every source file, and colors the lines the tests
# reached and the lines they missed. The gate answers pass or fail,
# and the page says where the holes are. So `test` does not depend on
# this target, and CI runs it only on the way to a publish.
#
# The tool belongs to the brand repository and is a tool dependency of
# the docs module, the same way Hugo and crdref are, so the recipe
# runs from docs/. `-root ..` names the tree the profile describes,
# which is this repository's root, and the tool reads the source files
# from there to color them.
.PHONY: coverage-report
coverage-report:
	cd docs && go tool coverage -title bluetooth-operator -label Go \
		-root .. -out ../coverage.html ../coverage.out
