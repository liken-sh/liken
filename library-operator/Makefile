# This Makefile names the checks a change must pass, and delegates each
# one to the domain that owns it. `make test` runs every check CI runs,
# in the same commands, so a change that passes here passes there. The
# Go operator is the module in this directory, so its checks are here;
# the media browser and the docs are their own domains with their own
# Makefiles.
#
# The coverage floors are the one number each gate enforces: the Go
# floor is in .testcoverage.yml, and the Rust floor is in
# media-browser/Makefile. CI reads the same files, so a floor moves in
# one place.

.PHONY: test
test: test-go test-media-browser test-docs

# One run of the tests gives both the race check and the coverage
# profile, because -race with -covermode=atomic does both.
#
# go-test-coverage is a pinned tool dependency (the `tool` directive
# in go.mod), so the gate needs nothing installed beyond the Go
# toolchain.

# The pod build is the program every pod and Job the operator creates
# runs, built with the tag pod (operate_pod.go says why). The check vets
# it and fails when anything in it links client-go, so a new import
# cannot bring client-go into every catalog pod and every library Job.
POD_BUILD_CLIENT_GO := go list -tags pod -deps . | grep '^k8s.io/client-go'

.PHONY: test-go
test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	go vet ./...
	go vet -tags pod .
	test -z "$$($(POD_BUILD_CLIENT_GO))" || { echo 'the pod build links client-go:'; $(POD_BUILD_CLIENT_GO); exit 1; }
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml

.PHONY: test-media-browser
test-media-browser:
	$(MAKE) -C media-browser test

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

# The report is the coverage data as one page the site publishes at
# /coverage.html. It reads what the gates already wrote: the Go
# profile from test-go, and the Cobertura file from
# test-media-browser. Run `make test` first, or the inputs are stale
# or missing.
#
# `test` does not depend on this, because a gate and a report are
# separate acts: the gate says pass or fail, and the report says
# where the lines are.
#
# The tool is the brand component's, pinned as a tool dependency of
# the docs module the way Hugo and crdref are, so it runs from docs/
# and needs nothing installed. -root names the tree the inputs
# describe, which is this directory.
COVERAGE_INPUTS := coverage.out coverage-media-browser.xml

.PHONY: coverage-report
coverage-report:
	cd docs && go tool coverage -title library-operator -root .. \
		-label Go -label "Rust (media-browser)" \
		-out ../coverage.html \
		$(addprefix ../,$(COVERAGE_INPUTS))
