# Builds the manual for liken.sh/media/. The pages that GENERATED lists
# are generated, so edit their sources and not the pages. crdref, from
# brand/, renders each CRD page from the CRD in deploy/ and a
# hand-written preamble and postamble in crdref/. The schema
# descriptions become the field tables, so editing a schema edits the
# manual. apiref renders the route reference from openapi.json. go.mod
# lists crdref, apiref, and Hugo as tools.
HUGO := go tool hugo
CRDREF := go tool crdref
APIREF := go tool apiref

# A failed or killed recipe leaves a partly written target that is newer
# than its inputs. make deletes it, so the next build writes it again.
.DELETE_ON_ERROR:

# go.mod takes crdref and apiref from brand/ through a replace
# directive, so an edit to their sources does not change go.mod. Each
# page therefore also depends on its generator's sources.
BRAND := ../../brand
CRDREF_SOURCES := go.mod $(BRAND)/go.mod \
	$(filter-out %_test.go,$(wildcard $(BRAND)/crdref/*.go $(BRAND)/linkcheck/*.go))
APIREF_SOURCES := go.mod $(BRAND)/go.mod \
	$(filter-out %_test.go,$(wildcard $(BRAND)/apiref/*.go $(BRAND)/linkcheck/*.go))

REFERENCE := content/docs/reference

# The program embeds this OpenAPI document. `go generate ./...` writes
# it from the router.
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

# The test-docs target in ../Makefile runs this with -B and fails when
# a page differs from what git has staged.
.PHONY: generate
generate: $(GENERATED)

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# Checks that every internal link resolves (manual_test.go lists the
# exceptions). It depends on the generated pages, so make writes any
# stale page before the test reads it.
.PHONY: test
test: $(GENERATED)
	go test ./...

# Writes each guide as an Agent Skill, one SKILL.md per guide, into
# ../skills (brand/README.md describes the output). The output is
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

# The route reference has one section for each route in the OpenAPI
# document. The hand-written API page beside it explains the API.
$(REFERENCE)/routes.md: $(OPENAPI) apiref/routes-preamble.md apiref/routes-postamble.md $(APIREF_SOURCES)
	$(APIREF) -title Routes -weight 66 -postamble apiref/routes-postamble.md $< $@ apiref/routes-preamble.md
