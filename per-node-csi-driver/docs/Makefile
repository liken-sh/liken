# Builds the manual served at liken.sh/per-node. Every manual has the
# same build, test, and skills targets, so the root Makefile and CI
# build each one the same way.
HUGO := go tool hugo

# A recipe that fails, or a build that is killed during one, leaves its
# target partly written and newer than its inputs. make deletes that
# target, so the next build makes it again and does not take it as
# current.
.DELETE_ON_ERROR:

SKILLS := go tool skills
BASE := https://liken.sh/per-node/

.PHONY: build
build:
	$(HUGO) --destination dist/site

# The tests check that every internal link resolves. manual_test.go
# lists this site's exceptions.
.PHONY: test
test:
	go test ./...

# skills writes each guide as an Agent Skill, a SKILL.md in ../skills,
# with the generator of the brand component. The output is committed,
# and test-docs in ../Makefile fails when it is stale.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills
