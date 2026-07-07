package machine

// Comparing liken version numbers.
//
// The catalog on the Cluster names releases by version, and two
// questions need an ordering over them: which catalog entry is the
// newest (the NEWEST printer column), and later, whether a machine is
// behind its target. Semantic versioning answers both, and the subset
// liken needs — dotted numeric segments, with an optional pre-release
// suffix that sorts before its release — is small enough to write
// down rather than take a dependency for. What matters is that the
// comparison is numeric: as strings, "0.10.0" sorts before "0.9.0",
// which is exactly the bug that makes naive version comparisons
// famous.

import (
	"strconv"
	"strings"
)

// CompareVersions orders two version strings: negative when a is
// older, zero when they are the same version, positive when a is
// newer. The core segments compare numerically, a missing segment
// counts as zero (1.0 is 1.0.0), and a pre-release (the part after a
// "-") sorts before the release it precedes, pre-releases among
// themselves comparing as plain strings. Full semver splits the
// pre-release into dot-separated identifiers with their own numeric
// rules; liken's releases don't lean on that, so string comparison
// serves.
func CompareVersions(a, b string) int {
	aCore, aPre, _ := strings.Cut(a, "-")
	bCore, bPre, _ := strings.Cut(b, "-")

	aParts := strings.Split(aCore, ".")
	bParts := strings.Split(bCore, ".")
	for i := 0; i < max(len(aParts), len(bParts)); i++ {
		if c := compareSegments(segment(aParts, i), segment(bParts, i)); c != 0 {
			return c
		}
	}

	switch {
	case aPre == bPre:
		return 0
	case aPre == "":
		return 1
	case bPre == "":
		return -1
	}
	return strings.Compare(aPre, bPre)
}

func segment(parts []string, i int) string {
	if i >= len(parts) {
		return "0"
	}
	return parts[i]
}

// compareSegments compares one dotted segment numerically, falling
// back to string order when a segment isn't a number at all — a
// malformed version still gets a stable, documented ordering instead
// of a panic.
func compareSegments(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	if aerr != nil || berr != nil {
		return strings.Compare(a, b)
	}
	switch {
	case an < bn:
		return -1
	case an > bn:
		return 1
	}
	return 0
}

// NewestVersion is the catalog's highest version, "" when the catalog
// is empty. The fleet sweep publishes this as status.releases.newest,
// which exists so a printer column can answer "is there something
// newer than the target?" at a glance.
func NewestVersion(catalog []ReleaseCatalogEntry) string {
	newest := ""
	for _, entry := range catalog {
		if newest == "" || CompareVersions(entry.Version, newest) > 0 {
			newest = entry.Version
		}
	}
	return newest
}
