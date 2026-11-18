# Builds the manual served at liken.sh/bluetooth. The CRD reference
# pages are generated, never edited by hand: crdref, from brand/,
# renders each page from a hand-written preamble and the kind's schema
# in deploy/crds.yaml, so editing a schema description edits the
# manual.
HUGO := go tool hugo
CRDREF := go tool crdref

# A recipe that fails or is killed can leave a partly written target
# newer than its inputs. make deletes it, so the next build makes it
# again.
.DELETE_ON_ERROR:

# go.mod reaches crdref in brand/ through a replace directive, so an
# edit to the generator's sources never changes go.mod. Each generated
# page lists those sources and go.mod as inputs, so an edit to either
# regenerates the page.
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

# test-docs in ../Makefile runs this target with -B and fails when a
# regenerated page differs from what git has staged.
.PHONY: generate
generate: $(GENERATED)

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The site's tests check that every internal link resolves, with the
# exceptions that manual_test.go passes to the link check.
.PHONY: test
test:
	go test ./...

# Writes each guide as an Agent Skill, a SKILL.md in ../skills, with
# the skills generator from brand/. The output is committed, and
# test-docs in ../Makefile fails when it is out of date.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills

$(REFERENCE)/adapters.md: ../deploy/crds.yaml crdref/adapter-preamble.md $(CRDREF_SOURCES)
	$(CRDREF) -kind Adapter -weight 10 $< $@ crdref/adapter-preamble.md

$(REFERENCE)/peripherals.md: ../deploy/crds.yaml crdref/peripheral-preamble.md $(CRDREF_SOURCES)
	$(CRDREF) -kind Peripheral -weight 15 $< $@ crdref/peripheral-preamble.md

$(REFERENCE)/pairingrequests.md: ../deploy/crds.yaml crdref/pairingrequest-preamble.md $(CRDREF_SOURCES)
	$(CRDREF) -kind PairingRequest -weight 20 $< $@ crdref/pairingrequest-preamble.md
