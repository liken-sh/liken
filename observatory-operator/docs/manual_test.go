// Package docs holds the manual's own tests. The site's internal
// links resolve at authoring time: brand's linkcheck walks the
// content tree and checks every link against the headings it
// targets, so a broken deep link fails `make test` in this
// repository instead of on the served site.
package docs

import (
	"testing"

	"github.com/liken-sh/brand/linkcheck"
)

// exceptions are the absolute links no content file answers for.
// Each one is served by the site anyway: the deploy manifests
// and the example publish through module mounts in hugo.yaml.
var exceptions = []string{
	"/deploy/kustomization.yaml",
	"/examples/simulators.yaml",
}

func TestManualInternalLinks(t *testing.T) {
	for _, problem := range linkcheck.CheckManual("content", exceptions) {
		t.Error(problem)
	}
}
