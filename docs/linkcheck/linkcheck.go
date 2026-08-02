// Package linkcheck owns the manual's internal links.
//
// The manual deep-links across its own pages: a guide points at one
// section of a generated reference page, and the front page points at
// the guides. Every one of those links names a heading id that Hugo
// computes at build time, so nothing checks them at authoring time.
// This package holds the one implementation of Hugo's id algorithm,
// which crdref uses to emit matching links, and a test that resolves
// every internal link in the content tree against the headings it
// targets.
package linkcheck

import "unicode"

// Anchor turns a heading's rendered text into the id Hugo gives it.
// Hugo's default is Goldmark's GitHub-style autoHeadingID: lowercase
// the text, keep letters, digits, and underscores, turn each space
// and each hyphen into a hyphen, and drop everything else. The cases
// in the test mirror ids read from a site built with the pinned Hugo
// binary, so this function follows the renderer, not a specification.
//
// The id comes from the rendered text, after inline Markdown is gone.
// A caller with a raw heading line strips the backticks, emphasis,
// and link syntax first.
func Anchor(text string) string {
	var b []rune
	for _, r := range text {
		switch {
		case r == ' ' || r == '-':
			b = append(b, '-')
		case r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b = append(b, unicode.ToLower(r))
		}
	}
	return string(b)
}
