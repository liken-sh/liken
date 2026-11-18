# CI runs `make test`, the same target that you run on a workstation.
# One `go test` run does the race check and writes the coverage
# profile, because -race works with -covermode=atomic.

# Delete a target whose recipe fails, so a partly written file is not
# taken as up to date on the next run.
.DELETE_ON_ERROR:

.PHONY: test
test:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	go vet ./...
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml
