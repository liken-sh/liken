# The checks of transect: the format, the pinned htmx files, the lints,
# and the tests under a coverage gate. CI runs `make test`, the same
# target a workstation runs.
#
# clippy checks each crate twice: once as it ships and once with its
# tests. With --all-targets, cargo runs both checks of a crate at once,
# beside the checks of other crates, and the memory of every rustc that
# runs at that moment counts against the runner's memory. Two runs keep
# the two checks of each crate apart.
#
# cargo-llvm-cov runs the tests itself and measures every line they
# reach, so one command is both the test run and the gate.

# A recipe that fails, or a build that is killed during one, leaves its
# target partly written and newer than its inputs. make deletes that
# target, so the next build makes it again and does not take it as
# current.
.DELETE_ON_ERROR:

# The floor on line coverage, as a percentage. Raise it when the tests
# earn it; never lower it.
COVERAGE_FLOOR := 88

# The binary embeds htmx and its SSE extension from
# crates/transect/src/web/static/. htmx.sha256 names each file by the
# digest that pin.toml beside it gives, and `ci pins` checks the two
# against each other, so a file that differs from its pin fails here.
STATIC := crates/transect/src/web/static

.PHONY: test
test:
	cargo fmt --check
	cd $(STATIC) && sha256sum --check --quiet --strict htmx.sha256
	cargo clippy --locked -- -D warnings
	cargo clippy --locked --tests -- -D warnings
	cargo llvm-cov --locked --workspace --fail-under-lines $(COVERAGE_FLOOR)
# The report of the same run, as a Cobertura file, for the page that
# `make coverage-report` draws. It reads the profiles the gate wrote.
	cargo llvm-cov report --cobertura --output-path coverage-transect.xml

# The coverage report is the Cobertura file that `test` wrote, drawn as
# one HTML page. transect has no manual, so the site serves the page at
# coverage/transect.html. `test` does not depend on it: the gate decides
# whether a change passes, and the report only shows a reader what the
# tests reached.
#
# coverage is brand's tool. transect has no docs module to pin it as a
# tool, so the command runs it from brand's own module.
.PHONY: coverage-report
coverage-report: coverage-transect.xml
	cd ../brand && go run ./coverage -title transect -root ../transect \
		-label Rust -out ../transect/coverage.html ../transect/coverage-transect.xml
