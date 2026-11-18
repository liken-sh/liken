// Package docs checks the manual's internal links at test time, so a
// link to a page or a heading that does not exist fails `make test`
// before the site serves it.
package docs

import (
	"testing"

	"github.com/liken-sh/liken/brand/linkcheck"
)

// exceptions are links that no file in the content tree matches,
// because hugo.yaml mounts the component's `deploy/` directory at
// `/deploy/`.
var exceptions = []string{
	"/deploy/controller.yaml",
	"/deploy/csidriver.yaml",
	"/deploy/kustomization.yaml",
	"/deploy/node.yaml",
	"/deploy/rbac.yaml",
}

func TestManualInternalLinks(t *testing.T) {
	for _, problem := range linkcheck.CheckManual("content", exceptions) {
		t.Error(problem)
	}
}
