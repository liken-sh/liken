# The docs domain builds the manual served at liken.sh/display, with the same
# `make build` every manual runs, so the root Makefile and CI build each
# manual one way. The build generates a reference page for each CRD, the
# Display and the Layout, through crdref, and the route reference from
# the API's own OpenAPI document, through apiref. Both generators come
# from the brand component, as tools of this module the way Hugo is.
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
OPENAPI := $(REFERENCE)/openapi.json

GENERATED := \
	$(REFERENCE)/displays.md \
	$(REFERENCE)/layouts.md \
	$(REFERENCE)/routes.md \
	$(OPENAPI)

SKILLS := go tool skills
BASE := https://liken.sh/display/

# A generated file whose command failed is removed, so a failed build
# never leaves a half-written page in the tree for the next build to
# accept.
.DELETE_ON_ERROR:

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

$(REFERENCE)/displays.md: ../deploy/displays.yaml crdref/displays-preamble.md crdref/displays-postamble.md $(CRDREF_SOURCES)
	$(CRDREF) -title Displays -weight 20 -postamble crdref/displays-postamble.md $< $@ crdref/displays-preamble.md

# The API's description is generated from the router itself, by the
# operator's own binary, so a route the program serves cannot be
# missing from the page that describes it.
$(OPENAPI): $(wildcard ../*.go) ../go.mod
	cd .. && go run . openapi > docs/$@

# The route reference is that same document as a page: one section
# per route, with its parameters, its answers, and the fields they
# carry. The hand-written API page beside it holds the narrative,
# which no document can generate.
$(REFERENCE)/routes.md: $(OPENAPI) apiref/routes-preamble.md apiref/routes-postamble.md $(APIREF_SOURCES)
	$(APIREF) -title Routes -weight 55 -postamble apiref/routes-postamble.md $< $@ apiref/routes-preamble.md

# The Layout page carries no postamble: every rule a reader needs is
# in the schema's own descriptions, and the guide holds the worked
# example.
$(REFERENCE)/layouts.md: ../deploy/layouts.yaml crdref/layouts-preamble.md $(CRDREF_SOURCES)
	$(CRDREF) -title Layouts -weight 30 $< $@ crdref/layouts-preamble.md
