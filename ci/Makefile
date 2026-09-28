# The checks of the program that reads every package.toml. CI runs
# `make test`, the same target a workstation runs.
#
# The coverage gate measures on its own run, on a pinned toolchain.
# Go 1.27 splits a basic block into one profile row per run of code
# inside it, and repeats the whole block's statement count on every
# row, so a block counts once more for each comment that interrupts it.
# Go 1.26 counts each block once, which is what .testcoverage.yml's
# threshold was set against. Move this pin to the newest toolchain that
# counts each block once.
COVERAGE_TOOLCHAIN := go1.26.7

.PHONY: test
test:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
	go test -race ./...
	GOTOOLCHAIN=$(COVERAGE_TOOLCHAIN) go test -coverprofile=coverage.out ./...
	GOTOOLCHAIN=$(COVERAGE_TOOLCHAIN) go tool go-test-coverage --config=.testcoverage.yml
