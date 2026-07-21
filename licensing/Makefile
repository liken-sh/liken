# The licensing domain: what liken redistributes, and what that
# requires.
#
# A liken release is an aggregate. liken's own programs use the MIT
# license, but the artifacts also carry the kernel, k3s, and a small
# number of other projects' binaries. Their licenses attach two
# obligations to redistribution. The release channel must meet both,
# and this domain produces the artifacts that meet them:
#
#   dist/LICENSES.md      the notices: every redistributed component,
#                         its license and copyright, where its source
#                         lives, and the license texts themselves.
#                         Every release bundles this file as an
#                         artifact, so the notices travel with the
#                         binaries: onto the channel, onto install
#                         sticks, and onto machines' slots.
#
#   dist/sources/         the corresponding source for the copyleft
#                         components, mirrored at pinned digests.
#                         sources.sh explains why the project mirrors
#                         instead of linking, and what tracks what.
#                         The release workflow publishes this tree to
#                         the channel at sources/<component>/<version>/;
#                         it is keyed by component version, so
#                         releases that share pins share sources.
#
# The GPL's terms shape both artifacts. A copy of the license must
# accompany every binary, which is why the appendix carries the full
# texts. Source must be offered from the same place as the binaries,
# which is why the channel itself carries the mirror.

all: dist/LICENSES.md

# The license texts, in the order the aggregate meets them: liken's
# own MIT license first, then the copyleft family, then the
# permissive texts that the Go dependencies and static libraries
# reference, then the microcode vendors' redistribution terms. The
# microcode texts come from the microcode domain's verified fetch,
# not from copies in this repo, so they can never drift from the
# vendored bytes. (The driver firmware's many per-family texts are
# deliberately not here: they ship in the image at
# /lib/firmware/LICENSES, as NOTICES.md explains.)
MICROCODE_VERSION := $(strip $(file <../microcode/VERSION))
TEXTS = texts/GPL-2.0.txt \
        texts/LGPL-2.0.txt \
        texts/LGPL-2.1.txt \
        texts/GPL-3.0.txt \
        texts/Apache-2.0.txt \
        texts/MPL-2.0.txt \
        texts/MIT.txt \
        texts/BSD-2-Clause.txt \
        texts/BSD-3-Clause.txt \
        ../microcode/dist/$(MICROCODE_VERSION)/Intel-microcode.txt \
        ../microcode/dist/$(MICROCODE_VERSION)/AMD-microcode.txt

# This produces one document: the authored notices, then every
# license text as an appendix. Each text is fenced, so the text stays
# verbatim however the markdown is rendered. liken's own license
# leads the appendix. The MIT text in texts/ is the generic grant
# that the Go modules reference, while ../LICENSE carries liken's
# copyright line.
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

# The source mirror is not part of `all`. It downloads a few hundred
# megabytes, and only the release workflow needs it.
sources:
	./sources.sh

clean:
	rm -rf dist cache

.PHONY: all sources clean
