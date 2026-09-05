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
# counts each block once, which is what .testcoverage.yml's threshold
# was set against. Move this pin to the newest toolchain that counts
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

# The report is a reader's view of the same profile the gate
# measures, as one self-contained HTML page. The site serves it at
# /coverage.html, and the publish job in ci.yaml renders it from the
# profile the test job uploaded.
#
# `test` does not depend on this. The gate says pass or fail, the
# report says where the holes are, and a failing gate must not wait
# on a page.
#
# coverage is the report tool the brand repository publishes, pinned
# as a tool of the docs module beside Hugo and crdref, so the command
# runs from docs/ and names the repository root with -root.
.PHONY: coverage-report
coverage-report: coverage.out
	cd docs && go tool coverage \
		-title display-operator -label Go \
		-root .. -out ../coverage.html ../coverage.out

# The profile is the gate's own output, so a report on a tree that
# has never run the tests runs them first.
coverage.out:
	$(MAKE) test-go
