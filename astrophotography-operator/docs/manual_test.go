// Package docs tests that every absolute link in the manual's Markdown
// points to a page and a heading that exist, so a broken link fails
// `make test` before the site is published.
package docs

import (
	"testing"

	"github.com/liken-sh/liken/brand/linkcheck"
)

// exceptions lists links to files that are not under `content`. The
// module mount of `../deploy` in `hugo.yaml` publishes them.
var exceptions = []string{
	"/deploy/kustomization.yaml",
}

func TestManualInternalLinks(t *testing.T) {
	for _, problem := range linkcheck.CheckManual("content", exceptions) {
		t.Error(problem)
	}
}
