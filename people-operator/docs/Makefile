# The docs domain builds the manual for people.liken.sh. The
# reference page is generated, never edited: crdref, the generator
# the brand repository publishes, renders it from deploy/people-crd.yaml,
# so a schema edit is a manual edit. GENERATED lists that page, and
# the rule names go.mod as an input, so a generator bump regenerates
# it.
HUGO := go tool hugo
CRDREF := go tool crdref

REFERENCE := content/docs/reference

GENERATED := $(REFERENCE)/people.md

SKILLS := go tool skills
BASE := https://people.liken.sh

.PHONY: build
build: $(GENERATED)
	$(HUGO) --destination dist/site

# The site's own test: every internal link resolves before the build.
# manual_test.go names the links that no content file answers for.
.PHONY: test
test: $(GENERATED)
	go test ./...

$(REFERENCE)/people.md: ../deploy/people-crd.yaml go.mod
	$(CRDREF) -title Person -weight 10 $< $@

# The skills target emits every guide as an Agent Skill into ../skills,
# one SKILL.md per guide, with the generator the brand repository
# publishes (its README says what the output holds). The output is
# committed, and the root test-docs target fails when it is stale.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills
