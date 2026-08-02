package linkcheck

import (
	"reflect"
	"testing"
)

// Goldmark computes a heading's id from its rendered text, so the
// inline syntax has to disappear before Anchor sees it: backticks and
// emphasis markers render as nothing, and a link renders as its text.
func TestStripInline(t *testing.T) {
	for raw, want := range map[string]string{
		"The `liken` command":             "The liken command",
		"Some *emphasis* and _more_":      "Some emphasis and more",
		"A [link](https://example.com/x)": "A link",
		"plain":                           "plain",
	} {
		if got := stripInline(raw); got != want {
			t.Errorf("stripInline(%q) = %q, want %q", raw, got, want)
		}
	}
}

// pageAnchors reads a page the way Goldmark does: front matter and
// fenced code are not headings, and a repeated heading gets a numbered
// id so both remain addressable.
func TestPageAnchorsCollectsHeadingIDs(t *testing.T) {
	page := []byte(`---
title: p
---

# The ` + "`liken`" + ` command

## First, run it

` + "```" + `sh
# a shell comment, not a heading
` + "```" + `

## Repeated

## Repeated

| <span id="spec--features"></span>` + "`features`" + ` | object | no | a row |
`)
	got := pageAnchors(page)
	want := map[string]bool{
		"the-liken-command": true,
		"first-run-it":      true,
		"repeated":          true,
		"repeated-1":        true,
		"spec--features":    true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pageAnchors = %v, want %v", got, want)
	}
}

// internalLinks finds every link whose target starts with a slash:
// inline links, image links, and links that carry a title. External
// links and links inside fenced code are not the manual's to check.
func TestInternalLinksFindsAbsoluteTargets(t *testing.T) {
	page := []byte(`Read [the guide](/docs/guides/install/) and
[one section](/docs/reference/cluster/#spec), but not
[upstream](https://example.com/) or [mail](mailto:x@y.z).

![the mark](/brand/liken.svg "A patch of crustose lichen.")

` + "```" + `
[not real](/docs/nowhere/)
` + "```" + `
`)
	got := internalLinks(page)
	want := []string{
		"/docs/guides/install/",
		"/docs/reference/cluster/#spec",
		"/brand/liken.svg",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("internalLinks = %v, want %v", got, want)
	}
}
