# The checks of transect: formatting, the pinned htmx files, lints, and
# the tests under a coverage gate. CI and a workstation both run
# `make test`.
#
# clippy runs twice, once on the shipped code and once with the tests,
# because with --all-targets cargo checks both forms of every crate at
# the same time, and those rustc processes together can exceed the CI
# runner's memory. cargo llvm-cov runs the tests and enforces the
# coverage gate in one command.

# A recipe that fails or is killed can leave its target partly written
# and newer than its inputs. make deletes that target, so the next run
# builds it again.
.DELETE_ON_ERROR:

# The minimum line coverage, as a percentage. Raise it when coverage
# rises. Never lower it.
COVERAGE_FLOOR := 88

# The binary embeds htmx and its SSE extension from this directory.
# htmx.sha256 lists the same digests as the pin.toml beside it (`ci
# pins` checks that they agree), so `test` fails on a file that differs
# from its pin.
STATIC := crates/transect/src/web/static

.PHONY: test
test:
	cargo fmt --check
	cd $(STATIC) && sha256sum --check --quiet --strict htmx.sha256
	cargo clippy --locked -- -D warnings
	cargo clippy --locked --tests -- -D warnings
	cargo llvm-cov --locked --workspace --fail-under-lines $(COVERAGE_FLOOR)
# Writes the Cobertura file that `coverage-report` renders, from the
# profile data of the coverage run above.
	cargo llvm-cov report --cobertura --output-path coverage-transect.xml

# Renders the Cobertura file that `test` wrote as one HTML page. transect
# has no manual, so the site serves the page at coverage/transect.html.
# `test` does not depend on this target, because the page only shows
# which lines the tests reached, and the coverage gate already decides
# whether a change passes. transect has no docs module that pins the
# coverage tool, so the recipe runs it from brand's module.
.PHONY: coverage-report
coverage-report: coverage-transect.xml
	cd ../brand && go run ./coverage -title transect -root ../transect \
		-label Rust -out ../transect/coverage.html ../transect/coverage-transect.xml
