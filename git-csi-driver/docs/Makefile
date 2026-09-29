# The docs domain builds the manual for git.liken.sh, with the same
# `make build` every manual runs, so the root Makefile and CI build each
# manual one way.
HUGO := go tool hugo

SKILLS := go tool skills
BASE := https://liken.sh/git/

.PHONY: build
build:
	$(HUGO) --destination dist/site

# The site's own tests: every internal link resolves before the build
# (manual_test.go passes this site's exceptions).
.PHONY: test
test:
	go test ./...

# The skills target emits every guide as an Agent Skill into ../skills,
# one SKILL.md per guide, with the generator the brand component
# publishes (its README says what the output holds). The output is
# committed, and the test-docs target in ../Makefile fails when it is
# stale.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills
