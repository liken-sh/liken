package linkcheck

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The content tree, relative to this package. The two generated
// reference pages land in it too, so one walk covers the whole
// manual.
const contentRoot = "../content"

// The reference pages are build products, so a fresh checkout does
// not have them, and a link into them cannot be checked without them.
var generatedPages = []string{
	"docs/reference/machine.md",
	"docs/reference/cluster.md",
}

// Absolute links that are not content pages. Each one is a build
// product or a copied asset, so no content file answers for it; this
// list says where each target really comes from.
var servedAssets = map[string]string{
	// layouts/home.llms.txt renders the home page as this output.
	"/llms.txt": "layouts/home.llms.txt",
	// layouts/home.llms-full.txt renders the whole manual as one file.
	"/llms-full.txt": "layouts/home.llms-full.txt",
	// The Makefile copies ../brand/liken.svg into static/brand/.
	"/brand/liken.svg": "the brand copy in the Makefile",
	// The Makefile writes the deploy marker into the built tree.
	"/release.txt": "the build rule in the Makefile",
}

// TestManualInternalLinks resolves every absolute link in the manual:
// a page link must name a content file, and a fragment must name a
// heading id in its target page. The ids come from Anchor, the same
// function crdref links with, so the check and the generator cannot
// disagree.
func TestManualInternalLinks(t *testing.T) {
	pages := manualPages(t)
	files := make([]string, 0, len(pages))
	for file := range pages {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		for _, target := range internalLinks(pages[file]) {
			if err := resolveTarget(pages, target); err != nil {
				t.Errorf("%s links %s: %v", file, target, err)
			}
		}
	}
}

// manualPages loads every Markdown file in the content tree, keyed by
// its path relative to the tree. The generated pages must be present
// first: without them their links dangle and links into them cannot
// resolve, and neither failure would mean what it says.
func manualPages(t *testing.T) map[string][]byte {
	t.Helper()
	for _, page := range generatedPages {
		if _, err := os.Stat(filepath.Join(contentRoot, page)); err != nil {
			t.Fatalf("%s is missing; run `make -C docs generate` first", page)
		}
	}
	pages := map[string][]byte{}
	err := filepath.WalkDir(contentRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		page, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(contentRoot, path)
		if err != nil {
			return err
		}
		pages[rel] = page
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return pages
}

// resolveTarget checks one absolute link target against the loaded
// pages. A target is either a served asset, a page, or a page with a
// fragment; anything else is a broken link.
func resolveTarget(pages map[string][]byte, target string) error {
	path, fragment, _ := strings.Cut(target, "#")
	if _, ok := servedAssets[path]; ok {
		return nil
	}
	page, err := pageFor(pages, path)
	if err != nil {
		return err
	}
	if fragment == "" {
		return nil
	}
	if !pageAnchors(page)[fragment] {
		return fmt.Errorf("no heading in %s renders the id %q", path, fragment)
	}
	return nil
}

// pageFor maps a URL path to its content file. Hugo renders
// content/docs/guides/install.md at /docs/guides/install/, and a
// section's _index.md at the section's own URL, so both spellings
// answer for a path.
func pageFor(pages map[string][]byte, path string) ([]byte, error) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		trimmed = "_index"
	}
	if page, ok := pages[trimmed+".md"]; ok {
		return page, nil
	}
	if page, ok := pages[filepath.Join(trimmed, "_index.md")]; ok {
		return page, nil
	}
	return nil, fmt.Errorf("no content file answers for %s", path)
}
