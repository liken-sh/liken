# The docs domain builds the manual for library.liken.sh. The
# reference pages are generated, never edited: crdref, the generator
# the brand repository publishes, renders one page from each CRD
# schema in deploy/, so a schema edit is a manual edit. GENERATED
# lists those pages, and each rule names go.mod as an input, so a
# generator bump regenerates every page. The list is empty until a
# plan adds the first resource.
HUGO := go tool hugo
CRDREF := go tool crdref

REFERENCE := content/docs/reference

GENERATED :=

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The site's own tests: every internal link resolves before the build (manual_test.go passes this site's exceptions).
.PHONY: test
test:
	go test ./...

# A generated page takes this shape, with its schema, its preamble,
# its postamble, and go.mod as inputs:
#
#   $(REFERENCE)/libraries.md: ../deploy/libraries-crd.yaml crdref/libraries-preamble.md crdref/libraries-postamble.md go.mod
#   	$(CRDREF) -title Libraries -weight 10 -postamble crdref/libraries-postamble.md $< $@ crdref/libraries-preamble.md
