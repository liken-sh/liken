# The docs domain builds the website: the front page and the manual,
# rendered ahead of time into a tree of plain files. Nothing renders
# on a server, and nothing renders in a browser. Every page is built
# twice, as HTML for people and as Markdown for agents, and the two
# land side by side in the output tree.
#
# The build has three inputs: the hand-written content and layouts
# here, the CRD schemas in machine/ and cluster/ (which crdref turns
# into the reference pages), and the brand assets. Hugo assembles
# them into dist/site/, which the Dockerfile bakes into the nginx
# image that serves liken.sh.

# The pin is a file, not a variable, so a Hugo upgrade shows as a
# one-line, reviewable diff. override keeps the pin authoritative
# even when a caller passes VERSION on the command line.
override VERSION := $(strip $(file <VERSION))

HUGO := dist/hugo/$(VERSION)/hugo

# The reference pages for the two CRDs are generated, never edited:
# the schemas carry every field's description, and crdref arranges
# them into pages, so the reference cannot drift from what the API
# server enforces.
GENERATED := content/docs/reference/machine.md content/docs/reference/cluster.md

BRAND := static/icon.svg static/favicon.ico static/brand/liken.svg static/brand/liken.png

.PHONY: all build generate serve image test clean

all: build

$(HUGO): VERSION fetch.sh
	./fetch.sh

# The brand assets are copies. The brand domain owns the originals,
# and the site takes them under the names its pages link to: the two
# icon names that browsers fetch, and a brand/ directory that gives
# the mark a stable public URL, so anything outside this repository
# can embed the image from liken.sh instead of deep-linking into the
# forge.
static/icon.svg: ../brand/liken.svg
	mkdir -p static
	cp $< $@

static/favicon.ico: ../brand/favicon.ico
	mkdir -p static
	cp $< $@

static/brand/liken.svg: ../brand/liken.svg
	mkdir -p static/brand
	cp $< $@

static/brand/liken.png: ../brand/liken.png
	mkdir -p static/brand
	cp $< $@

content/docs/reference/machine.md: ../machine/manifests/machines-crd.yaml $(wildcard crdref/*.go)
	go run ./crdref $< $@

content/docs/reference/cluster.md: ../cluster/manifests/clusters-crd.yaml $(wildcard crdref/*.go)
	go run ./crdref $< $@

generate: $(GENERATED)

# The built site. The find covers every content and layout file, so
# an edit to any page rebuilds. release.txt is the deploy marker: CI
# checks the served copy against the commit it shipped. It lives in
# the output tree, never in content/, because it is not a page.
dist/site/index.html: $(HUGO) hugo.yaml $(GENERATED) $(BRAND) $(shell find content layouts -type f)
	$(HUGO) build --cleanDestinationDir --destination dist/site
	git rev-parse HEAD > dist/site/release.txt

build: dist/site/index.html

# The authoring loop: edit a page, and the browser refreshes. Hugo's
# server injects a live-reload script in this mode, which is the one
# place any JavaScript touches the site; the built tree has none.
# --renderToMemory keeps the preview out of the working tree; the
# only rendered tree on disk is the one `build` writes to dist/site.
# --disableFastRender makes every change rebuild every page: fast
# render only re-renders pages a live-reload browser is watching, so
# a layout edit would go stale for any page fetched another way.
serve: $(HUGO) $(GENERATED) $(BRAND)
	$(HUGO) server --renderToMemory --disableFastRender

# The production preview: the same image the cluster runs, serving on
# localhost. `docker run --rm -p 8080:80 liken-website:dev` and read
# http://localhost:8080/.
image: build
	docker build -t liken-website:dev .

test:
	go test ./crdref

# clean removes everything the build produced, but keeps cache/: the
# Hugo tarball redownloads identically, and cleaning scratch files
# should not cost the network anything.
clean:
	rm -rf dist static $(GENERATED)
