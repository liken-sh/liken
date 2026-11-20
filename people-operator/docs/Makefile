# This Makefile builds the manual served at liken.sh/people. crdref,
# from brand/, generates the reference page from deploy/people-crd.yaml.
# Do not edit that page by hand.
HUGO := go tool hugo
CRDREF := go tool crdref

# A recipe that fails can leave a half-written target that is newer
# than its inputs, which make would then treat as up to date.
.DELETE_ON_ERROR:

# crdref is built from brand/ through a replace directive, so an edit
# to its source does not change go.mod. The reference page lists
# crdref's source files and both go.mod files as prerequisites, so an
# edit to any of them regenerates the page.
BRAND := ../../brand
CRDREF_SOURCES := go.mod $(BRAND)/go.mod \
	$(filter-out %_test.go,$(wildcard $(BRAND)/crdref/*.go $(BRAND)/linkcheck/*.go))

REFERENCE := content/docs/reference

GENERATED := $(REFERENCE)/people.md

SKILLS := go tool skills
BASE := https://liken.sh/people/

# test-docs in ../Makefile runs generate with -B and fails when a page
# differs from the git index.
.PHONY: generate
generate: $(GENERATED)

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# manual_test.go fails on an internal link that points at no page or
# heading. Its exceptions are the links to the files that the deploy/
# mount in hugo.yaml serves.
.PHONY: test
test: $(GENERATED)
	go test ./...

$(REFERENCE)/people.md: ../deploy/people-crd.yaml $(CRDREF_SOURCES)
	$(CRDREF) -title Person -weight 10 $< $@

# skills writes each guide as an Agent Skill, one SKILL.md per guide,
# into ../skills. The generator is in brand/skills, and its main.go
# describes the output.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills
