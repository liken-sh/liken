# `make test` runs every check that CI runs, with the same commands.

# A recipe that fails or is killed can leave a partly written target
# that is newer than its inputs. make deletes it, so the next run builds
# it again.
.DELETE_ON_ERROR:

.PHONY: test
test: test-go test-docs

# A package with no test file writes no rows to the coverage profile,
# so the coverage gate cannot fail it. test-go fails when this command
# lists any package.
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
# gives every file the time that git wrote it, so make can treat a
# stale page as current. -B regenerates every page, and the check fails
# when the result differs from the git index.
	$(MAKE) -C docs -B generate
	git diff --quiet -- docs/content/docs/reference \
		&& test -z "$$(git ls-files --others --exclude-standard -- docs/content/docs/reference)" \
		|| { git status --short -- docs/content/docs/reference; exit 1; }
	$(MAKE) -C docs test
# skills/ is generated from the guides and committed. The check fails
# when a guide changed and nobody ran `make -C docs skills`. It compares
# with the git index, not the last commit, so a commit that stages a
# guide and its regenerated skill passes.
	$(MAKE) -C docs skills
	git diff --quiet -- skills && test -z "$$(git ls-files --others --exclude-standard -- skills)" \
		|| { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# The coverage report is one HTML page that the site serves at
# /coverage.html. The CI workflow renders it from the profile that the
# go job uploads. The docs module pins the coverage tool of brand/, so
# the command runs from docs/ and passes this directory with -root.
.PHONY: coverage-report
coverage-report: coverage.out
	cd docs && go tool coverage \
		-title display-operator -label Go \
		-root .. -out ../coverage.html ../coverage.out

coverage.out:
	$(MAKE) test-go
