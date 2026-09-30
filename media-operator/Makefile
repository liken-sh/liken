# This Makefile names the checks a change must pass, and delegates each
# one to the domain that owns it. `make test` runs every check CI runs,
# in the same commands, so a change that passes here passes there. The
# Go module and the Rust workspace are both in this directory, so their
# checks are here; the docs are their own domain with their own
# Makefile.
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

# The pod build is the program the sidecar containers, the player shim,
# and the api run, built with the tag pod (leader_pod.go says why). The
# api's watches link client-go's reflector, its dynamic client, and rest,
# with the packages they need. The check vets the build and fails when
# it links the typed clientset, the informer factories, or leader
# election, which would more than double the program that every
# playback pod and every Remote's pod runs.
POD_BUILD_CLIENT_GO := go list -tags pod -deps . | grep -E '^k8s.io/client-go/(kubernetes|informers|dynamic/dynamicinformer|tools/leaderelection)(/|$$)'

.PHONY: test-go
test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	go vet ./...
	go vet -tags pod .
	test -z "$$($(POD_BUILD_CLIENT_GO))" || { echo 'the pod build links more of client-go than its watches need:'; $(POD_BUILD_CLIENT_GO); exit 1; }
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml

# The Rust half is a cargo workspace with two members: the idle screen,
# which draws with the media-screen library at the top of the
# repository, and the display a playback pod draws over the film.
#
# The Rust checks: the format, the lints, and the tests under a coverage
# gate. cargo-llvm-cov runs the tests itself and measures every line they
# reach, so one command is both the test run and the gate. The unit tests
# cover the parsers, the rules, the measurements, and the timeline; the
# integration test runs the binary under cage on the headless backend, so
# the frame loop and the graphics setup count too. That test needs cage,
# wlr-randr, and a Vulkan device, and Mesa's lavapipe is enough of one.
#
# The two crates share a lock file and a target tree. Each crate keeps a
# floor of its own, because they earn different numbers: the idle screen
# carries a window, and the display's own window half needs a compositor
# no unit test has. media-screen's own gate runs in its own directory.

# The floor on line coverage, as a percentage, one per crate. CI enforces the
# same numbers through this file. Raise one when the tests earn it; never
# lower it.
IDLE_COVERAGE_FLOOR := 95
DISPLAY_COVERAGE_FLOOR := 90

.PHONY: test-rust
test-rust:
	cargo fmt --check
	cargo clippy --workspace --all-targets -- -D warnings
# The image builds with `measure` off, so the lints run over that build
# too. The pass is the library and the binary alone, because the test
# targets reach the flags and the measurements the feature carries.
	cargo clippy -p idle-screen --no-default-features --lib --bins -- -D warnings
# The two gates share one instrumented target tree. Each gate starts
# from no profiles, so it measures its own tests alone, and the first
# gate also starts from no objects, so no test binary of an earlier run
# counts.
#
# Each gate is followed by a report of the same run, as a Cobertura
# file at the workspace root. `cargo llvm-cov report` reads the
# profiles the gate wrote, so it must run before the next clean
# deletes them. A report after every gate would measure one crate
# under another's tests. The two files are inputs to
# `make coverage-report`, which draws the page the site publishes.
	cargo llvm-cov clean --workspace
	cargo llvm-cov -p idle-screen --all-targets \
		--fail-under-lines $(IDLE_COVERAGE_FLOOR)
	cargo llvm-cov report -p idle-screen \
		--cobertura --output-path coverage-idle-screen.xml
	cargo llvm-cov clean --profraw-only
	cargo llvm-cov -p media-display --all-targets \
		--fail-under-lines $(DISPLAY_COVERAGE_FLOOR)
	cargo llvm-cov report -p media-display \
		--cobertura --output-path coverage-display.xml

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
# The tool is the brand component's, pinned as a tool dependency of
# the docs module the way Hugo and crdref are, so it runs from docs/
# and needs nothing installed. -root names the tree the inputs
# describe, which is this directory.
COVERAGE_INPUTS := coverage.out coverage-idle-screen.xml coverage-display.xml

.PHONY: coverage-report
coverage-report:
	cd docs && go tool coverage -title media-operator -root .. \
		-label Go -label "Rust (idle-screen)" \
		-label "Rust (media-display)" \
		-out ../coverage.html \
		$(addprefix ../,$(COVERAGE_INPUTS))
