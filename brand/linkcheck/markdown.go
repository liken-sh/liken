package linkcheck

// The manual is Markdown, and the link check reads it the way the
// renderer does: front matter and fenced code hold no headings and no
// links, a heading's id comes from its rendered text, and a repeated
// heading gets a numbered id. This file holds that reading. It covers
// the Markdown this manual writes, not the whole of CommonMark: ATX
// headings, inline links, and backtick fences are the grammar in use.

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// A whole inline link renders as its text, so the id of a heading
	// that contains one keeps the text and drops the target.
	inlineLink = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)

	// Underscore emphasis only opens and closes at a word's edge, so
	// an underscore inside a word, which Anchor keeps, is unchanged.
	underscoreEmphasis = regexp.MustCompile(`(^|[\s(])_+([^_]+?)_+($|[\s).,:;!?])`)

	atxHeading = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*$`)

	// The generated reference pages put an id on each field's table
	// row as a raw-HTML span, so a link can land on one field. Those
	// ids are link targets exactly like heading ids.
	explicitID = regexp.MustCompile(`\bid="([^"]+)"`)

	// A heading may end in an attribute block, which sets attributes
	// on the rendered heading and renders no text of its own. apiref
	// writes one on every route heading to hold the method. The
	// renderer takes the block off before it computes the id, so this
	// reading takes it off too. A block counts only when it holds an
	// id, a class, or a key and a value, which is what the renderer
	// accepts. A route path ends in a braced template such as
	// {name}, and that is text.
	headingAttributes = regexp.MustCompile(`\s*\{\s*(?:[#.][^{}]*|[^{}]*=[^{}]*)\}\s*$`)

	// A link's target is at its end, so matching from the closing
	// bracket finds a link whose text wraps across lines. The three
	// groups are the opening, the target, and the optional quoted
	// title with the closing parenthesis, so a caller can put a new
	// target between the outer two. Every target matches, external
	// ones included; the scan below keeps the absolute ones.
	linkTarget = regexp.MustCompile(`(\]\(\s*)([^)\s]*)((?:\s+"[^"]*")?\))`)
)

// stripInline reduces a heading line to its rendered text: an
// attribute block and the backticks and asterisks render as nothing,
// a link renders as its text, and underscore emphasis renders as its
// content.
func stripInline(s string) string {
	s = headingAttributes.ReplaceAllString(s, "")
	s = inlineLink.ReplaceAllString(s, "$1")
	s = strings.NewReplacer("`", "", "*", "").Replace(s)
	s = underscoreEmphasis.ReplaceAllString(s, "$1$2$3")
	return s
}

// proseLines returns the lines the renderer reads as prose: the front
// matter block and every fenced code block drop out. A fence line
// itself belongs to no one, so it drops too.
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

// pageAnchors returns every heading id the rendered page has.
// When two headings render to the same id, the renderer numbers the
// later ones, so both stay addressable and this map holds both.
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

// RewriteTargets replaces every link target on one line with what
// rewrite returns for it. This is the one place that knows what a
// Markdown link is, so the link check and the skills generator read
// the same grammar. The caller decides which targets change and
// returns the rest as they are.
func RewriteTargets(line string, rewrite func(target string) string) string {
	return linkTarget.ReplaceAllStringFunc(line, func(link string) string {
		m := linkTarget.FindStringSubmatch(link)
		return m[1] + rewrite(m[2]) + m[3]
	})
}

// internalLinks returns every absolute link target on the page, in
// document order, fragments included.
func internalLinks(page []byte) []string {
	var targets []string
	for _, line := range proseLines(page) {
		RewriteTargets(line, func(target string) string {
			// An external target starts with its scheme, not a slash,
			// so the leading slash is what makes a link the manual's
			// own to check.
			if strings.HasPrefix(target, "/") {
				targets = append(targets, target)
			}
			return target
		})
	}
	return targets
}
