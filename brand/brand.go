// Package brand embeds the project's mark and stylesheet for Go
// programs that build pages.
//
// The Hugo sites use this directory as their theme. A Go program that
// builds pages without Hugo, such as the release channel's index in
// liken/releases, imports this package, because go:embed reads only
// files inside the program's own module.
package brand

import _ "embed"

// Stylesheet is the stylesheet that each site inlines into every
// page. The comment at the top of liken.css gives the reason.
//
//go:embed liken.css
var Stylesheet string

// Mark is the liken mark as an SVG document, to inline into a page.
// It references no other file.
//
//go:embed liken.svg
var Mark string
