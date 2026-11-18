// Package docs tests the manual. The test checks that every internal
// link in the content tree points at a page or a heading that exists,
// so a broken link fails `make test` instead of the served site.
package docs

import (
	"testing"

	"github.com/liken-sh/liken/brand/linkcheck"
)

// exceptions are links that point at no content file. The site serves
// them from the `../deploy` module mount in `hugo.yaml`.
var exceptions = []string{
	"/deploy/kustomization.yaml",
	"/deploy/people-crd.yaml",
}

func TestManualInternalLinks(t *testing.T) {
	for _, problem := range linkcheck.CheckManual("content", exceptions) {
		t.Error(problem)
	}
}
