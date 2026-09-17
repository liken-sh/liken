# The docs domain builds the manual for audio.liken.sh, with the
# same `make build` every operator repository runs, so the docs
# workflows stay one pattern. The build generates two reference
# pages, for the Sink and Source CRDs, through crdref, and the route
# reference from the API's own OpenAPI document, through apiref. Both
# generators come from the brand repository, pinned as tools of this
# module the way Hugo is.
HUGO := go tool hugo
CRDREF := go tool crdref
APIREF := go tool apiref

REFERENCE := content/docs/reference

# The OpenAPI document is committed, under the static tree the site
# publishes it from. `make openapi` in the repository root is the one
# command that rewrites it, from the router table.
OPENAPI := static/v1/audio/openapi.json

GENERATED := \
	$(REFERENCE)/sinks.md \
	$(REFERENCE)/sources.md \
	$(REFERENCE)/routes.md

SKILLS := go tool skills
BASE := https://audio.liken.sh

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

# Both CRDs are in one file, and crdref picks the document whose
# spec.names.kind matches the -kind it is given.
$(REFERENCE)/sinks.md: ../deploy/crds.yaml crdref/sinks-preamble.md crdref/sinks-postamble.md go.mod
	$(CRDREF) -kind Sink -title Sinks -weight 20 -postamble crdref/sinks-postamble.md $< $@ crdref/sinks-preamble.md

$(REFERENCE)/sources.md: ../deploy/crds.yaml crdref/sources-preamble.md crdref/sources-postamble.md go.mod
	$(CRDREF) -kind Source -title Sources -weight 30 -postamble crdref/sources-postamble.md $< $@ crdref/sources-preamble.md

# The route reference is the OpenAPI document as a page: one section
# per route, with its parameters, its answers, and the fields they
# carry. The hand-written API page beside it holds the narrative,
# which no document can generate.
$(REFERENCE)/routes.md: $(OPENAPI) apiref/routes-preamble.md apiref/routes-postamble.md go.mod
	$(APIREF) -title Routes -weight 55 -postamble apiref/routes-postamble.md $< $@ apiref/routes-preamble.md
