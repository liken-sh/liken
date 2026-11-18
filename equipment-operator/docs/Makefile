# Builds the manual served at liken.sh/equipment. Do not edit the pages
# in GENERATED: crdref, from the brand component, writes each one from
# its CRD in ../deploy.
HUGO := go tool hugo
CRDREF := go tool crdref

# A recipe that fails or is killed can leave a partly written target
# that is newer than its inputs. make deletes it, so the next build
# writes it again.
.DELETE_ON_ERROR:

# go.mod replaces the crdref module with its source in brand/, so an
# edit to crdref does not change go.mod. Each generated page depends on
# crdref's source files and on both go.mod files, so an edit to any of
# them regenerates the page.
BRAND := ../../brand
CRDREF_SOURCES := go.mod $(BRAND)/go.mod \
	$(filter-out %_test.go,$(wildcard $(BRAND)/crdref/*.go $(BRAND)/linkcheck/*.go))

REFERENCE := content/docs/reference

GENERATED := $(REFERENCE)/receivers.md $(REFERENCE)/cecbuses.md $(REFERENCE)/televisions.md

SKILLS := go tool skills
BASE := https://liken.sh/equipment/

# The test-docs target in ../Makefile runs this with -B and fails when
# a regenerated page differs from the committed one.
.PHONY: generate
generate: $(GENERATED)

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# manual_test.go fails when a link in the manual points to a page or a
# heading that does not exist.
.PHONY: test
test: $(GENERATED)
	go test ./...

$(REFERENCE)/receivers.md: ../deploy/receivers-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Receiver -weight 10 $< $@

$(REFERENCE)/cecbuses.md: ../deploy/cecbuses-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title CECBus -weight 15 $< $@

$(REFERENCE)/televisions.md: ../deploy/televisions-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Television -weight 16 $< $@

# Writes each guide as an Agent Skill, one SKILL.md in ../skills, with
# the skills generator from the brand component. The output is
# committed, and the test-docs target in ../Makefile fails when it is
# out of date.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills
