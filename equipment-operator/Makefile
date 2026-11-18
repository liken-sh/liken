# `make test` runs every check that CI runs, with the same commands, so
# a change that passes here passes in CI. The minimum coverage is set in
# .testcoverage.yml, which CI reads too.

# A recipe that fails or is killed can leave its target partly written
# and newer than its inputs. make then deletes the target, so the next
# run builds it again.
.DELETE_ON_ERROR:

.PHONY: test
test: test-go test-docs

# One test run checks for races and writes the coverage profile, with
# -covermode=atomic, the mode that -race needs. go-test-coverage is a
# `tool` in go.mod, so the Go toolchain is the only thing to install.

# A package with no test file adds nothing to the coverage profile, so
# the coverage check cannot fail it. This command lists such packages,
# and test-go fails when it finds one.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

# The node build, with the tag `node`, is the program that the cec
# DaemonSet runs (leader_node.go). Its watches need only client-go's
# reflector, dynamic client, and rest. This check fails when it also
# links the typed clientset, the informer factories, or leader
# election, which make the program much larger.
NODE_BUILD_CLIENT_GO := go list -tags node -deps . | grep -E '^k8s.io/client-go/(kubernetes|informers|dynamic/dynamicinformer|tools/leaderelection)(/|$$)'

.PHONY: test-go
test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	go vet ./...
	go vet -tags node .
	test -z "$$($(NODE_BUILD_CLIENT_GO))" || { echo 'the node build links more of client-go than its watches need:'; $(NODE_BUILD_CLIENT_GO); exit 1; }
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml

.PHONY: test-docs
test-docs:
# The reference pages are generated and committed. make decides whether
# a page is current by comparing file times, and a fresh checkout gives
# each file the time that git wrote it. So in CI, make can skip a page
# that is out of date, and a change to a generator in brand/ can leave
# a stale page that the site publishes. -B makes the generate target
# regenerate every page. The check fails when the result differs from
# what git has staged, or adds an untracked file.
	$(MAKE) -C docs -B generate
	git diff --quiet -- docs/content/docs/reference \
		&& test -z "$$(git ls-files --others --exclude-standard -- docs/content/docs/reference)" \
		|| { git status --short -- docs/content/docs/reference; exit 1; }
	$(MAKE) -C docs test
# skills/ is generated from the guides and committed, and the same
# check fails when a guide changed and nobody ran `make -C docs skills`.
# It compares with the index and not with the last commit, so a commit
# that stages a guide and its regenerated skill together passes.
	$(MAKE) -C docs skills
	git diff --quiet -- skills && test -z "$$(git ls-files --others --exclude-standard -- skills)" \
		|| { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# The coverage report is an HTML page that the site publishes. `test`
# does not depend on it, because a report that fails to render must not
# fail a build. coverage is a tool of the docs module, so the command
# runs in docs/ with paths relative to it.
.PHONY: coverage-report
coverage-report: coverage.out
	cd docs && go tool coverage \
		-title equipment-operator \
		-root .. \
		-out ../coverage.html \
		-label Go ../coverage.out

coverage.out:
	$(MAKE) test-go
