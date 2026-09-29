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
# The report of the same run, as a Cobertura file, for the page that
# `make coverage-report` draws. It reads the profiles the gate wrote.
	cargo llvm-cov report --cobertura --output-path coverage-media-screen.xml

# The coverage report is the Cobertura file that `test` wrote, drawn as
# one HTML page. The crate has no manual, so the site serves the page
# at coverage/media-screen.html. `test` does not depend on it: the gate
# decides whether a change passes, and the report only shows a reader
# what the tests reached.
#
# coverage is brand's tool. The crate has no docs module to pin it as
# a tool, so the command runs it from brand's own module.
.PHONY: coverage-report
coverage-report: coverage-media-screen.xml
	cd ../brand && go run ./coverage -title media-screen -root ../media-screen \
		-label Rust -out ../media-screen/coverage.html ../media-screen/coverage-media-screen.xml
