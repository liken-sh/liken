package linkcheck

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CheckManual resolves every internal link in one site's content
// tree. It walks contentRoot, collects each page's heading ids, and
// checks every absolute link target against them, so a broken deep
// link fails at authoring time instead of on the served site. It
// returns one message per broken link, and none when every link
// resolves. exceptions are the absolute link paths no content file
// answers for, such as a build product the deploy stamps or a file
// a module mount serves; each site passes its own list.
func CheckManual(contentRoot string, exceptions []string) []string {
	excepted := make(map[string]bool, len(exceptions))
	for _, path := range exceptions {
		excepted[path] = true
	}
	pages, err := loadPages(contentRoot)
	if err != nil {
		return []string{fmt.Sprintf("cannot read the content tree at %s: %v", contentRoot, err)}
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

// loadPages reads every Markdown file under root, keyed by its
// path relative to root, which is the form the link targets
// resolve against.
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

// resolveTarget checks one absolute link target against the loaded
// pages. A target is either a served asset, a page, or a page with a
// fragment; anything else is a broken link.
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
