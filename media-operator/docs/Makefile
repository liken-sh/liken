# The docs domain builds the manual for media.liken.sh. Five of the
# reference pages are generated, never edited: crdref, the generator
# the brand component publishes, renders each page from its CRD
# schema in deploy/, a hand-written preamble that opens the page, and
# a hand-written postamble that carries its bus sections. The schema
# descriptions become the field tables, so a schema edit is a manual
# edit. The sixth generated page is the route reference, which apiref
# renders from the API's own OpenAPI document. go.mod names both
# generators as tools, the way it names Hugo, and each rule lists its
# generator's sources as inputs, so a change to a generator
# regenerates its pages.
HUGO := go tool hugo
CRDREF := go tool crdref
APIREF := go tool apiref

# crdref and apiref come from brand/ through the replace directive in
# go.mod, so an edit to their sources never changes go.mod. Each
# generated page names its generator's sources as inputs, and go.mod
# for the generator's own dependencies, so an edit to either one
# regenerates the page.
BRAND := ../../brand
CRDREF_SOURCES := go.mod $(BRAND)/go.mod \
	$(filter-out %_test.go,$(wildcard $(BRAND)/crdref/*.go $(BRAND)/linkcheck/*.go))
APIREF_SOURCES := go.mod $(BRAND)/go.mod \
	$(filter-out %_test.go,$(wildcard $(BRAND)/apiref/*.go $(BRAND)/linkcheck/*.go))

REFERENCE := content/docs/reference

# The OpenAPI document is committed at the top of media-operator/, where the
# program embeds it. `go generate ./...` is the one command that
# rewrites it, from the router.
OPENAPI := ../openapi.json

GENERATED := \
	$(REFERENCE)/players.md \
	$(REFERENCE)/plays.md \
	$(REFERENCE)/remotes.md \
	$(REFERENCE)/keymaps.md \
	$(REFERENCE)/mediapreferences.md \
	$(REFERENCE)/routes.md

SKILLS := go tool skills
BASE := https://liken.sh/media/

# The generate target builds every generated reference page and
# nothing else. The test-docs target in ../Makefile runs it with -B,
# so that every page is regenerated, and fails when one differs from
# what is committed.
.PHONY: generate
generate: $(GENERATED)

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The site's own tests: every internal link resolves before the build
# (manual_test.go passes this site's exceptions). Depending on the
# generated pages means the link test checks freshly generated
# output, not whatever was last committed.
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

$(REFERENCE)/players.md: ../deploy/players-crd.yaml crdref/players-preamble.md crdref/players-postamble.md $(CRDREF_SOURCES)
	$(CRDREF) -title Players -weight 10 -postamble crdref/players-postamble.md $< $@ crdref/players-preamble.md

$(REFERENCE)/plays.md: ../deploy/plays-crd.yaml crdref/plays-preamble.md crdref/plays-postamble.md $(CRDREF_SOURCES)
	$(CRDREF) -title Plays -weight 20 -postamble crdref/plays-postamble.md $< $@ crdref/plays-preamble.md

$(REFERENCE)/remotes.md: ../deploy/remotes-crd.yaml crdref/remotes-preamble.md crdref/remotes-postamble.md $(CRDREF_SOURCES)
	$(CRDREF) -title Remotes -weight 30 -postamble crdref/remotes-postamble.md $< $@ crdref/remotes-preamble.md

$(REFERENCE)/keymaps.md: ../deploy/keymaps-crd.yaml crdref/keymaps-preamble.md crdref/keymaps-postamble.md $(CRDREF_SOURCES)
	$(CRDREF) -title Keymaps -weight 40 -postamble crdref/keymaps-postamble.md $< $@ crdref/keymaps-preamble.md

$(REFERENCE)/mediapreferences.md: ../deploy/mediapreferences-crd.yaml crdref/mediapreferences-preamble.md crdref/mediapreferences-postamble.md $(CRDREF_SOURCES)
	$(CRDREF) -title MediaPreferences -weight 50 -postamble crdref/mediapreferences-postamble.md $< $@ crdref/mediapreferences-preamble.md

# The route reference is the OpenAPI document as a page: one section
# per route, with its parameters, its answers, and the fields they
# carry. The hand-written API page beside it holds the narrative,
# which no document can generate.
$(REFERENCE)/routes.md: $(OPENAPI) apiref/routes-preamble.md apiref/routes-postamble.md $(APIREF_SOURCES)
	$(APIREF) -title Routes -weight 66 -postamble apiref/routes-postamble.md $< $@ apiref/routes-preamble.md
