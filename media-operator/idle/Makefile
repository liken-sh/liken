# The Rust checks: the format, the lints, and the tests under a coverage
# gate. cargo-llvm-cov runs the tests itself and measures every line they
# reach, so one command is both the test run and the gate. The unit tests
# cover the parsers, the rules, the measurements, and the timeline; the
# integration test runs the binary under cage on the headless backend, so
# the frame loop and the graphics setup count too. That test needs cage,
# wlr-randr, and a Vulkan device, and Mesa's lavapipe is enough of one.
#
# This Makefile runs cargo from the workspace root. The two crates share
# a lock file and a target tree, so one invocation covers both. Each
# crate keeps a floor of its own, because they earn different numbers:
# media-screen is rules plus one thread over a socket, and the idle
# screen carries a window.

# The floor on line coverage, as a percentage, one per crate. CI enforces the
# same numbers through this file. Raise one when the tests earn it; never
# lower it.
IDLE_COVERAGE_FLOOR := 95
SCREEN_COVERAGE_FLOOR := 99

# Every cargo command runs at the workspace root, which is the directory
# above this one.
ROOT := $(CURDIR)/..

.PHONY: test
test:
	cd $(ROOT) && cargo fmt --check
	cd $(ROOT) && cargo clippy --workspace --all-targets -- -D warnings
# The image builds with `measure` off, so the lints run over that build
# too. The pass is the library and the binary alone, because the test
# targets reach the flags and the measurements the feature carries.
	cd $(ROOT) && cargo clippy -p idle-screen --no-default-features --lib --bins -- -D warnings
# The two gates share one instrumented target tree, and the idle test
# binary links media-screen, so its profiles count media-screen lines
# the idle tests never reach. Each gate starts from no profiles, so it
# measures its own tests alone.
#
# Each gate is followed by a report of the same run, as a Cobertura
# file at the workspace root. `cargo llvm-cov report` reads the
# profiles the gate wrote, so it must run before the next clean
# deletes them. A report after both gates would measure one crate
# under the other's tests. The two files are inputs to
# `make coverage-report`, which draws the page the site publishes.
	cd $(ROOT) && cargo llvm-cov clean --workspace --profraw-only
	cd $(ROOT) && cargo llvm-cov -p media-screen --all-targets \
		--fail-under-lines $(SCREEN_COVERAGE_FLOOR)
	cd $(ROOT) && cargo llvm-cov report -p media-screen \
		--cobertura --output-path $(ROOT)/coverage-media-screen.xml
	cd $(ROOT) && cargo llvm-cov clean --workspace --profraw-only
	cd $(ROOT) && cargo llvm-cov -p idle-screen --all-targets \
		--fail-under-lines $(IDLE_COVERAGE_FLOOR)
	cd $(ROOT) && cargo llvm-cov report -p idle-screen \
		--cobertura --output-path $(ROOT)/coverage-idle-screen.xml
