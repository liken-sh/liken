package schema

import (
	"regexp"
	"strings"
)

// width is the column that a folded description wraps at, the width of
// the CRDs that people wrote by hand before they were generated.
const width = 72

// wrap breaks text into lines of at most width columns after an
// indent, filling each line with as many words as fit. A word longer
// than a line gets a line of its own. The narrowest line holds 32
// columns of text, so a deep field still reads as prose.
func wrap(text string, indent int) []string {
	room := max(width-indent, 32)
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) <= room:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// folded is the key of a folded block scalar, such as "description:
// >-". YAML joins the lines of such a block with single spaces, so its
// words can be wrapped again without a change of meaning.
var folded = regexp.MustCompile(`^( *)(- )?[A-Za-z][A-Za-z0-9-]*: >-$`)

// tidy removes the lines that hold only spaces, which the template
// directives leave behind, and wraps each folded block again for the
// indentation it ends up at. A fragment that one template includes at
// two depths then wraps the same way as a field written at either
// depth.
func tidy(rendered string) string {
	var out []string
	lines := strings.Split(rendered, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
		match := folded.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		keyIndent := len(match[1]) + len(match[2])
		var words []string
		contentIndent := -1
		for i+1 < len(lines) {
			next := lines[i+1]
			indent := len(next) - len(strings.TrimLeft(next, " "))
			if strings.TrimSpace(next) == "" || indent <= keyIndent {
				break
			}
			if contentIndent < 0 {
				contentIndent = indent
			}
			words = append(words, strings.TrimSpace(next))
			i++
		}
		for _, w := range wrap(strings.Join(words, " "), contentIndent) {
			out = append(out, strings.Repeat(" ", contentIndent)+w)
		}
	}
	return strings.Join(out, "\n") + "\n"
}
