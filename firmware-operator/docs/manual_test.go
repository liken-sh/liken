// Package docs checks that every internal link in the manual points to
// a page that exists, and to a heading on that page when the link has
// a fragment. A broken link then fails `make test` before the site
// serves it.
package docs

import (
	"testing"

	"github.com/liken-sh/liken/brand/linkcheck"
)

// exceptions are links to the deploy manifests. No content file
// provides them, because hugo.yaml mounts ../deploy at /deploy.
var exceptions = []string{
	"/deploy/kustomization.yaml",
	"/deploy/firmware-crd.yaml",
}

func TestManualInternalLinks(t *testing.T) {
	for _, problem := range linkcheck.CheckManual("content", exceptions) {
		t.Error(problem)
	}
}
