.DELETE_ON_ERROR:

# A package with no test file adds no rows to the coverage profile, so
# the coverage gate cannot see it. This command lists such packages,
# and `test` fails if it prints any.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

# Some programs that import this module must stay small, so `test`
# checks which k8s.io packages these packages link.
#
# library-operator's pod build imports `apiclient`, `memo`, `events`, and
# `conditions`, and its Makefile fails when that build links client-go.
# PLAIN_K8S lists every k8s.io package that these four packages depend
# on, and `test` fails when the list is not empty.
#
# media-operator's pod build and equipment-operator's node build import
# `informer`, which needs only client-go's reflector, dynamic client, and
# rest packages. It must not depend on the typed clientset, the informer
# factories, or leader election, because they are large, and
# INFORMER_HEAVY lists any of them that it does depend on. Leader
# election is in `election`, which only an operator that elects a leader
# imports.
PLAIN_K8S := go list -deps ./apiclient ./memo ./events ./conditions | grep '^k8s.io/'
INFORMER_HEAVY := go list -deps ./informer | grep -E '^k8s.io/client-go/(kubernetes|informers|dynamic/dynamicinformer|tools/leaderelection)(/|$$)'

.PHONY: test
test:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	test -z "$$($(PLAIN_K8S))" || { echo 'apiclient, memo, events, or conditions depends on these k8s.io packages, want none:'; $(PLAIN_K8S); exit 1; }
	test -z "$$($(INFORMER_HEAVY))" || { echo 'informer depends on these client-go packages, want none of the typed clientset, the informer factories, or leader election:'; $(INFORMER_HEAVY); exit 1; }
	go vet ./...
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml

# Writes the coverage profile from `test` as one HTML page, which CI
# publishes on the site at coverage/kubernetes.html. `test` does not
# depend on this target, because the coverage gate decides whether a
# change passes and the page is only for reading. The page generator is
# `coverage` in the `brand` module, so the recipe runs it from there.
.PHONY: coverage-report
coverage-report: coverage.out
	cd ../brand && go run ./coverage -title kubernetes -root ../kubernetes \
		-label Go -out ../kubernetes/coverage.html ../kubernetes/coverage.out
