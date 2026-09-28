// The generator: from one guide's Markdown to one SKILL.md, and from
// a site's guides directory to the skills directory this program
// owns.
//
// A guide already has what a skill needs: the steps, the exact
// commands, and the checks. The one thing it lacks is a trigger
// line, and the one thing an agent lacks is the site, because it
// reads the skill from a checkout. So the front matter gains a
// required description, and every link becomes a full URL. Nothing
// else about the guide changes, so the guide stays the one source.
package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/liken-sh/brand/linkcheck"
)

// preamble opens every skill. It names where the guide lives on the
// site, and it tells the agent to check the kubectl context before
// the first command, because a workstation with two contexts is the
// usual case and the wrong cluster is the first mistake an agent
// makes.
const preamble = "This skill is the guide at %s, emitted for agents. " +
	"Before the first command, run `kubectl config current-context` " +
	"and confirm that it names the cluster the person means."

// skillName is the Agent Skills rule for a name: lowercase letters,
// digits, and single hyphens between them. The slug is both the
// skill's name and its directory, and the specification says the
// two must match, so the rule applies to the guide's file name.
var skillName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const maxNameLength = 64

// Generate renders one guide as one SKILL.md. The slug is the guide
// file's base name, and it becomes the skill's name. The front
// matter's title and weight are dropped: the body already carries
// the H1, and a skill has no order in a section. The description is
// required, because the trigger line is the one thing a skill has
// that a guide does not. base is the site's base URL, which every
// link in the body resolves against.
func Generate(guide []byte, slug string, base string) ([]byte, error) {
	file := slug + ".md"
	if len(slug) > maxNameLength || !skillName.MatchString(slug) {
		return nil, fmt.Errorf("%s: name %q is not a skill name "+
			"(lowercase letters, digits, and single hyphens; at most 64 characters)", file, slug)
	}

	base = strings.TrimSuffix(base, "/")
	page, err := url.Parse(base + "/docs/guides/" + slug + "/")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}

	matter, body := splitFrontMatter(guide)
	var front struct {
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(matter), &front); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if strings.TrimSpace(front.Description) == "" {
		return nil, fmt.Errorf("%s has no description", file)
	}

	var b strings.Builder
	// A double-quoted YAML scalar carries any description, colons
	// and quotation marks included, and Go's quoting rules produce
	// one that YAML reads back unchanged.
	fmt.Fprintf(&b, "---\nname: %s\ndescription: %s\n---\n\n", slug, strconv.Quote(front.Description))
	fmt.Fprintf(&b, preamble+"\n\n", page)
	b.WriteString(strings.TrimLeft(rewriteLinks(body, base, page), "\n"))
	return []byte(strings.TrimRight(b.String(), "\n") + "\n"), nil
}

// Emit writes one skill for every guide in guidesDir, then removes
// every other skill directory in outDir. This program owns outDir,
// so a renamed or deleted guide leaves no stale skill. The section
// page _index.md, files that are not Markdown, and subdirectories
// are not guides, so Emit skips them and does not report them.
func Emit(guidesDir, outDir, base string) error {
	guides, err := os.ReadDir(guidesDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	written := map[string]bool{}
	for _, guide := range guides {
		if guide.IsDir() || filepath.Ext(guide.Name()) != ".md" || guide.Name() == "_index.md" {
			continue
		}
		slug := strings.TrimSuffix(guide.Name(), ".md")
		source, err := os.ReadFile(filepath.Join(guidesDir, guide.Name()))
		if err != nil {
			return err
		}
		skill, err := Generate(source, slug, base)
		if err != nil {
			return err
		}
		dir := filepath.Join(outDir, slug)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), skill, 0o644); err != nil {
			return err
		}
		written[slug] = true
	}
	return sweep(outDir, written)
}

// sweep removes the directories in outDir that this run did not
// write. It removes only directories: a file at the top of outDir,
// such as a README, is the repository's, not this program's.
func sweep(outDir string, written map[string]bool) error {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || written[entry.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(outDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// splitFrontMatter divides a guide at the front matter's closing
// --- line. A file with no front matter, or with no closing line, is
// all body, and Generate then refuses it for having no description.
func splitFrontMatter(guide []byte) (matter, body string) {
	lines := strings.Split(string(guide), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return "", string(guide)
	}
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n")
		}
	}
	return "", string(guide)
}

// rewriteLinks rewrites the link targets on the body's prose lines
// and returns every line, fences included, because the output is the
// guide itself. A fenced block holds commands or output, so its text
// stays as written, the same rule linkcheck applies when it scans.
func rewriteLinks(body, base string, page *url.URL) string {
	lines := strings.Split(body, "\n")
	inFence := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lines[i] = linkcheck.RewriteTargets(line, func(target string) string {
			return rewriteTarget(target, base, page)
		})
	}
	return strings.Join(lines, "\n")
}

// rewriteTarget turns a site link into a full URL, because an agent
// reads the skill from disk, where a site path resolves to nothing.
// An absolute path joins the base, and a relative path resolves
// against the guide's own page. A fragment, an empty target, and a
// target that already has a scheme or a host say where they point
// and stay as they are.
func rewriteTarget(target, base string, page *url.URL) string {
	if target == "" || strings.HasPrefix(target, "#") {
		return target
	}
	relative, err := url.Parse(target)
	if err != nil || relative.Scheme != "" || relative.Host != "" {
		return target
	}
	if strings.HasPrefix(target, "/") {
		return base + target
	}
	return page.ResolveReference(relative).String()
}
