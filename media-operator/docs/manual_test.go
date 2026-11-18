// Package docs tests that every internal link of the manual resolves
// to a page in content and, when the link has a fragment, to a heading
// on that page.
package docs

import (
	"testing"

	"github.com/liken-sh/liken/brand/linkcheck"
)

// exceptions are links to files that are not in content. hugo.yaml
// mounts ../deploy at /deploy, so the site serves them.
var exceptions = []string{
	"/deploy/kustomization.yaml",
}

func TestManualInternalLinks(t *testing.T) {
	for _, problem := range linkcheck.CheckManual("content", exceptions) {
		t.Error(problem)
	}
}
