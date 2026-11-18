# cargo llvm-cov runs the tests and fails when line coverage is under
# COVERAGE_FLOOR. No test opens a window: the tests draw the mark
# through a renderer that records its calls.

# Delete the target of a recipe that fails, so that the next build
# does not take a partly written file as current.
.DELETE_ON_ERROR:

# A percentage. Raise it when coverage rises, and never lower it.
COVERAGE_FLOOR := 95

.PHONY: test
test:
	cargo fmt --check
	cargo clippy --all-targets -- -D warnings
	cargo llvm-cov --all-targets --fail-under-lines $(COVERAGE_FLOOR)
