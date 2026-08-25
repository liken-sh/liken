# The docs domain builds the manual for display.liken.sh, with the
# same `make build` every operator repository runs, so the docs
# workflows stay one pattern. This site generates no pages; the
# build is Hugo alone, pinned as a tool dependency of this domain's
# own Go module.
HUGO := go tool hugo

.PHONY: build
build:
	$(HUGO) --destination dist/site
