# The docs domain builds the manual for display.liken.sh, with the
# same `make build` every operator repository runs, so the docs
# workflows stay one pattern. The build generates a reference page for
# each CRD, the Display and the Layout, through crdref, and the route
# reference from the API's own OpenAPI document, through apiref. Both
# generators come from the brand repository, pinned as tools of this
# module the way Hugo is.
HUGO := go tool hugo
CRDREF := go tool crdref
APIREF := go tool apiref

REFERENCE := content/docs/reference
OPENAPI := $(REFERENCE)/openapi.json

GENERATED := \
	$(REFERENCE)/displays.md \
	$(REFERENCE)/layouts.md \
	$(REFERENCE)/routes.md \
	$(OPENAPI)

SKILLS := go tool skills
BASE := https://display.liken.sh

# A generated file whose command failed is removed, so a failed build
# never leaves a half-written page in the tree for the next build to
# accept.
.DELETE_ON_ERROR:

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
# one SKILL.md per guide, with the generator the brand repository
# publishes (its README says what the output holds). The output is
# committed, and the root test-docs target fails when it is stale.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills

$(REFERENCE)/displays.md: ../deploy/displays.yaml crdref/displays-preamble.md crdref/displays-postamble.md go.mod
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
$(REFERENCE)/routes.md: $(OPENAPI) apiref/routes-preamble.md apiref/routes-postamble.md go.mod
	$(APIREF) -title Routes -weight 55 -postamble apiref/routes-postamble.md $< $@ apiref/routes-preamble.md

# The Layout page carries no postamble: every rule a reader needs is
# in the schema's own descriptions, and the guide holds the worked
# example.
$(REFERENCE)/layouts.md: ../deploy/layouts.yaml crdref/layouts-preamble.md go.mod
	$(CRDREF) -title Layouts -weight 30 $< $@ crdref/layouts-preamble.md
