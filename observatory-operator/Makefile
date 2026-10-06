# This Makefile names the checks a change must pass. `make test` runs
# every check CI runs, in the same commands, so a change that passes
# here passes there.
#
# The coverage floor is the one number the gate enforces, in
# .testcoverage.yml. CI reads the same file, so the floor moves in one
# place.

.PHONY: test
test: test-go

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

.PHONY: test-go
test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	go vet ./...
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml

# drivers/generated.go maps each driver to the image that holds it, from
# indi/images/ and the tag in indi/package.toml, and holds the tag of
# the weston image from weston/package.toml. A test fails when the file
# is stale; this writes it again.
.PHONY: drivers
drivers:
	go test ./drivers -run TestTheMapIsCurrent -update

# The coverage report is the profile the gate already measured, drawn
# as one HTML page. This module has no manual, so the site serves the
# page at coverage/observatory-operator.html. `test` does not depend on
# it: the gate decides whether a change passes, and the report only
# shows a reader what the tests reached.
#
# coverage is brand's tool. This module has no docs module to pin it
# as a tool, so the command runs it from brand's own module.
.PHONY: coverage-report
coverage-report: coverage.out
	cd ../brand && go run ./coverage -title observatory-operator -root ../observatory-operator \
		-label Go -out ../observatory-operator/coverage.html ../observatory-operator/coverage.out
