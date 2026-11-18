// Package docs tests the manual's links. The test checks each link that
// starts with a slash, and its heading anchor, against the content
// tree, so a broken link fails `make test` before a reader finds it on
// the site.
package docs

import (
	"testing"

	"github.com/liken-sh/liken/brand/linkcheck"
)

// exceptions are absolute links that point at no file in the content
// tree. hugo.yaml mounts the deploy manifests at /deploy/, so the
// site serves these paths anyway.
var exceptions = []string{
	"/deploy/crds.yaml",
	"/deploy/deviceclasses.yaml",
	"/deploy/kustomization.yaml",
	"/deploy/operator.yaml",
	"/deploy/rbac.yaml",
}

func TestManualInternalLinks(t *testing.T) {
	for _, problem := range linkcheck.CheckManual("content", exceptions) {
		t.Error(problem)
	}
}
