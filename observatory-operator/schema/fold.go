package schema

import (
	"regexp"
	"strings"
)

// width is the column at which a folded description wraps.
const width = 72

// wrap breaks text into lines that fit in width columns after indent
// spaces. A line always has room for at least 32 columns of text, so a
// deeply indented field still wraps at a readable length. A word longer
// than a line gets a line of its own.
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

// folded matches the key line of a folded block scalar, such as
// "description: >-". YAML joins the lines of such a block with single
// spaces, so the lines can be wrapped again without changing the
// value.
var folded = regexp.MustCompile(`^( *)(- )?[A-Za-z][A-Za-z0-9-]*: >-$`)

// tidy removes the blank lines that template directives leave in the
// rendered YAML, and wraps each folded block again at its indentation
// in the output. A fragment that a template includes at two depths
// then wraps correctly at both.
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
