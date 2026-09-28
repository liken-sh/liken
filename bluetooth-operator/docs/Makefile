# The docs domain builds the manual for bluetooth.liken.sh. The three
# CRD reference pages are generated, never edited: crdref, the
# generator the brand repository publishes, renders each page from
# its schema in deploy/crds.yaml and a hand-written preamble that
# opens it. The schema descriptions become the field tables, so a
# schema edit is a manual edit. The three CRDs travel in one manifest
# file, so each rule selects its kind. go.mod pins the generator the
# way it pins Hugo, as a tool directive, and each rule lists go.mod
# as an input, so a generator bump regenerates every page.
HUGO := go tool hugo
CRDREF := go tool crdref

REFERENCE := content/docs/reference

GENERATED := \
	$(REFERENCE)/adapters.md \
	$(REFERENCE)/peripherals.md \
	$(REFERENCE)/pairingrequests.md

SKILLS := go tool skills
BASE := https://bluetooth.liken.sh

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The site's own tests: every internal link resolves before the build
# (manual_test.go passes this site's exceptions).
.PHONY: test
test:
	go test ./...

# The skills target emits every guide as an Agent Skill into ../skills,
# one SKILL.md per guide, with the generator the brand repository
# publishes (its README says what the output holds). The output is
# committed, and the root test-docs target fails when it is stale.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills

$(REFERENCE)/adapters.md: ../deploy/crds.yaml crdref/adapter-preamble.md go.mod
	$(CRDREF) -kind Adapter -weight 10 $< $@ crdref/adapter-preamble.md

$(REFERENCE)/peripherals.md: ../deploy/crds.yaml crdref/peripheral-preamble.md go.mod
	$(CRDREF) -kind Peripheral -weight 15 $< $@ crdref/peripheral-preamble.md

$(REFERENCE)/pairingrequests.md: ../deploy/crds.yaml crdref/pairingrequest-preamble.md go.mod
	$(CRDREF) -kind PairingRequest -weight 20 $< $@ crdref/pairingrequest-preamble.md
