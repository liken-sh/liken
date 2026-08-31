# The crate's checks: the format, the lints, and the tests under a
# coverage gate. cargo-llvm-cov runs the tests itself and measures
# every line they reach, so one command is both the test run and the
# gate. Nothing here opens a window: the tests read the mark and the
# palette out of the two originals, and they draw the mark through a
# renderer that keeps the calls instead of rasterizing them.

# The floor on line coverage, as a percentage. CI enforces the same
# number through this file. Raise it when the tests earn it; never
# lower it.
COVERAGE_FLOOR := 95

.PHONY: test
test:
	cargo fmt --check
	cargo clippy --all-targets -- -D warnings
	cargo llvm-cov --all-targets --fail-under-lines $(COVERAGE_FLOOR)
