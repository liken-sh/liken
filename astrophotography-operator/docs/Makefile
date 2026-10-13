# The docs domain builds the manual at liken.sh/astrophotography/. The
# reference page of each resource is generated, never edited: crdref,
# the generator the brand component publishes, renders each one from
# its CRD in deploy/, so a schema edit is a manual edit. GENERATED
# lists those pages, and each rule names the generator's sources as
# inputs, so a change to the generator regenerates them.
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

# The weights put the resource pages after the hand-written reference
# pages: the Target, then the Solver it names.
GENERATED := $(REFERENCE)/targets.md \
	$(REFERENCE)/solvers.md

SKILLS := go tool skills
BASE := https://liken.sh/astrophotography/

# The generate target builds every generated reference page and
# nothing else. The test-docs target in ../Makefile runs it with -B,
# so that every page is regenerated, and fails when one differs from
# what git has staged.
.PHONY: generate
generate: $(GENERATED)

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The site's own test: every internal link resolves before the build.
# manual_test.go names the links that no content file answers for.
.PHONY: test
test: $(GENERATED)
	go test ./...

$(REFERENCE)/targets.md: ../deploy/targets-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Target -weight 100 $< $@

$(REFERENCE)/solvers.md: ../deploy/solvers-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Solver -weight 105 $< $@

# The skills target emits every guide as an Agent Skill into ../skills,
# one SKILL.md per guide, with the generator the brand component
# publishes (its README says what the output holds). The output is
# committed, and the test-docs target in ../Makefile fails when it is
# stale.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills
