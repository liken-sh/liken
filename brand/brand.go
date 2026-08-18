// Package brand holds the project's presentation as data, for the
// Go programs that build pages.
//
// The brand domain owns the mark and the stylesheet, and two kinds of
// consumer read them. The Hugo sites take this repository as a git
// submodule and use it as their theme. The theme's assets/ and
// static/ trees hold the files under the URLs the pages link to. A
// Go program that builds pages outside Hugo, such as the release
// channel's index builder, imports this package instead, because a Go
// program can only embed files from its own module. One repository
// holds the originals, and the Makefile rebuilds every derived copy
// from them.
package brand

import _ "embed"

// Stylesheet is the shared stylesheet that every liken site inlines
// into every page. liken.css explains why no site links it over the
// network.
//
//go:embed liken.css
var Stylesheet string

// Mark is the liken mark as an SVG document, for inlining into a
// page. It is a few kilobytes of polygons with no external
// references, so a page that inlines it needs no second request and
// no image file beside it.
//
//go:embed liken.svg
var Mark string
