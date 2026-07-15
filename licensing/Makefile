# The licensing domain: what liken redistributes and what that
# obliges.
#
# A liken release is an aggregate. liken's own programs are MIT, but
# the artifacts also carry the kernel, k3s, and a handful of other
# projects' binaries, and their licenses attach two obligations to
# redistribution. Both are the release channel's to meet, and this
# domain produces the artifacts that meet them:
#
#   dist/LICENSES.md      the notices: every redistributed component,
#                         its license and copyright, where its source
#                         lives, and the license texts themselves.
#                         Bundled into every release as an artifact,
#                         so the notices travel with the binaries —
#                         onto the channel, onto install sticks, onto
#                         machines' slots.
#
#   dist/sources/         the corresponding source for the copyleft
#                         components, mirrored at pinned digests
#                         (sources.sh explains why mirroring, and
#                         what tracks what). The release workflow
#                         publishes this tree to the channel at
#                         sources/<component>/<version>/; it is keyed
#                         by component version, so releases that
#                         share pins share sources.
#
# The GPL's terms are what shape both artifacts: a copy of the
# license must accompany every binary (hence the appendix of full
# texts), and source must be offered from the same place the binaries
# are (hence the mirror on the channel itself).

all: dist/LICENSES.md

# The license texts, in the order the aggregate meets them: liken's
# own MIT first, then the copyleft family, then the permissive texts
# the Go dependencies and static libraries reference.
TEXTS = texts/GPL-2.0.txt \
        texts/LGPL-2.0.txt \
        texts/LGPL-2.1.txt \
        texts/GPL-3.0.txt \
        texts/Apache-2.0.txt \
        texts/MPL-2.0.txt \
        texts/MIT.txt \
        texts/BSD-2-Clause.txt \
        texts/BSD-3-Clause.txt

# One document: the authored notices, then every license text as an
# appendix, each fenced so the texts stay verbatim however the
# markdown is rendered. liken's own license leads the appendix — the
# MIT text in texts/ is the generic grant the Go modules reference,
# while ../LICENSE carries liken's copyright line.
dist/LICENSES.md: NOTICES.md $(TEXTS) ../LICENSE Makefile
	@mkdir -p dist
	{ cat NOTICES.md; \
	  printf '\n# Appendix: license texts\n'; \
	  printf '\n## liken (MIT)\n\n```\n'; cat ../LICENSE; printf '```\n'; \
	  for text in $(TEXTS); do \
	    printf '\n## %s\n\n```\n' "$$(basename $$text .txt)"; \
	    cat "$$text"; \
	    printf '```\n'; \
	  done; } > $@

# The source mirror is not part of `all`: it downloads a few hundred
# megabytes and only the release workflow needs it.
sources:
	./sources.sh

clean:
	rm -rf dist cache

.PHONY: all sources clean
