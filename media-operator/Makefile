# The root Makefile names the checks a change must pass, and delegates
# each one to the domain that owns it. `make test` runs every check CI
# runs, in the same commands, so a change that passes here passes
# there. The Go operator is the module at the root and the Rust crates
# are the workspace at the root, so their checks are here; the docs are
# their own domain with their own Makefile.
#
# The coverage floors are the one number each gate enforces: the Go
# floor is in .testcoverage.yml, and the three Rust floors are below.
# CI reads the same files, so a floor moves in one place.

.PHONY: test
test: test-go test-rust test-docs

# keycodes.go is generated from the kernel header named here, and it is
# committed, so a build needs the header only when the table is
# regenerated against a newer kernel.
INPUT_EVENT_CODES ?= /usr/include/linux/input-event-codes.h

.PHONY: codes
codes:
	go run keycodegen.go $(INPUT_EVENT_CODES) keycodes.go
	gofmt -w keycodes.go

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

# The Rust half is a cargo workspace with three members: the media-screen
# library, the idle screen that draws with it, and the display a playback
# pod draws over the film.
#
# The Rust checks: the format, the lints, and the tests under a coverage
# gate. cargo-llvm-cov runs the tests itself and measures every line they
# reach, so one command is both the test run and the gate. The unit tests
# cover the parsers, the rules, the measurements, and the timeline; the
# integration test runs the binary under cage on the headless backend, so
# the frame loop and the graphics setup count too. That test needs cage,
# wlr-randr, and a Vulkan device, and Mesa's lavapipe is enough of one.
#
# The three crates share a lock file and a target tree, so one invocation
# covers them all. Each crate keeps a floor of its own, because they earn
# different numbers: media-screen is rules plus one thread over a socket,
# the idle screen carries a window, and the display's own window half
# needs a compositor no unit test has.

# The floor on line coverage, as a percentage, one per crate. CI enforces the
# same numbers through this file. Raise one when the tests earn it; never
# lower it.
IDLE_COVERAGE_FLOOR := 95
SCREEN_COVERAGE_FLOOR := 99
DISPLAY_COVERAGE_FLOOR := 90

.PHONY: test-rust
test-rust:
	cargo fmt --check
	cargo clippy --workspace --all-targets -- -D warnings
# The image builds with `measure` off, so the lints run over that build
# too. The pass is the library and the binary alone, because the test
# targets reach the flags and the measurements the feature carries.
	cargo clippy -p idle-screen --no-default-features --lib --bins -- -D warnings
# The three gates share one instrumented target tree, and the idle test
# binary links media-screen, so its profiles count media-screen lines
# the idle tests never reach. Each gate starts from no profiles, so it
# measures its own tests alone. The first gate also starts from no
# objects: llvm-cov reads every test binary left in the target tree,
# and the idle binaries from an earlier run carry a second copy of
# media-screen that the media-screen tests never run, which reads as
# half the crate uncovered.
#
# Each gate is followed by a report of the same run, as a Cobertura
# file at the workspace root. `cargo llvm-cov report` reads the
# profiles the gate wrote, so it must run before the next clean
# deletes them. A report after every gate would measure one crate
# under another's tests. The three files are inputs to
# `make coverage-report`, which draws the page the site publishes.
	cargo llvm-cov clean --workspace
	cargo llvm-cov -p media-screen --all-targets \
		--fail-under-lines $(SCREEN_COVERAGE_FLOOR)
	cargo llvm-cov report -p media-screen \
		--cobertura --output-path coverage-media-screen.xml
	cargo llvm-cov clean --workspace --profraw-only
	cargo llvm-cov -p idle-screen --all-targets \
		--fail-under-lines $(IDLE_COVERAGE_FLOOR)
	cargo llvm-cov report -p idle-screen \
		--cobertura --output-path coverage-idle-screen.xml
	cargo llvm-cov clean --workspace --profraw-only
	cargo llvm-cov -p media-display --all-targets \
		--fail-under-lines $(DISPLAY_COVERAGE_FLOOR)
	cargo llvm-cov report -p media-display \
		--cobertura --output-path coverage-display.xml

.PHONY: test-docs
test-docs:
	$(MAKE) -C docs test
# skills/ is generated from the guides and committed, so a checkout
# carries it. The check regenerates it and fails when git reports a
# change or an untracked file there, which means a guide changed and
# nobody ran `make -C docs skills`.
	$(MAKE) -C docs skills
	test -z "$$(git status --porcelain -- skills)" || { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# The report is the coverage data as one page the site publishes at
# /coverage.html. It reads what the gates already wrote: the Go
# profile from test-go, and one Cobertura file per crate from
# test-rust. Run `make test` first, or the inputs are stale or
# missing.
#
# `test` does not depend on this, because a gate and a report are
# separate acts: the gate says pass or fail, and the report says
# where the lines are.
#
# The tool is the brand repository's, pinned as a tool dependency of
# the docs module the way Hugo and crdref are, so it runs from docs/
# and needs nothing installed. -root names the tree the inputs
# describe, which is this directory.
COVERAGE_INPUTS := coverage.out coverage-media-screen.xml coverage-idle-screen.xml \
	coverage-display.xml

.PHONY: coverage-report
coverage-report:
	cd docs && go tool coverage -title media-operator -root .. \
		-label Go -label "Rust (media-screen)" -label "Rust (idle-screen)" \
		-label "Rust (media-display)" \
		-out ../coverage.html \
		$(addprefix ../,$(COVERAGE_INPUTS))
