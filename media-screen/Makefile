# The checks of the media-screen crate: the format, the lints, and the
# tests under a coverage gate. CI runs `make test`, the same target a
# workstation runs.
#
# cargo-llvm-cov runs the tests itself and measures every line they
# reach, so one command is both the test run and the gate. The crate is
# rules plus one thread over a socket, and its unit tests reach nearly
# every line of it.

# The floor on line coverage, as a percentage. Raise it when the tests
# earn it; never lower it.
COVERAGE_FLOOR := 99

.PHONY: test
test:
	cargo fmt --check
	cargo clippy --all-targets -- -D warnings
	cargo llvm-cov --all-targets --fail-under-lines $(COVERAGE_FLOOR)
