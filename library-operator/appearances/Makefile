# The appearances tool's checks: the format, the lints, and the unit
# tests. The unit tests load no model, so they need no OpenVINO.

.PHONY: test
test:
	cargo fmt --check
	cargo clippy --all-targets -- -D warnings
	cargo test
