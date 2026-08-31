# The root Makefile names the checks a change must pass, and delegates
# each one to the domain that owns it. `make test` runs every check CI
# runs, in the same commands, so a change that passes here passes
# there. The Go operator is the module at the root, so its checks are
# here; the idle screen and the docs are their own domains with
# their own Makefiles.
#
# The coverage floors are the one number each gate enforces: the Go
# floor is in .testcoverage.yml, and the Rust floor is in
# idle/Makefile. CI reads the same files, so a floor moves in one
# place.

.PHONY: test
test: test-go test-idle test-docs

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

.PHONY: test-go
test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
	go test -race ./...
	GOTOOLCHAIN=$(COVERAGE_TOOLCHAIN) go test -coverprofile=coverage.out ./...
	GOTOOLCHAIN=$(COVERAGE_TOOLCHAIN) go tool go-test-coverage --config=.testcoverage.yml

.PHONY: test-idle
test-idle:
	$(MAKE) -C idle test

.PHONY: test-docs
test-docs:
	$(MAKE) -C docs test
	$(MAKE) -C docs build
