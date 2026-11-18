# `make test` runs the checks that CI runs, with the same commands. The
# docs have their own Makefile, which `test-docs` calls. The Go coverage
# floor is in `.testcoverage.yml`, which CI also reads.

# A recipe that fails or is killed can leave its target partly written
# and newer than its inputs. make deletes that target, so the next build
# makes it again.
.DELETE_ON_ERROR:

.PHONY: test
test: test-go test-docs

# One test run gives both the race check and the coverage profile,
# because `-race` with `-covermode=atomic` does both. `go-test-coverage`
# is a tool dependency in `go.mod`, so the gate needs only the Go
# toolchain.

# A package with no test file adds no rows to the coverage profile, so
# the coverage gate does not see it. `test-go` fails when this command
# lists a package.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

.PHONY: test-go
test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	go vet ./...
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml

.PHONY: test-docs
test-docs:
# The reference pages are generated and committed. A fresh checkout
# gives each file the time that git wrote it, so make's time check can
# skip a page that a change to a generator in `brand/` made stale. `-B`
# regenerates every page. The check fails when the result differs from
# what git has staged, or adds an untracked file.
	$(MAKE) -C docs -B generate
	git diff --quiet -- docs/content/docs/reference \
		&& test -z "$$(git ls-files --others --exclude-standard -- docs/content/docs/reference)" \
		|| { git status --short -- docs/content/docs/reference; exit 1; }
	$(MAKE) -C docs test
# `skills/` is generated from the guides and committed. The check fails
# when a guide changed and nobody ran `make -C docs skills`. It compares
# with the index, so a commit that stages a guide and its regenerated
# skill passes.
	$(MAKE) -C docs skills
	git diff --quiet -- skills && test -z "$$(git ls-files --others --exclude-standard -- skills)" \
		|| { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# `go tool coverage` writes the profile in `coverage.out` as one HTML
# page that marks the lines the tests ran and the lines they did not.
# The report passes or fails nothing, so `test` does not depend on it,
# and CI runs it only before a publish.
#
# The tool is a tool dependency of the docs module, so the recipe runs
# in `docs/`. `-root ..` points the tool at the source files in this
# directory, which the profile describes.
.PHONY: coverage-report
coverage-report:
	cd docs && go tool coverage -title bluetooth-operator -label Go \
		-root .. -out ../coverage.html ../coverage.out
