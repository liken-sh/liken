# The docs domain builds the manual for audio.liken.sh, with the
# same `make build` every operator repository runs, so the docs
# workflows stay one pattern. The build generates two reference
# pages, for the Sink and Source CRDs, through crdref, the generator
# the brand repository publishes, pinned as a tool of this module the
# way Hugo is.
HUGO := go tool hugo
CRDREF := go tool crdref

REFERENCE := content/docs/reference

GENERATED := \
	$(REFERENCE)/sinks.md \
	$(REFERENCE)/sources.md

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

# Both CRDs are in one file, and crdref picks the document whose
# spec.names.kind matches the -kind it is given.
$(REFERENCE)/sinks.md: ../deploy/crds.yaml crdref/sinks-preamble.md crdref/sinks-postamble.md go.mod
	$(CRDREF) -kind Sink -title Sinks -weight 20 -postamble crdref/sinks-postamble.md $< $@ crdref/sinks-preamble.md

$(REFERENCE)/sources.md: ../deploy/crds.yaml crdref/sources-preamble.md crdref/sources-postamble.md go.mod
	$(CRDREF) -kind Source -title Sources -weight 30 -postamble crdref/sources-postamble.md $< $@ crdref/sources-preamble.md
