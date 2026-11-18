package main

import (
	"fmt"
	"regexp"
	"slices"
)

// A release version is CalVer, yyyy.mm.dd-nnn. A release tag is the
// version with no prefix.
var releaseVersion = regexp.MustCompile(`^[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3}$`)

// releaseTagGlob matches the tags that releaseVersion matches, as a
// glob for git describe.
const releaseTagGlob = "[0-9][0-9][0-9][0-9].[0-9][0-9].[0-9][0-9]-[0-9][0-9][0-9]"

// CheckReleaseTag fails for a tag that is not a release version, and
// for serial 000. The lab uses serial 000 for the bundle it builds from
// the working tree, so no published release may use it.
func CheckReleaseTag(tag string) error {
	if !releaseVersion.MatchString(tag) {
		return fmt.Errorf("the tag %q is not a release version, which looks like 2026.10.02-001", tag)
	}
	if tag[len(tag)-3:] == "000" {
		return fmt.Errorf("the tag %q has serial 000, which the lab uses for the bundle it builds from the working tree, so it cannot be published", tag)
	}
	return nil
}

// DevVersion returns the version of a development build: the newest
// release tag before the commit, the number of commits in the whole
// repository since that tag, and the first eight characters of the
// commit, as in 2026.10.02-001-dev-017-abcdef01. As text, it sorts
// after that release and before the next one.
func DevVersion(tag string, count int, commit string) string {
	return fmt.Sprintf("%s-dev-%03d-%s", tag, count, commit[:8])
}

// NewestRelease returns the newest release version among tags, or ""
// when none is a release version. CalVer with fixed widths sorts as
// text.
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

// NewestReleaseAtOrBefore returns the newest release version among
// tags that is not newer than limit.
func NewestReleaseAtOrBefore(tags []string, limit string) string {
	var older []string
	for _, tag := range tags {
		if tag <= limit {
			older = append(older, tag)
		}
	}
	return NewestRelease(older)
}
