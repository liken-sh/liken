// Package brand carries the project's presentation as data, for the
// Go programs that build pages.
//
// The brand domain owns the mark and the stylesheet, and two things
// consume them: the website, whose Makefile copies the files it
// needs, and the release channel's index pages, which this package
// serves. A Go program cannot embed a file outside its own directory,
// so the channel reads them through this import rather than through a
// copy in the releases domain. One file on disk, no copy to keep in
// step.
package brand

import _ "embed"

// Stylesheet is the shared stylesheet that both liken.sh and the
// release channel inline into every page. liken.css explains why
// neither site links it over the network.
//
//go:embed liken.css
var Stylesheet string

// Mark is the liken mark as an SVG document, for inlining into a
// page. It is a few kilobytes of polygons with no external
// references, so a page that carries it needs no second request and
// no image file beside it.
//
//go:embed liken.svg
var Mark string
