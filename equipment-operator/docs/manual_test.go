// Package docs tests the manual. Its test fails when a link between
// pages of the manual points to a page or a heading that does not
// exist, so a broken link shows up in `make test` and not on the
// published site.
package docs

import (
	"testing"

	"github.com/liken-sh/liken/brand/linkcheck"
)

// exceptions are links to files that are not in content/. The site
// serves them because hugo.yaml mounts ../deploy at static/deploy.
var exceptions = []string{
	"/deploy/kustomization.yaml",
	"/deploy/receivers-crd.yaml",
	"/deploy/rbac.yaml",
	"/deploy/operator.yaml",
}

func TestManualInternalLinks(t *testing.T) {
	for _, problem := range linkcheck.CheckManual("content", exceptions) {
		t.Error(problem)
	}
}
