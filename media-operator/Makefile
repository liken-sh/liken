# The root Makefile names the checks a change must pass, and delegates
# each one to the domain that owns it. `make test` runs every check CI
# runs, in the same commands, so a change that passes here passes
# there. The Go operator is the module at the root, so its checks are
# here; the Rust crates and the docs are their own domains with
# their own Makefiles, and the manifests under deploy/ have a check
# of their own below.
#
# The coverage floors are the one number each gate enforces: the Go
# floor is in .testcoverage.yml, and the two Rust floors are in
# idle/Makefile. CI reads the same files, so a floor moves in one
# place.

.PHONY: test
test: test-go test-rust test-deploy test-docs

# keycodes.go is generated from the kernel header named here, and it is
# committed, so a build needs the header only when the table is
# regenerated against a newer kernel.
INPUT_EVENT_CODES ?= /usr/include/linux/input-event-codes.h

.PHONY: codes
codes:
	go run keycodegen.go $(INPUT_EVENT_CODES) keycodes.go
	gofmt -w keycodes.go

# The coverage gate measures on its own run, on a pinned toolchain.
# Go 1.27 splits a basic block into one profile row per run of code
# inside it, and repeats the whole block's statement count on every
# row. Every reader sums those rows, `go tool cover` included, so a
# block counts once more for each comment that interrupts it. Go 1.26
# counts each block once, which is what .testcoverage.yml's thresholds
# were set against. Move this pin to the newest toolchain that counts
# each block once.
#
# go-test-coverage is a pinned tool dependency (the `tool` directive
# in go.mod), so the gate needs nothing installed beyond the Go
# toolchain.
COVERAGE_TOOLCHAIN := go1.26.7

# A package with no test file writes no rows to the profile, so the
# gate never counts it: its number is not low, it is missing. This
# lists such packages, and test-go fails on the first one.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

.PHONY: test-go
test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	go vet ./...
	go test -race ./...
	GOTOOLCHAIN=$(COVERAGE_TOOLCHAIN) go test -coverprofile=coverage.out ./...
	GOTOOLCHAIN=$(COVERAGE_TOOLCHAIN) go tool go-test-coverage --config=.testcoverage.yml

# The Rust half is a cargo workspace with two members, the media-screen
# library and the idle screen that draws with it. idle/Makefile holds
# both gates and runs cargo at the workspace root.
.PHONY: test-rust
test-rust:
	$(MAKE) -C idle test

# The manifests under deploy/ are checked against the Kubernetes API,
# offline. kubectl-validate carries the API schemas of each release it
# embeds, reads the CRDs beside the manifests for the custom kinds,
# and compiles every CEL rule a CRD declares, the way the API server
# does on apply. It is a tool directive in deploy/go.mod, a nested
# module, so its dependency graph stays out of the operator's go.sum,
# the same way docs/ pins Hugo. kustomization.yaml is left out,
# because kustomize's own kind is not part of the API.
#
# kubectl-validate asks the cluster the current kubeconfig names for
# its schemas before it falls back to the embedded ones, with no flag
# to turn that off. KUBECONFIG=/dev/null keeps the check offline and
# the same on every machine, whatever context a workstation holds.
#
# The version is the newest release the pinned kubectl-validate
# embeds, one minor behind the k3s liken ships. A release it does not
# embed is fetched from the GitHub API on every run, which fails
# offline and hits the rate limit under pre-commit. Move this to the
# shipped release when a newer kubectl-validate embeds it.
KUBERNETES_VERSION := 1.35
DEPLOY_MANIFESTS := $(notdir $(filter-out deploy/kustomization.yaml,$(wildcard deploy/*.yaml)))

.PHONY: test-deploy
test-deploy:
	cd deploy && KUBECONFIG=/dev/null go tool kubectl-validate --version $(KUBERNETES_VERSION) --local-crds . $(DEPLOY_MANIFESTS)

.PHONY: test-docs
test-docs:
	$(MAKE) -C docs test
	$(MAKE) -C docs build
