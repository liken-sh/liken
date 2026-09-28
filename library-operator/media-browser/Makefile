# The media browser's checks: the format, the lints, and the tests
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
COVERAGE_FLOOR := 97

.PHONY: test
test:
	cargo fmt --check
	cargo clippy --all-targets -- -D warnings
	cargo llvm-cov --all-targets --fail-under-lines $(COVERAGE_FLOOR)
# The gate's own run, written out as a Cobertura file at the
# repository root. `cargo llvm-cov report` reads the profiles the
# gate just wrote, so it runs right after the gate. The file is an
# input to `make coverage-report`, which draws the page the site
# publishes.
	cargo llvm-cov report --cobertura --output-path ../coverage-media-browser.xml
