package linkcheck

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CheckManual checks every absolute link in the Markdown files under
// contentRoot against the pages and their heading ids, so a broken
// link fails a test before the site is published. It returns one
// message for each broken link, or one message when the content tree
// cannot be read. exceptions lists the link paths that
// have no content file, such as a file that the deploy writes or that
// a module mount serves.
func CheckManual(contentRoot string, exceptions []string) []string {
	excepted := make(map[string]bool, len(exceptions))
	for _, path := range exceptions {
		excepted[path] = true
	}
	pages, err := loadPages(contentRoot)
	if err != nil {
		return []string{fmt.Sprintf("reading the content tree %s: %v", contentRoot, err)}
	}
	files := make([]string, 0, len(pages))
	for file := range pages {
		files = append(files, file)
	}
	sort.Strings(files)
	var problems []string
	for _, file := range files {
		for _, target := range internalLinks(pages[file]) {
			if err := resolveTarget(pages, excepted, target); err != nil {
				problems = append(problems, fmt.Sprintf("%s links %s: %v", file, target, err))
			}
		}
	}
	return problems
}

// loadPages reads every Markdown file under root, keyed by its path
// relative to root.
func loadPages(root string) (map[string][]byte, error) {
	pages := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		page, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		pages[rel] = page
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pages, nil
}

// resolveTarget returns nil when target is an excepted path, a page,
// or a page and the id of one of its headings.
func resolveTarget(pages map[string][]byte, excepted map[string]bool, target string) error {
	path, fragment, _ := strings.Cut(target, "#")
	if excepted[path] {
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
		return fmt.Errorf("%s has no heading with the id %q", path, fragment)
	}
	return nil
}

// pageFor returns the content file that Hugo serves at a URL path.
// Hugo serves docs/guides/install.md at /docs/guides/install/, and a
// section's _index.md at the section's URL, so pageFor tries both.
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
	return nil, fmt.Errorf("no content file serves %s", path)
}
