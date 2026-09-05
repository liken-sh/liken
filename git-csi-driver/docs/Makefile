# The docs domain builds the manual for git.liken.sh, with the
# same `make build` every operator repository runs, so the docs
# workflows stay one pattern.
HUGO := go tool hugo

.PHONY: build
build:
	$(HUGO) --destination dist/site

# The site's own tests: every internal link resolves before the build
# (manual_test.go passes this site's exceptions).
.PHONY: test
test:
	go test ./...
