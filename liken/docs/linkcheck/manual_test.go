// Package linkcheck owns the manual's internal links.
//
// The manual deep-links across its own pages: a guide points at one
// section of a generated reference page, and the front page points at
// the guides. Every one of those links names a heading id that Hugo
// computes at build time, so nothing checks them at authoring time.
// brand's linkcheck package holds the one implementation of Hugo's
// id algorithm and the check that resolves every internal link.
// This package holds the site's own exceptions and the test that
// runs the check.
package linkcheck

import (
	"testing"

	"github.com/liken-sh/brand/linkcheck"
)

// The content tree, relative to this package. The two generated
// reference pages land in it too, so one walk covers the whole
// manual.
const contentRoot = "../content"

// Absolute links that are not content pages. Each one is a build
// product or a copied asset, so no content file answers for it; this
// list says where each target really comes from.
var exceptions = []string{
	// layouts/home.llms.txt renders the home page as this output.
	"/llms.txt",
	// layouts/home.llms-full.txt renders the whole manual as one file.
	"/llms-full.txt",
	// The brand theme's static/ tree serves the mark at this URL.
	"/brand/liken.svg",
	// The Makefile writes the deploy marker into the built tree.
	"/release.txt",
	// CI renders the test coverage report from the profile the gate
	// wrote, and copies it into the built tree.
	"/coverage.html",
}

// TestManualInternalLinks resolves every absolute link in the manual:
// a page link must name a content file, and a fragment must name a
// heading id in its target page. The ids come from Anchor, the same
// function crdref links with, so the check and the generator cannot
// disagree.
func TestManualInternalLinks(t *testing.T) {
	for _, problem := range linkcheck.CheckManual(contentRoot, exceptions) {
		t.Error(problem)
	}
}
