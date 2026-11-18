// Package docs holds the tests of the manual. brand's linkcheck checks
// every internal link in the content tree against the page and heading
// it targets, so a broken link fails make test instead of breaking on
// the served site.
package docs

import (
	"testing"

	"github.com/liken-sh/liken/brand/linkcheck"
)

// exceptions are absolute links that no file under content provides.
// The site serves them from module mounts in hugo.yaml: the deploy
// manifests from ../deploy and the OpenAPI document from static/.
var exceptions = []string{
	"/deploy/deviceclasses.yaml",
	"/deploy/kustomization.yaml",
	"/deploy/operator.yaml",
	"/v1/audio/openapi.json",
}

func TestManualInternalLinks(t *testing.T) {
	for _, problem := range linkcheck.CheckManual("content", exceptions) {
		t.Error(problem)
	}
}
