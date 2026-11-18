# CI and a workstation both run `make test`.

# A recipe that fails can leave a partly written target. make deletes
# it, so the next run builds it again.
.DELETE_ON_ERROR:

# The minimum line coverage, as a percentage. Raise it when the tests
# cover more. Do not lower it.
COVERAGE_FLOOR := 99

# cargo llvm-cov runs the tests and measures coverage in one run. The
# report command then writes that run's coverage as a Cobertura file.
.PHONY: test
test:
	cargo fmt --check
	cargo clippy --all-targets -- -D warnings
	cargo llvm-cov --all-targets --fail-under-lines $(COVERAGE_FLOOR)
	cargo llvm-cov report --cobertura --output-path coverage-media-screen.xml

# Writes the Cobertura file as an HTML page. The crate has no manual, so
# the site serves the page at coverage/media-screen.html. The coverage
# tool is in brand's Go module, so the command runs from there.
.PHONY: coverage-report
coverage-report: coverage-media-screen.xml
	cd ../brand && go run ./coverage -title media-screen -root ../media-screen \
		-label Rust -out ../media-screen/coverage.html ../media-screen/coverage-media-screen.xml
