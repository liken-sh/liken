# The docs domain builds the manual for media.liken.sh. Five of the
# reference pages are generated, never edited: crdref, the generator
# the brand repository publishes, renders each page from its CRD
# schema in deploy/, a hand-written preamble that opens the page, and
# a hand-written postamble that carries its bus sections. The schema
# descriptions become the field tables, so a schema edit is a manual
# edit. The sixth generated page is the route reference, which apiref
# renders from the API's own OpenAPI document. go.mod pins both
# generators the way it pins Hugo, as tool directives, and each rule
# lists go.mod as an input, so a generator bump regenerates every
# page.
HUGO := go tool hugo
CRDREF := go tool crdref
APIREF := go tool apiref

REFERENCE := content/docs/reference

# The OpenAPI document is committed at the repository root, where the
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
BASE := https://media.liken.sh

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

$(REFERENCE)/players.md: ../deploy/players-crd.yaml crdref/players-preamble.md crdref/players-postamble.md go.mod
	$(CRDREF) -title Players -weight 10 -postamble crdref/players-postamble.md $< $@ crdref/players-preamble.md

$(REFERENCE)/plays.md: ../deploy/plays-crd.yaml crdref/plays-preamble.md crdref/plays-postamble.md go.mod
	$(CRDREF) -title Plays -weight 20 -postamble crdref/plays-postamble.md $< $@ crdref/plays-preamble.md

$(REFERENCE)/remotes.md: ../deploy/remotes-crd.yaml crdref/remotes-preamble.md crdref/remotes-postamble.md go.mod
	$(CRDREF) -title Remotes -weight 30 -postamble crdref/remotes-postamble.md $< $@ crdref/remotes-preamble.md

$(REFERENCE)/keymaps.md: ../deploy/keymaps-crd.yaml crdref/keymaps-preamble.md crdref/keymaps-postamble.md go.mod
	$(CRDREF) -title Keymaps -weight 40 -postamble crdref/keymaps-postamble.md $< $@ crdref/keymaps-preamble.md

$(REFERENCE)/mediapreferences.md: ../deploy/mediapreferences-crd.yaml crdref/mediapreferences-preamble.md crdref/mediapreferences-postamble.md go.mod
	$(CRDREF) -title MediaPreferences -weight 50 -postamble crdref/mediapreferences-postamble.md $< $@ crdref/mediapreferences-preamble.md

# The route reference is the OpenAPI document as a page: one section
# per route, with its parameters, its answers, and the fields they
# carry. The hand-written API page beside it holds the narrative,
# which no document can generate.
$(REFERENCE)/routes.md: $(OPENAPI) apiref/routes-preamble.md apiref/routes-postamble.md go.mod
	$(APIREF) -title Routes -weight 66 -postamble apiref/routes-postamble.md $< $@ apiref/routes-preamble.md
