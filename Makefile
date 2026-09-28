# The targets that cover more than one component. Each component's own
# Makefile builds and tests that component; this file only runs them
# together.

# The project's manuals, built into one tree in the layout of the one
# site that plan 69 describes: liken's manual at the root, and each
# component's manual under the first label of the name its own site has
# today, so display.liken.sh becomes /display/.
#
#   make docs       build every manual into dist/docs/
#   make preview    build them, then serve dist/docs/ at PREVIEW_URL
#
# Each manual builds twice. The component's own `make -C docs build`
# generates the reference pages and runs its own checks, and then Hugo
# builds the manual again under its preview address. Hugo takes that
# address on its command line, so no component's hugo.yaml changes.
# A link from one manual to another still names the other site's own
# address, so it leaves the preview.
PORT ?= 8080
PREVIEW_URL := http://localhost:$(PORT)
DOCS := $(CURDIR)/dist/docs

# Each entry is a component's directory and its prefix in the site.
SITES := \
	audio-operator:audio \
	bluetooth-operator:bluetooth \
	display-operator:display \
	equipment-operator:equipment \
	media-operator:media \
	library-operator:library \
	people-operator:people \
	git-csi-driver:git \
	per-node-csi-driver:per-node

.PHONY: docs
docs:
	rm -rf $(DOCS)
	$(MAKE) -C liken/docs build
	cd liken/docs && go tool hugo build --quiet \
		--baseURL $(PREVIEW_URL)/ --destination $(DOCS)
	@set -e; for site in $(SITES); do \
		dir=$${site%%:*}; prefix=$${site##*:}; \
		echo "building $$dir into /$$prefix/"; \
		$(MAKE) -C $$dir/docs build; \
		(cd $$dir/docs && go tool hugo build --quiet \
			--baseURL $(PREVIEW_URL)/$$prefix/ --destination $(DOCS)/$$prefix); \
	done

# The server is brand's preview program: a file server that answers
# the way GitHub Pages does, with text/markdown for each page's
# Markdown twin and the site's 404.html for a missing page.
.PHONY: preview
preview: docs
	cd brand && go run ./preview -dir $(DOCS) -addr localhost:$(PORT)
