package main

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
)

// imageDrift reports each reference in text to an image of imagesFile
// whose tag differs from the tag in imagesFile. It compares tags only,
// also for a build image that imagesFile pins by digest, because guides
// and tests write images by tag.
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
					findings = append(findings, fmt.Sprintf("%s:%d refers to %s:%s, and %s has %s:%s", file, n+1, m[1], tag, imagesFile, want.Name, want.Tag))
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

func isScript(name string) bool {
	return path.Ext(name) == ".sh"
}

// pinnedImageRef matches an image reference that names a digest, such as
// ghcr.io/oras-project/oras:<tag>@sha256:<hex> or alpine@sha256:<hex>.
// The character before the reference must not be part of a name, so a
// match never begins in the middle of a URL path or a longer name.
var pinnedImageRef = regexp.MustCompile(`(?:^|[^A-Za-z0-9._/:@-])([a-z0-9][a-z0-9._/-]*(?::[A-Za-z0-9_][A-Za-z0-9_.-]*)?@sha256:[0-9a-f]{64})\b`)

// scriptImages reports each image reference with a digest in the
// commands of a shell script. A script that runs an outside image, such
// as cosign or oras, reads the reference from imagesFile with image_of
// from toolchains/pin.sh, so one entry pins the image for every script
// and toolchains/latest.sh reports and bumps it. Comment lines are
// skipped, because a comment may quote a reference.
func scriptImages(file, text string) []string {
	var findings []string
	for _, l := range logicalLines(text) {
		if l.allowed {
			continue
		}
		for _, m := range pinnedImageRef.FindAllStringSubmatch(l.text, -1) {
			findings = append(findings, fmt.Sprintf("%s:%d refers to %s, which is not read from %s. Add the image there and read it with image_of", file, l.n, m[1], imagesFile))
		}
	}
	return findings
}

// isDockerfile is true for Dockerfile and Dockerfile.<name>, and false
// for Dockerfile.<name>.dockerignore, which is an ignore file.
func isDockerfile(name string) bool {
	return name == "Dockerfile" || strings.HasPrefix(name, "Dockerfile.") && !strings.HasSuffix(name, ".dockerignore")
}

// outsideImages reports each FROM and COPY --from in a Dockerfile that
// uses a full image reference, such as ubuntu:24.04@sha256:... A
// Dockerfile must use an outside image by its bare key in imagesFile, so
// one entry pins it for the whole repository, and the bake file or the
// build script resolves the key to the entry's reference. A name with
// ":", "/", or "@" cannot be a key, a repository image, a named build
// context, or a stage. A name with "$" is skipped, because a build
// argument sets it at build time.
func outsideImages(file, text string) []string {
	var findings []string
	parsed := parseDockerfile(text)
	for _, name := range slices.Concat(parsed.From, parsed.Sources) {
		if strings.Contains(name, "$") || !strings.ContainsAny(name, ":/@") {
			continue
		}
		findings = append(findings, fmt.Sprintf("%s builds from %s, which is not a key of %s. Name the image there and build from its key", file, name, imagesFile))
	}
	return findings
}
