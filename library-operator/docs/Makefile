# The docs domain builds the manual for library.liken.sh. The
# reference pages are generated, never edited: crdref, the generator
# the brand component publishes, renders one page from each CRD
# schema in deploy/, so a schema edit is a manual edit. GENERATED
# lists those pages, and each rule names the generator's sources as
# inputs, so a change to the generator regenerates every page.
HUGO := go tool hugo
CRDREF := go tool crdref

# crdref comes from brand/ through the replace directive in go.mod, so
# an edit to its sources never changes go.mod. Each generated page
# names those sources as inputs, and go.mod for the generator's own
# dependencies, so an edit to either one regenerates the page.
BRAND := ../../brand
CRDREF_SOURCES := go.mod $(BRAND)/go.mod \
	$(filter-out %_test.go,$(wildcard $(BRAND)/crdref/*.go $(BRAND)/linkcheck/*.go))

REFERENCE := content/docs/reference

GENERATED := $(REFERENCE)/libraries.md $(REFERENCE)/catalogs.md $(REFERENCE)/metadataproviders.md

SKILLS := go tool skills
BASE := https://liken.sh/library/

# The generate target builds every generated reference page and
# nothing else. The test-docs target in ../Makefile runs it with -B,
# so that every page is regenerated, and fails when one differs from
# what git has staged.
.PHONY: generate
generate: $(GENERATED)

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The site's own tests: every internal link resolves before the build (manual_test.go passes this site's exceptions).
.PHONY: test
test: $(GENERATED)
	go test ./...

# The skills target emits every guide as an Agent Skill into ../skills,
# one SKILL.md per guide, with the generator the brand component
# publishes (its README says what the output holds). The output is
# committed, and the test-docs target in ../Makefile fails when it is
# stale.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills

$(REFERENCE)/libraries.md: ../deploy/libraries-crd.yaml crdref/libraries-preamble.md crdref/libraries-postamble.md $(CRDREF_SOURCES)
	$(CRDREF) -title Libraries -weight 10 -postamble crdref/libraries-postamble.md $< $@ crdref/libraries-preamble.md

$(REFERENCE)/catalogs.md: ../deploy/catalogs-crd.yaml crdref/catalogs-preamble.md $(CRDREF_SOURCES)
	$(CRDREF) -title Catalogs -weight 20 $< $@ crdref/catalogs-preamble.md

$(REFERENCE)/metadataproviders.md: ../deploy/metadataproviders-crd.yaml crdref/metadataproviders-preamble.md $(CRDREF_SOURCES)
	$(CRDREF) -title "Metadata providers" -weight 30 $< $@ crdref/metadataproviders-preamble.md
