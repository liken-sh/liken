# The checks of the program that reads every package.toml. CI runs
# `make test`, the same target a workstation runs.
#
# One run of the tests gives both the race check and the coverage
# profile, because -race with -covermode=atomic does both.

# A recipe that fails, or a build that is killed during one, leaves its
# target partly written and newer than its inputs. make deletes that
# target, so the next build makes it again and does not take it as
# current.
.DELETE_ON_ERROR:

.PHONY: test
test:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	go vet ./...
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml
