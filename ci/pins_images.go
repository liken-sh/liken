package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// imageDrift lists each reference in the text to an image of
// images.toml whose tag differs from the entry's. A build image counts
// by its tag alone, because a guide or a test names it by tag.
func imageDrift(file, text string, tools Toolchains) []string {
	var findings []string
	entries := slices.Concat(slices.Collect(maps.Values(tools.Build)), slices.Collect(maps.Values(tools.Run)))
	slices.Sort(entries)
	for _, entry := range entries {
		want := parseImageRef(entry)
		pattern := imagePattern(want.Name)
		for n, line := range strings.Split(text, "\n") {
			for _, m := range pattern.FindAllStringSubmatch(line, -1) {
				tag := strings.TrimRight(m[2], ".-")
				if tag != want.Tag {
					findings = append(findings, fmt.Sprintf("%s:%d names %s:%s, and %s names %s:%s", file, n+1, m[1], tag, imagesFile, want.Name, want.Tag))
				}
			}
		}
	}
	return findings
}

// imagePattern matches a reference to the named image, by any name
// that Docker reads as the same image: debian, library/debian, and
// docker.io/library/debian are one image on Docker Hub. The name must
// start a word, so that ghcr.io/liken-sh/debian is not debian, and a
// tag starts with a letter, a digit, or an underscore, so that
// debian:${VERSION} and golang:<tag> are not references.
func imagePattern(name string) *regexp.Regexp {
	names := []string{regexp.QuoteMeta(name)}
	first, _, nested := strings.Cut(name, "/")
	switch {
	case !nested:
		names = append(names, "library/"+regexp.QuoteMeta(name), "docker\\.io/library/"+regexp.QuoteMeta(name))
	case !strings.ContainsAny(first, ".:") && first != "localhost":
		names = append(names, "docker\\.io/"+regexp.QuoteMeta(name))
	}
	return regexp.MustCompile(`(?:^|[^A-Za-z0-9._/-])(` + strings.Join(names, "|") + `):([A-Za-z0-9_][A-Za-z0-9_.-]*)`)
}
