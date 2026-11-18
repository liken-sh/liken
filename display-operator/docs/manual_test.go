// Package docs tests the manual's internal links. Hugo computes the
// id of each heading only when it builds the site, so a link to a
// moved page or a renamed heading would break only on the served
// site. This test resolves every link against the content tree, so
// the broken link fails `make test` instead.
package docs

import (
	"testing"

	"github.com/liken-sh/liken/brand/linkcheck"
)

// exceptions are the links that no content file matches, because
// hugo.yaml mounts them into static/: the manifests in deploy/ and
// the generated OpenAPI document.
var exceptions = []string{
	"/deploy/deviceclasses.yaml",
	"/deploy/displays.yaml",
	"/deploy/kustomization.yaml",
	"/deploy/operator.yaml",
	"/deploy/api.yaml",
	"/docs/reference/openapi.json",
}

func TestManualInternalLinks(t *testing.T) {
	for _, problem := range linkcheck.CheckManual("content", exceptions) {
		t.Error(problem)
	}
}
