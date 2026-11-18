# Builds the manual at liken.sh/astrophotography/. crdref, brand's
# generator, writes the reference page of each resource from its CRD in
# ../deploy/, so change the CRD's schema, not the page.
HUGO := go tool hugo
CRDREF := go tool crdref

# make deletes the partly written target of a failed recipe, so the
# next build does not treat it as current.
.DELETE_ON_ERROR:

# go.mod takes crdref from brand/ through a replace directive, so an
# edit to crdref's sources does not change go.mod. Each generated page
# depends on those sources and on both go.mod files, so an edit to the
# generator regenerates the pages.
BRAND := ../../brand
CRDREF_SOURCES := go.mod $(BRAND)/go.mod \
	$(filter-out %_test.go,$(wildcard $(BRAND)/crdref/*.go $(BRAND)/linkcheck/*.go))

REFERENCE := content/docs/reference

# The weights put the generated pages after the hand-written reference
# pages, with the `Target` page before the `Solver` page.
GENERATED := $(REFERENCE)/targets.md \
	$(REFERENCE)/solvers.md

SKILLS := go tool skills
BASE := https://liken.sh/astrophotography/

# test-docs in ../Makefile runs this target with -B, which regenerates
# every page, and fails when a page differs from what git has staged.
.PHONY: generate
generate: $(GENERATED)

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# Checks that every absolute link in the manual points to a page and a
# heading that exist.
.PHONY: test
test: $(GENERATED)
	go test ./...

$(REFERENCE)/targets.md: ../deploy/targets-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Target -weight 100 $< $@

$(REFERENCE)/solvers.md: ../deploy/solvers-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Solver -weight 105 $< $@

# Writes each guide as an Agent Skill, one SKILL.md in ../skills, with
# brand's skills generator. The output is committed, and test-docs in
# ../Makefile fails when it is stale.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills
