# This file describes how every liken binary learns which version it
# is.
#
# The person who cuts a release names it, in the calendar grammar
# that releases/versioning.md defines, and the releases domain passes
# that name down as LIKEN_VERSION. Every other build is a development
# build, and its version comes from what git already records:
# `git describe` names the most recent release tag and how far past
# it this tree is (v2026.07.11-001-5-gabc123), or the bare commit
# when no tag exists yet, with -dirty appended when the tree has
# uncommitted changes. This build invents nothing and needs no manual
# version bump. A dev machine's status.version.liken points at the
# exact commit it was built from.
#
# Make's model needs a file to notice a version change, and git does
# not keep one. The stamp file provides that file: every make run
# recomputes the version (the phony prerequisite forces the recipe)
# but rewrites .version only when the value differs. So binaries
# relink exactly when the version changes, and stay untouched
# otherwise. The stamp always records the git-derived version, never
# a release override. A release build writes into its own build tree
# and must not make the ordinary dev artifacts look stale.

VERSION_MK_DIR := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
GIT_VERSION := $(shell git -C $(VERSION_MK_DIR) describe --tags --always --dirty)
LIKEN_VERSION ?= $(GIT_VERSION)
LIKEN_VERSION_STAMP := $(VERSION_MK_DIR)/.version

$(LIKEN_VERSION_STAMP): liken-version-probe
	@echo "$(GIT_VERSION)" | cmp -s - $@ || echo "$(GIT_VERSION)" > $@

liken-version-probe:
.PHONY: liken-version-probe

# Including this file must not change the including Makefile's
# default goal. Resetting .DEFAULT_GOAL lets the next target parsed
# become the default goal instead.
.DEFAULT_GOAL :=
