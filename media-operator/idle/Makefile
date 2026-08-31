# The idle screen's checks: the format, the lints, and the tests
# under a coverage gate. cargo-llvm-cov runs the tests itself and
# measures every line they reach, so one command is both the test run
# and the gate. The unit tests cover the parsers, the measurements,
# and the timeline; the integration test runs the binary under cage on
# the headless backend, so the frame loop and the graphics setup count
# too. That test needs cage, wlr-randr, and a Vulkan device, and
# Mesa's lavapipe is enough of one.

# The floor on line coverage, as a percentage. CI enforces the same
# number through this file. Raise it when the tests earn it; never
# lower it.
COVERAGE_FLOOR := 90

.PHONY: test
test:
	cargo fmt --check
	cargo clippy --all-targets -- -D warnings
	cargo llvm-cov --all-targets --fail-under-lines $(COVERAGE_FLOOR)
