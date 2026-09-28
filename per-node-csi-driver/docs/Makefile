# The docs domain builds the manual for per-node.liken.sh, with the
# same `make build` every operator repository runs, so the docs
# workflows stay one pattern.
HUGO := go tool hugo

SKILLS := go tool skills
BASE := https://per-node.liken.sh

.PHONY: build
build:
	$(HUGO) --destination dist/site

# The site's own tests: every internal link resolves before the build
# (manual_test.go passes this site's exceptions).
.PHONY: test
test:
	go test ./...

# The skills target emits every guide as an Agent Skill into ../skills,
# one SKILL.md per guide, with the generator the brand repository
# publishes (its README says what the output holds). The output is
# committed, and the root test-docs target fails when it is stale.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills
