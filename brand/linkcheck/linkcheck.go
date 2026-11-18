// Package linkcheck checks the internal links of a manual built with
// Hugo.
//
// Manual pages link to each other, and often to one section of a
// generated reference page. Such a link ends in a heading id that
// Hugo computes when it builds the site, so nothing reports a wrong
// link until the site is built. This package reimplements Hugo's
// heading id, so that crdref and apiref can write links that match,
// and CheckManual resolves every internal link in a content tree
// against the headings it points to.
package linkcheck

import "unicode"

// Anchor returns the id Hugo gives a heading with this rendered text.
// Hugo's default is the GitHub-style id of Goldmark: it lowercases
// the text, keeps letters, digits, and underscores, turns each space
// and hyphen into a hyphen, and drops every other character. No
// specification defines this, so the test cases are ids copied from a
// site built with the pinned Hugo binary.
//
// The text must be the rendered heading, without inline Markdown. A
// caller that has the raw heading line removes the backticks,
// emphasis, and link syntax first.
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
