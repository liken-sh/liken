# `make test` runs the same commands as CI, so a change that passes here
# passes there. The checks of the docs run through docs/Makefile.

# A recipe that fails, or a build that is killed during one, leaves its
# target partly written and newer than its inputs. make deletes that
# target, so the next build makes it again and does not take it as
# current.
.DELETE_ON_ERROR:

.PHONY: test
test: test-go test-docs

# A package with no test file writes nothing to the coverage profile,
# so the coverage gate does not see it. test-go fails when this lists a
# package.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

.PHONY: test-go
test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	go vet ./...
	@# -race works with -covermode=atomic, so one run checks for races
	@# and writes the coverage profile. go-test-coverage is a tool directive
	@# in go.mod, so the gate needs only the Go toolchain.
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml

.PHONY: test-docs
test-docs: manifest-check
	$(MAKE) -C docs test
# skills/ is generated from the guides and committed. This regenerates
# it and fails when the result differs from the index or adds an
# untracked file, because then a guide changed and nobody ran
# `make -C docs skills`. The comparison is with the index, so a commit
# that stages a guide together with its regenerated skill passes.
	$(MAKE) -C docs skills
	git diff --quiet -- skills && test -z "$$(git ls-files --others --exclude-standard -- skills)" \
		|| { git status --short -- skills; exit 1; }
	$(MAKE) -C docs build

# The monitoring component has no code and no drill, so building it is
# its only check. The base builds alone, as on a cluster with no
# Prometheus, and then with the monitoring component, as on a cluster
# with the prometheus-operator.
.PHONY: manifest-check
manifest-check:
	kubectl kustomize deploy/ > /dev/null
	tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
	ln -s "$$PWD/deploy" "$$tmp/deploy" && \
	mkdir "$$tmp/consumer" && \
	printf '%s\n' \
		'apiVersion: kustomize.config.k8s.io/v1beta1' \
		'kind: Kustomization' \
		'resources:' \
		'  - ../deploy' \
		'components:' \
		'  - ../deploy/monitoring' \
		> "$$tmp/consumer/kustomization.yaml" && \
	kubectl kustomize "$$tmp/consumer" > /dev/null

# coverage-report renders the coverage profile as one HTML page for the
# site. `test` does not depend on it, so a report that fails to render
# does not fail a build. coverage is a tool of the docs module, so the
# command runs from docs/.
.PHONY: coverage-report
coverage-report: coverage.out
	cd docs && go tool coverage \
		-title per-node-csi-driver \
		-root .. \
		-out ../coverage.html \
		-label Go ../coverage.out

coverage.out:
	$(MAKE) test-go
