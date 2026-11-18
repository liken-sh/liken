# Builds the images derived from the mark, liken.svg, and copies the
# mark and the stylesheet, liken.css, into static/ and assets/, where
# the Hugo theme serves them. Every generated file is committed,
# because a site uses this directory as its theme and runs nothing
# inside it. After you change the mark or the stylesheet, run `make`
# and commit what changes. CI runs only the test targets.
#
# rsvg-convert (from librsvg) rasterizes the mark, and ImageMagick
# packs the .ico file.

# Delete the target of a recipe that fails, so that the next build
# does not take a partly written file as current.
.DELETE_ON_ERROR:

COPIES := static/favicon.ico static/icon.svg \
          static/brand/liken.svg static/brand/liken.png \
          assets/liken.css

all: favicon.ico liken.png $(COPIES)

# Each size is rasterized from the vector image, because downscaling
# one large raster blurs the 16-pixel size.
favicon.ico: liken.svg
	rsvg-convert -w 16 -h 16 $< -o favicon-16.png
	rsvg-convert -w 32 -h 32 $< -o favicon-32.png
	rsvg-convert -w 48 -h 48 $< -o favicon-48.png
	magick favicon-16.png favicon-32.png favicon-48.png $@
	rm -f favicon-16.png favicon-32.png favicon-48.png

# For places that do not accept a vector image, such as a GitHub
# organization avatar.
liken.png: liken.svg
	rsvg-convert -w 1024 -h 1024 $< -o $@

# Hugo copies a theme's static/ tree into every site that uses the
# theme, so every site serves these files at the same URLs.
# static/brand/ gives the mark a URL that any page can embed without
# linking into the forge. The stylesheet goes in assets/, because the
# theme inlines it with resources.Get, which reads assets/.
static/favicon.ico: favicon.ico
	mkdir -p static
	cp $< $@

static/icon.svg: liken.svg
	mkdir -p static
	cp $< $@

static/brand/liken.svg: liken.svg
	mkdir -p static/brand
	cp $< $@

static/brand/liken.png: liken.png
	mkdir -p static/brand
	cp $< $@

assets/liken.css: liken.css
	mkdir -p assets
	cp $< $@

# staticcheck and go-test-coverage are tool dependencies in go.mod.

# A package with no test file writes nothing to the coverage profile,
# so the coverage gate cannot fail it. test-go fails on such a package
# instead.
UNTESTED_PACKAGES := go list -f '{{if not (or .TestGoFiles .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./...

test: test-go test-rust

test-go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go mod tidy -diff
	test -z "$$($(UNTESTED_PACKAGES))" || { echo 'packages with no test file:'; $(UNTESTED_PACKAGES); exit 1; }
	go vet ./...
	go tool staticcheck ./...
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool go-test-coverage --config=.testcoverage.yml

# liken-iced parses liken.svg and liken.css, so this also fails on a
# change to either that the crate cannot read.
test-rust:
	$(MAKE) -C iced test

.PHONY: all test test-go test-rust
