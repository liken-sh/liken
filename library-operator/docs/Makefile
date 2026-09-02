# The docs domain builds the manual for library.liken.sh. The
# reference pages are generated, never edited: crdref, the generator
# the brand repository publishes, renders one page from each CRD
# schema in deploy/, so a schema edit is a manual edit. GENERATED
# lists those pages, and each rule names go.mod as an input, so a
# generator bump regenerates every page.
HUGO := go tool hugo
CRDREF := go tool crdref

REFERENCE := content/docs/reference

GENERATED := $(REFERENCE)/libraries.md $(REFERENCE)/catalogs.md $(REFERENCE)/metadataproviders.md

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The site's own tests: every internal link resolves before the build (manual_test.go passes this site's exceptions).
.PHONY: test
test:
	go test ./...

$(REFERENCE)/libraries.md: ../deploy/libraries-crd.yaml crdref/libraries-preamble.md crdref/libraries-postamble.md go.mod
	$(CRDREF) -title Libraries -weight 10 -postamble crdref/libraries-postamble.md $< $@ crdref/libraries-preamble.md

$(REFERENCE)/catalogs.md: ../deploy/catalogs-crd.yaml crdref/catalogs-preamble.md go.mod
	$(CRDREF) -title Catalogs -weight 20 $< $@ crdref/catalogs-preamble.md

$(REFERENCE)/metadataproviders.md: ../deploy/metadataproviders-crd.yaml crdref/metadataproviders-preamble.md go.mod
	$(CRDREF) -title "Metadata providers" -weight 30 $< $@ crdref/metadataproviders-preamble.md
