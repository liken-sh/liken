# The mark (liken.svg) and everything derived from it. The SVG is the
# source of truth; favicon.ico and liken.png are generated here and
# committed alongside it, so nobody needs a rasterizer installed to
# use them. README.md explains the design and the biology behind it.
#
# rsvg-convert (from librsvg) turns the vector into pixels, and
# ImageMagick packs the multi-size .ico. Neither runs on the website's
# deploy path (the committed files are what ship), so this Makefile is
# for a person changing the mark, not for CI.

all: favicon.ico liken.png

# The browser-tab icon, packed at three sizes so the tab stays sharp
# whether the browser asks for 16, 32, or 48 pixels. Each size is
# rasterized straight from the vector rather than downscaled from one
# large raster, which is what keeps the smallest size crisp.
favicon.ico: liken.svg
	rsvg-convert -w 16 -h 16 $< -o favicon-16.png
	rsvg-convert -w 32 -h 32 $< -o favicon-32.png
	rsvg-convert -w 48 -h 48 $< -o favicon-48.png
	magick favicon-16.png favicon-32.png favicon-48.png $@
	rm -f favicon-16.png favicon-32.png favicon-48.png

# A raster export for the places that will not take a vector, mainly
# a GitHub organization avatar. Transparent and square, so the host's
# own background shows through.
liken.png: liken.svg
	rsvg-convert -w 1024 -h 1024 $< -o $@

.PHONY: all
