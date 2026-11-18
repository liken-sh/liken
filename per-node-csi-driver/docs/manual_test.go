// Package docs checks the manual's internal links. A link to a page
// or a heading that content/ does not have fails `make test`, so the
// broken link is found before the site is published.
package docs

import (
	"testing"

	"github.com/liken-sh/liken/brand/linkcheck"
)

// exceptions are the links to the deploy/ manifests. No file in
// content/ matches them, because hugo.yaml mounts ../deploy at
// static/deploy and the site serves the manifests from there.
var exceptions = []string{
	"/deploy/csidriver.yaml",
	"/deploy/kustomization.yaml",
	"/deploy/node.yaml",
	"/deploy/rbac.yaml",
	"/deploy/storageclass.yaml",
}

func TestManualInternalLinks(t *testing.T) {
	for _, problem := range linkcheck.CheckManual("content", exceptions) {
		t.Error(problem)
	}
}
