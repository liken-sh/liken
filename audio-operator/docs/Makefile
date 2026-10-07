# The docs domain builds the manual served at liken.sh/audio, with
# the same `make build` every manual runs, so the root Makefile and CI
# build each manual one way. The build generates two reference pages,
# for the Sink and Source CRDs, through crdref, and the route
# reference from the API's own OpenAPI document, through apiref. Both
# generators come from the brand component, as tools of this module
# the way Hugo is.
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

# The OpenAPI document is committed, under the static tree the site
# publishes it from. `make openapi` in audio-operator/ is the one
# command that rewrites it, from the router table.
OPENAPI := static/v1/audio/openapi.json

GENERATED := \
	$(REFERENCE)/sinks.md \
	$(REFERENCE)/sources.md \
	$(REFERENCE)/routes.md

SKILLS := go tool skills
BASE := https://liken.sh/audio/

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

# Both CRDs are in one file, and crdref picks the document whose
# spec.names.kind matches the -kind it is given.
$(REFERENCE)/sinks.md: ../deploy/crds.yaml crdref/sinks-preamble.md crdref/sinks-postamble.md $(CRDREF_SOURCES)
	$(CRDREF) -kind Sink -title Sinks -weight 20 -postamble crdref/sinks-postamble.md $< $@ crdref/sinks-preamble.md

$(REFERENCE)/sources.md: ../deploy/crds.yaml crdref/sources-preamble.md crdref/sources-postamble.md $(CRDREF_SOURCES)
	$(CRDREF) -kind Source -title Sources -weight 30 -postamble crdref/sources-postamble.md $< $@ crdref/sources-preamble.md

# The route reference is the OpenAPI document as a page: one section
# per route, with its parameters, its answers, and the fields they
# carry. The hand-written API page beside it holds the narrative,
# which no document can generate.
$(REFERENCE)/routes.md: $(OPENAPI) apiref/routes-preamble.md apiref/routes-postamble.md $(APIREF_SOURCES)
	$(APIREF) -title Routes -weight 55 -postamble apiref/routes-postamble.md $< $@ apiref/routes-preamble.md
