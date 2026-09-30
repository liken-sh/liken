# The checks of the shared module. CI runs `make test`, the same target
# a workstation runs.
#
# One run of the tests gives both the race check and the coverage
# profile, because -race with -covermode=atomic does both.

# A package with no test file writes no rows to the profile, so the
# gate never counts it: its number is not low, it is missing. This
# lists such packages, and test fails on the first one.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

# The pod build of library-operator must not link client-go, and its
# Makefile fails when it does. So apiclient and memo import nothing
# from k8s.io, and a program that must not link client-go can still use
# them. informer links client-go's reflector, its dynamic client, and
# rest. The pod build of media-operator and the node build of
# equipment-operator link informer, and must not link the typed
# clientset, the informer factories, or leader election, which would
# more than double each program. These two lists find each import that
# breaks either rule.
PLAIN_K8S := go list -deps ./apiclient ./memo | grep '^k8s.io/'
INFORMER_HEAVY := go list -deps ./informer | grep -E '^k8s.io/client-go/(kubernetes|informers|dynamic/dynamicinformer|tools/leaderelection)(/|$$)'

.PHONY: test
test:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	test -z "$$($(PLAIN_K8S))" || { echo 'apiclient or memo imports from k8s.io:'; $(PLAIN_K8S); exit 1; }
	test -z "$$($(INFORMER_HEAVY))" || { echo 'informer links more of client-go than a watch needs:'; $(INFORMER_HEAVY); exit 1; }
	go vet ./...
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml

# The coverage report is the profile the gate already measured, drawn
# as one HTML page. This module has no manual, so the site serves the
# page at coverage/kubernetes.html. `test` does not depend on it: the
# gate decides whether a change passes, and the report only shows a
# reader what the tests reached.
#
# coverage is brand's tool. This module has no docs module to pin it
# as a tool, so the command runs it from brand's own module.
.PHONY: coverage-report
coverage-report: coverage.out
	cd ../brand && go run ./coverage -title kubernetes -root ../kubernetes \
		-label Go -out ../kubernetes/coverage.html ../kubernetes/coverage.out
