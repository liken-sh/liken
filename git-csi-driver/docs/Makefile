# Builds the manual served at liken.sh/git. Every manual has a
# `make build` target, so the root Makefile and CI build all manuals the
# same way.
HUGO := go tool hugo

# A recipe that fails or is killed can leave a partly written target
# that is newer than its inputs. make deletes that target, so the next
# build creates it again.
.DELETE_ON_ERROR:

SKILLS := go tool skills
BASE := https://liken.sh/git/

.PHONY: build
build:
	$(HUGO) --destination dist/site

# Checks that every internal link resolves. manual_test.go lists this
# site's exceptions.
.PHONY: test
test:
	go test ./...

# Writes each guide as an Agent Skill, one SKILL.md per guide, into
# ../skills, using the generator from the brand component (its README
# describes the output). The output is committed, and the test-docs
# target in ../Makefile fails when it is out of date.
.PHONY: skills
skills:
	$(SKILLS) -base $(BASE) content/docs/guides ../skills
