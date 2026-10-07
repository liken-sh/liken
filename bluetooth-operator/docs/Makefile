# The docs domain builds the manual served at liken.sh/bluetooth. The three
# CRD reference pages are generated, never edited: crdref, the
# generator the brand component publishes, renders each page from
# its schema in deploy/crds.yaml and a hand-written preamble that
# opens it. The schema descriptions become the field tables, so a
# schema edit is a manual edit. The three CRDs travel in one manifest
# file, so each rule selects its kind. go.mod names the generator as
# a tool, the way it names Hugo, and each rule lists the generator's
# sources as inputs, so a change to the generator regenerates every
# page.
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

GENERATED := \
	$(REFERENCE)/adapters.md \
	$(REFERENCE)/peripherals.md \
	$(REFERENCE)/pairingrequests.md

SKILLS := go tool skills
BASE := https://liken.sh/bluetooth/

# The generate target builds every generated reference page and
# nothing else. The test-docs target in ../Makefile runs it with -B,
# so that every page is regenerated, and fails when one differs from
# what git has staged.
.PHONY: generate
generate: $(GENERATED)

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The site's own tests: every internal link resolves before the build
# (manual_test.go passes this site's exceptions).
.PHONY: test
test:
	go test ./...

# The skills target emits every guide as an Agent Skill into ../skills,
# one SKILL.md per guide, with the generator the brand component
# publishes (its README says what the output holds). The output is
# committed, and the test-docs target in ../Makefile fails when it is
# stale.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills

$(REFERENCE)/adapters.md: ../deploy/crds.yaml crdref/adapter-preamble.md $(CRDREF_SOURCES)
	$(CRDREF) -kind Adapter -weight 10 $< $@ crdref/adapter-preamble.md

$(REFERENCE)/peripherals.md: ../deploy/crds.yaml crdref/peripheral-preamble.md $(CRDREF_SOURCES)
	$(CRDREF) -kind Peripheral -weight 15 $< $@ crdref/peripheral-preamble.md

$(REFERENCE)/pairingrequests.md: ../deploy/crds.yaml crdref/pairingrequest-preamble.md $(CRDREF_SOURCES)
	$(CRDREF) -kind PairingRequest -weight 20 $< $@ crdref/pairingrequest-preamble.md
