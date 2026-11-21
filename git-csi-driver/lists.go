package main

// lists.go splits the values of the driver's list parameters and
// attributes.

import "strings"

// splitList splits a list value on commas and on newlines, trims the
// spaces around each entry, and drops the empty entries. A YAML block
// scalar puts one entry on each line, and a one-line value separates
// them with commas, so a volume or a class can use either form. The
// `ignore` parameter of a class and the `paths` attribute of a volume
// share this splitter, so the two accept the same forms.
func splitList(value string) []string {
	entries := []string{}
	for _, entry := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	}) {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			entries = append(entries, trimmed)
		}
	}
	return entries
}
