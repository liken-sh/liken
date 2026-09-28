package main

import (
	"fmt"
	"regexp"
	"slices"
)

// A release version is CalVer, yyyy.mm.dd-nnn, and the repository's
// release tag is the bare version.
var releaseVersion = regexp.MustCompile(`^[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3}$`)

// releaseTagGlob is the same grammar as a git glob, for git describe.
const releaseTagGlob = "[0-9][0-9][0-9][0-9].[0-9][0-9].[0-9][0-9]-[0-9][0-9][0-9]"

// CheckReleaseTag refuses a tag that is not a release version. Serial
// 000 is the name of the working tree's own channel, so it never
// publishes.
func CheckReleaseTag(tag string) error {
	if !releaseVersion.MatchString(tag) {
		return fmt.Errorf("the tag %q is not a release version; a release tag looks like 2026.10.02-001", tag)
	}
	if tag[len(tag)-3:] == "000" {
		return fmt.Errorf("the tag %q has serial 000, which names the working tree's channel and never publishes", tag)
	}
	return nil
}

// DevVersion names a development build: the newest release tag before
// it, the number of commits since that tag, and the first eight
// characters of the commit, as in 2026.10.02-001-dev-017-abcdef01. The
// count is for the whole repository. It sorts after its release and
// before the next one.
func DevVersion(tag string, count int, commit string) string {
	return fmt.Sprintf("%s-dev-%03d-%s", tag, count, commit[:8])
}

// NewestRelease is the newest release version among tags, or "" when
// none is a release version. CalVer with fixed widths sorts as text.
func NewestRelease(tags []string) string {
	var releases []string
	for _, tag := range tags {
		if releaseVersion.MatchString(tag) {
			releases = append(releases, tag)
		}
	}
	if len(releases) == 0 {
		return ""
	}
	return slices.Max(releases)
}

// NewestReleaseAtOrBefore is the newest release version among tags
// that is not newer than limit.
func NewestReleaseAtOrBefore(tags []string, limit string) string {
	var older []string
	for _, tag := range tags {
		if tag <= limit {
			older = append(older, tag)
		}
	}
	return NewestRelease(older)
}
