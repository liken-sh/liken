# The docs domain builds the manual for people.liken.sh. The
# reference page is generated, never edited: crdref, the generator
# the brand repository publishes, renders it from deploy/people-crd.yaml,
# so a schema edit is a manual edit. GENERATED lists that page, and
# the rule names go.mod as an input, so a generator bump regenerates
# it.
HUGO := go tool hugo
CRDREF := go tool crdref

REFERENCE := content/docs/reference

GENERATED := $(REFERENCE)/people.md

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The site's own tests: every internal link resolves before the build (manual_test.go passes this site's exceptions).
.PHONY: test
test: $(GENERATED)
	go test ./...

$(REFERENCE)/people.md: ../deploy/people-crd.yaml go.mod
	$(CRDREF) -title Person -weight 10 $< $@
