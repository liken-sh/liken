# This Makefile names the checks a change must pass, and delegates each
# one to the domain that owns it. `make test` runs every check CI runs,
# in the same commands, so a change that passes here passes there. The
# Go module is in this directory, so its checks are here. The docs are
# their own domain with their own Makefile.
#
# The coverage floor is the one number the gate enforces, in
# .testcoverage.yml. CI reads the same file, so the floor moves in one
# place.

.PHONY: test
test: test-go test-docs

# One run of the tests gives both the race check and the coverage
# profile, because -race with -covermode=atomic does both.
#
# go-test-coverage is a pinned tool dependency (the `tool` directive
# in go.mod), so the gate needs nothing installed beyond the Go
# toolchain.

# A package with no test file writes no rows to the profile, so the
# gate never measures it and cannot fail it. This lists such
# packages, and test-go fails on the first one.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

# The node build is the program the cec DaemonSet runs on each node
# with a CEC adapter, built with the tag node (leader_node.go says
# why). Its watches link client-go's reflector, its dynamic client, and
# rest, with the packages they need. The check vets the build and
# fails when it links the typed clientset, the informer factories, or
# leader election, which would more than double the program on every
# such node.
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
# The reference pages are generated and committed, so a checkout
# carries them. make decides that a page is current by comparing file
# times, and a fresh checkout gives each file the time that git wrote
# it. So in CI, make cannot be trusted to rebuild a page, and a change
# to a generator in brand/ can leave a stale page that the site
# publishes. The check runs the generate target with -B, which
# regenerates every page whatever the times say. It fails when the
# result differs from what git has staged, or adds an untracked file,
# as the skills check below does.
	$(MAKE) -C docs -B generate
	git diff --quiet -- docs/content/docs/reference \
		&& test -z "$$(git ls-files --others --exclude-standard -- docs/content/docs/reference)" \
		|| { git status --short -- docs/content/docs/reference; exit 1; }
	$(MAKE) -C docs test
# skills/ is generated from the guides and committed, so a checkout
# carries it. The check regenerates it and fails when the result differs
# from what git has staged, or adds an untracked file, which means a
# guide changed and nobody ran `make -C docs skills`. The comparison is
# with the index, not with the last commit, so a commit that stages a
# guide and its regenerated skill together passes.
	$(MAKE) -C docs skills
	git diff --quiet -- skills && test -z "$$(git ls-files --others --exclude-standard -- skills)" \
		|| { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# The coverage report is the profile the gate already measured, drawn
# as one HTML page for the site to publish. `test` does not depend on
# it: the gate decides whether a change passes, and the report only
# shows a reader what the tests reached, so a report that fails to
# render never fails a build.
#
# coverage is a tool of the docs module, the way Hugo and crdref are,
# so the command runs from docs/ and names the paths above it.
.PHONY: coverage-report
coverage-report: coverage.out
	cd docs && go tool coverage \
		-title equipment-operator \
		-root .. \
		-out ../coverage.html \
		-label Go ../coverage.out

# The report reads the profile test-go writes, so a tree with no
# profile runs the tests first.
coverage.out:
	$(MAKE) test-go
