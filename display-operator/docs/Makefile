# Builds the manual served at liken.sh/display. Every manual has the
# same `make build` target, so the root Makefile and CI build each
# manual the same way. crdref generates a reference page for each CRD,
# and apiref generates the route reference from the API's OpenAPI
# document. Both come from the brand component and run as Go tools of
# this module, like Hugo.
HUGO := go tool hugo
CRDREF := go tool crdref
APIREF := go tool apiref

# A replace directive in go.mod points crdref and apiref at brand/, so
# an edit to their sources does not change go.mod. So each generated
# page depends on its generator's source files, and on go.mod, which
# pins the generator's dependencies.
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

# Delete a target whose command failed, so the next build does not
# take the half-written file as up to date.
.DELETE_ON_ERROR:

# The test-docs target in ../Makefile runs this with -B to regenerate
# every page, and fails when a page differs from what git has staged.
.PHONY: generate
generate: $(GENERATED)

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The tests include a check that every internal link resolves, with
# the exceptions in manual_test.go. The dependency on the generated
# pages makes that check read freshly generated pages instead of the
# committed ones.
.PHONY: test
test: $(GENERATED)
	go test ./...

# Writes each guide as an Agent Skill, a SKILL.md in ../skills, with
# the skills generator from the brand component, whose README
# describes the output. The output is committed, and the test-docs
# target in ../Makefile fails when it is out of date.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills

$(REFERENCE)/displays.md: ../deploy/displays.yaml crdref/displays-preamble.md crdref/displays-postamble.md $(CRDREF_SOURCES)
	$(CRDREF) -title Displays -weight 20 -postamble crdref/displays-postamble.md $< $@ crdref/displays-preamble.md

# The operator's binary generates the OpenAPI document from its own
# router, so the document lists every route that the program serves.
$(OPENAPI): $(wildcard ../*.go) ../go.mod
	cd .. && go run . openapi > docs/$@

# The route reference has a section for each route of the OpenAPI
# document, with its parameters, its responses, and their fields. The
# hand-written API page beside it explains the API.
$(REFERENCE)/routes.md: $(OPENAPI) apiref/routes-preamble.md apiref/routes-postamble.md $(APIREF_SOURCES)
	$(APIREF) -title Routes -weight 55 -postamble apiref/routes-postamble.md $< $@ apiref/routes-preamble.md

# The Layout page has no postamble, because the schema's descriptions
# give every rule, and the guide has the worked example.
$(REFERENCE)/layouts.md: ../deploy/layouts.yaml crdref/layouts-preamble.md $(CRDREF_SOURCES)
	$(CRDREF) -title Layouts -weight 30 $< $@ crdref/layouts-preamble.md
