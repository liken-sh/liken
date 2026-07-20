# This Makefile builds the mark (liken.svg) and everything derived
# from it. liken.svg is the original file; favicon.ico and liken.png
# are generated here and committed beside it. Because of this, nobody
# needs a rasterizer installed to use them. README.md explains the
# design and the biology behind the mark.
#
# rsvg-convert (from librsvg) turns the vector image into pixels, and
# ImageMagick packs the multi-size .ico file. Neither tool runs on the
# website's deploy path, because the committed files are what ship.
# This Makefile is for a person who changes the mark, not for CI.

all: favicon.ico liken.png

# This is the browser-tab icon, packed at three sizes so the tab stays
# sharp whether the browser asks for 16, 32, or 48 pixels. Each size
# is rasterized straight from the vector image, not downscaled from
# one large raster image. This method keeps the smallest size crisp.
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

.PHONY: all
