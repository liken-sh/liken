# This Makefile builds the mark (liken.svg) and everything derived
# from it, then installs the copies that the Hugo theme serves.
# liken.svg and liken.css are the originals; every other image file
# comes from liken.svg, and every file under static/ and assets/ is a
# copy of one of the originals.
#
# Every derived file is committed. A site consumes this repository as
# a git submodule and runs nothing inside it, so the checkout itself
# must already hold everything the theme serves. This Makefile is for
# a person who changes the mark or the stylesheet, not for CI: edit
# an original, run `make`, and commit the files that change.
#
# rsvg-convert (from librsvg) turns the vector image into pixels, and
# ImageMagick packs the multi-size .ico file.

COPIES := static/favicon.ico static/icon.svg \
          static/brand/liken.svg static/brand/liken.png \
          assets/liken.css

all: favicon.ico liken.png $(COPIES)

# This is the browser-tab icon, packed at three sizes so the tab stays
# sharp whether the browser asks for 16, 32, or 48 pixels.
# rsvg-convert rasterizes each size straight from the vector image.
# Rasterizing one large image and downscaling it would blur the
# 16-pixel size.
favicon.ico: liken.svg
	rsvg-convert -w 16 -h 16 $< -o favicon-16.png
	rsvg-convert -w 32 -h 32 $< -o favicon-32.png
	rsvg-convert -w 48 -h 48 $< -o favicon-48.png
	magick favicon-16.png favicon-32.png favicon-48.png $@
	rm -f favicon-16.png favicon-32.png favicon-48.png

# This is a raster export for places that will not accept a vector
# image, mainly a GitHub organization avatar. The image is transparent
# and square, so the host's own background shows through.
liken.png: liken.svg
	rsvg-convert -w 1024 -h 1024 $< -o $@

# These are the theme's public files. Hugo merges a theme's static/
# tree into every consuming site's output, so these copies give each
# site the same URLs. The two icon names are the ones browsers fetch
# on their own. The brand/ directory gives the mark a stable public
# URL, so a page anywhere can embed the image without a deep link
# into the forge. The stylesheet goes to assets/ rather than static/,
# because the theme inlines it into every page instead of serving it
# as a file, and assets/ is the tree Hugo reads for that.
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

.PHONY: all
