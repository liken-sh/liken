# The docs domain builds the manual for display.liken.sh, with the
# same `make build` every operator repository runs, so the docs
# workflows stay one pattern. The build generates one reference page,
# for the Display CRD, through crdref, the generator the brand
# repository publishes, pinned as a tool of this module the way Hugo
# is.
HUGO := go tool hugo
CRDREF := go tool crdref

REFERENCE := content/docs/reference

GENERATED := \
	$(REFERENCE)/displays.md

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The site's own tests: every internal link resolves before the build
# (manual_test.go passes this site's exceptions). Depending on the
# generated pages means the link test checks freshly generated
# output, not whatever was last committed.
.PHONY: test
test: $(GENERATED)
	go test ./...

$(REFERENCE)/displays.md: ../deploy/displays.yaml crdref/displays-preamble.md crdref/displays-postamble.md go.mod
	$(CRDREF) -title Displays -weight 20 -postamble crdref/displays-postamble.md $< $@ crdref/displays-preamble.md
