// Package docs tests the manual. A broken internal link fails
// `make test` here instead of on the served site.
package docs

import (
	"testing"

	"github.com/liken-sh/liken/brand/linkcheck"
)

// exceptions are absolute links that no file in the content tree
// matches. The site serves them from the module mounts in hugo.yaml.
var exceptions = []string{
	"/deploy/kustomization.yaml",
	"/examples/simulators.yaml",
}

func TestManualInternalLinks(t *testing.T) {
	for _, problem := range linkcheck.CheckManual("content", exceptions) {
		t.Error(problem)
	}
}
