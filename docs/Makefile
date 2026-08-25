# The docs domain builds the website: the front page and the manual,
# rendered ahead of time into a tree of plain files. Nothing renders
# on a server, and nothing renders in a browser. Every page is built
# twice, as HTML for people and as Markdown for agents, and the two
# land side by side in the output tree.
#
# The build has three inputs: the hand-written content here, the CRD
# schemas in machine/ and cluster/ (which crdref turns into the
# reference pages), and the brand theme, the git submodule at
# themes/brand that supplies the shared page shell, stylesheet, and
# public brand files. Hugo assembles them into dist/site/, the tree
# that CI publishes to GitHub Pages as liken.sh.
#
# Hugo itself is a pinned tool dependency of this domain's own Go
# module (go.mod explains why the module is nested), so `go tool
# hugo` is the whole toolchain: no fetched binary, no separate pin
# file.
HUGO := go tool hugo

# The reference pages for the two CRDs are generated, never edited:
# the schemas hold every field's description, and crdref arranges
# them into pages, so the reference cannot drift from what the API
# server enforces.
GENERATED := content/docs/reference/machine.md content/docs/reference/cluster.md

.PHONY: all build generate serve test clean

all: build

# Each page opens with a hand-written preamble that the schema cannot
# hold: how the page relates to the guides. The preamble is beside
# the generator and passes as the third argument.
# crdref is pinned from the brand repository as a tool dependency,
# so go.mod versions the generator, and go.mod is the prerequisite
# that rebuilds a page when the generator changes.
content/docs/reference/machine.md: ../machine/manifests/machines-crd.yaml crdref/machine-preamble.md go.mod
	go tool crdref $< $@ crdref/machine-preamble.md

content/docs/reference/cluster.md: ../cluster/manifests/clusters-crd.yaml crdref/cluster-preamble.md go.mod
	go tool crdref $< $@ crdref/cluster-preamble.md

generate: $(GENERATED)

# The built site. The find covers every content, layout, and theme
# file, so an edit to any page, and a bump of the theme submodule,
# rebuilds. go.mod is a prerequisite because it pins Hugo itself.
# release.txt is the deploy marker: CI checks the served copy against
# the commit it shipped. It is in the output tree, never in
# content/, because it is not a page.
dist/site/index.html: hugo.yaml go.mod go.sum $(GENERATED) $(shell find content layouts themes -type f)
	$(HUGO) build --cleanDestinationDir --destination dist/site
	git rev-parse HEAD > dist/site/release.txt

build: dist/site/index.html

# The authoring loop: edit a page, and the browser refreshes. Hugo's
# server injects a live-reload script in this mode. That script is the
# only JavaScript the site ever runs, and the built tree has none.
# --renderToMemory keeps the preview out of the working tree; the
# only rendered tree on disk is the one `build` writes to dist/site.
# --disableFastRender makes every change rebuild every page: fast
# render only re-renders pages a live-reload browser is watching, so
# a layout edit would go stale for any page fetched another way.
serve: $(GENERATED)
	$(HUGO) server --renderToMemory --disableFastRender

# The domain's Go checks run here, against this module, because the
# root module's `./...` does not descend into a nested module. The
# link check in linkcheck resolves every internal link in the manual,
# and the generated pages are both link sources and link targets, so
# the tests need them in place first.
test: $(GENERATED)
	go vet ./...
	go tool staticcheck ./...
	go test ./...

# clean removes everything the build produced. Hugo's own bytes are
# not here to remove: the Go module cache holds them, outside the
# tree.
clean:
	rm -rf dist $(GENERATED)
