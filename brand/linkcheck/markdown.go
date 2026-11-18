package linkcheck

// The link check reads the manual's Markdown the way Hugo's renderer,
// Goldmark, does: front matter and fenced code contain no headings and
// no links, a heading's id comes from its rendered text, and a
// repeated heading gets a numbered id. This file parses only the
// Markdown that the manual uses: ATX headings, inline links, and
// backtick fences.

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// A link renders as its text, so the id of a heading that
	// contains one comes from the text and ignores the target.
	inlineLink = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)

	// Underscore emphasis starts and ends only at a word boundary.
	// An underscore inside a word is not emphasis, and Anchor keeps
	// it.
	underscoreEmphasis = regexp.MustCompile(`(^|[\s(])_+([^_]+?)_+($|[\s).,:;!?])`)

	atxHeading = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*$`)

	// The generated reference pages put a raw-HTML span with an id in
	// each field's table row, so a link can point at one field. Those
	// ids are link targets like heading ids.
	explicitID = regexp.MustCompile(`\bid="([^"]+)"`)

	// A heading can end in an attribute block such as
	// {data-method=GET}, which apiref writes on each route heading.
	// The renderer removes the block before it computes the id. The
	// pattern matches only a block with an id, a class, or a key=value
	// pair, because a route path can end in a template such as {name},
	// which is heading text.
	headingAttributes = regexp.MustCompile(`\s*\{\s*(?:[#.][^{}]*|[^{}]*=[^{}]*)\}\s*$`)

	// The pattern starts at the closing bracket of the link text, so it
	// also matches a link whose text wraps across lines. Its groups are
	// the opening "](", the target, and the optional title with the
	// closing parenthesis, so RewriteTargets can replace the target
	// alone.
	linkTarget = regexp.MustCompile(`(\]\(\s*)([^)\s]*)((?:\s+"[^"]*")?\))`)
)

// stripInline returns the text a heading line renders. It removes the
// attribute block, backticks, and asterisks, replaces a link with its
// text, and removes underscore emphasis markers.
func stripInline(s string) string {
	s = headingAttributes.ReplaceAllString(s, "")
	s = inlineLink.ReplaceAllString(s, "$1")
	s = strings.NewReplacer("`", "", "*", "").Replace(s)
	s = underscoreEmphasis.ReplaceAllString(s, "$1$2$3")
	return s
}

// proseLines returns the lines of a page that the renderer reads as
// Markdown. It leaves out the front matter, the fenced code blocks,
// and the fence lines.
func proseLines(page []byte) []string {
	lines := strings.Split(string(page), "\n")
	if len(lines) > 0 && lines[0] == "---" {
		for i := 1; i < len(lines); i++ {
			if lines[i] == "---" {
				lines = lines[i+1:]
				break
			}
		}
	}
	var prose []string
	inFence := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			prose = append(prose, line)
		}
	}
	return prose
}

// pageAnchors returns every id that the rendered page defines, from
// headings and from explicit id attributes. When two headings render
// to the same id, the renderer appends -1, -2, and so on to the later
// ones, and the result contains each of those ids.
func pageAnchors(page []byte) map[string]bool {
	ids := map[string]bool{}
	for _, line := range proseLines(page) {
		for _, m := range explicitID.FindAllStringSubmatch(line, -1) {
			ids[m[1]] = true
		}
		m := atxHeading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		id := Anchor(stripInline(m[2]))
		for i := 1; ids[id]; i++ {
			id = fmt.Sprintf("%s-%d", Anchor(stripInline(m[2])), i)
		}
		ids[id] = true
	}
	return ids
}

// RewriteTargets replaces every link target on one line with the
// result of rewrite. The link check and the skills generator both use
// it, so they parse links the same way. rewrite returns a target
// unchanged to keep it.
func RewriteTargets(line string, rewrite func(target string) string) string {
	return linkTarget.ReplaceAllStringFunc(line, func(link string) string {
		m := linkTarget.FindStringSubmatch(link)
		return m[1] + rewrite(m[2]) + m[3]
	})
}

// internalLinks returns the link targets on the page that start with a
// slash, in document order, with their fragments.
func internalLinks(page []byte) []string {
	var targets []string
	for _, line := range proseLines(page) {
		RewriteTargets(line, func(target string) string {
			if strings.HasPrefix(target, "/") {
				targets = append(targets, target)
			}
			return target
		})
	}
	return targets
}
