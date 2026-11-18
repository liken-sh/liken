# This Makefile builds firmware-operator's manual, published at
# liken.sh/firmware. Do not edit the reference pages by hand. crdref,
# from brand/, generates each one from its CRD in ../deploy, so change
# the CRD's schema instead.
HUGO := go tool hugo
CRDREF := go tool crdref

# When a recipe fails or is killed, make deletes the partly written
# target. Otherwise the next build would see it as up to date.
.DELETE_ON_ERROR:

# go.mod reaches crdref through a replace directive to brand/, so an
# edit to crdref does not change go.mod. So each page also depends on
# crdref's sources, and an edit to them regenerates the pages.
BRAND := ../../brand
CRDREF_SOURCES := go.mod $(BRAND)/go.mod \
	$(filter-out %_test.go,$(wildcard $(BRAND)/crdref/*.go $(BRAND)/linkcheck/*.go))

REFERENCE := content/docs/reference

GENERATED := $(REFERENCE)/firmware.md $(REFERENCE)/firmwarecatalog.md

SKILLS := go tool skills
BASE := https://liken.sh/firmware/

# generate writes only the reference pages. test-docs in ../Makefile
# runs it with -B and fails when a page differs from what git has
# staged.
.PHONY: generate
generate: $(GENERATED)

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The test checks that every internal link of the site resolves.
# manual_test.go lists the links that no content file provides.
.PHONY: test
test: $(GENERATED)
	go test ./...

$(REFERENCE)/firmware.md: ../deploy/firmware-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Firmware -weight 10 $< $@

$(REFERENCE)/firmwarecatalog.md: ../deploy/firmwarecatalog-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title FirmwareCatalog -weight 20 $< $@

# skills writes each guide as an Agent Skill, one SKILL.md in ../skills
# per guide. The generator is brand/skills, and brand/README.md
# describes the output. The output is committed, and test-docs in ../Makefile
# fails when it is out of date.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills
