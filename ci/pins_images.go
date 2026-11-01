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

// isDockerfile is true for a Dockerfile: Dockerfile, or Dockerfile.<name>
// beside it. A <Dockerfile>.dockerignore is the ignore file of one.
func isDockerfile(name string) bool {
	return name == "Dockerfile" || strings.HasPrefix(name, "Dockerfile.") && !strings.HasSuffix(name, ".dockerignore")
}

// outsideImages lists each image that a Dockerfile builds from or
// copies from by a full reference, such as ubuntu:24.04@sha256:... A
// Dockerfile names an outside image bare, by its key in images.toml, so
// that one entry pins it for the whole repository, toolchains/latest.sh
// reports it, and the bake file or the script that builds the file
// resolves the key to the entry's reference. A bare name is a key of
// images.toml, an image of the repository, a named build context, or a
// stage; a name with a registry, a tag, or a digest is none of those. A
// build argument fills its name at build time, so the check cannot read
// it.
func outsideImages(file, text string) []string {
	var findings []string
	parsed := parseDockerfile(text)
	for _, name := range slices.Concat(parsed.From, parsed.Sources) {
		if strings.Contains(name, "$") || !strings.ContainsAny(name, ":/@") {
			continue
		}
		findings = append(findings, fmt.Sprintf("%s builds from %s, which is not a key of %s; name the image there and build from its key", file, name, imagesFile))
	}
	return findings
}
