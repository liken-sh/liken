package api

// This file defines liken's version grammar and the ordering over it.
//
// A liken version is a calendar date and a serial: yyyy.mm.dd-nnn,
// with every field zero-padded, and the serial starts at 001 and
// counts up within the day. The number says when a release was cut
// and nothing else. liken does not use semantic versioning on
// purpose: an OS release carries a kernel version and a Kubernetes
// version of its own, and a semver major on the outside would only
// invite readings like "liken 2.0 must mean kubernetes 2". There is
// also no compatibility boundary for a major version to mark. Every
// release is expected to take over from the release before it, and
// to carry the machine's layer and on-disk state forward, so
// compatibility is the code's job, not the number's. What shipped
// inside a release is recorded where it belongs, in the release
// document's components.
//
// Versions are immutable: a new serial supersedes a bad release; the
// system never rebuilds a release under the same name. The catalog
// on the Cluster names releases by these versions, and two questions
// need an ordering over them: which catalog entry is the newest (the
// NEWEST printer column), and whether a machine is behind its
// target.

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// versionShape is the grammar, shared verbatim with the Cluster
// CRD's pattern on spec.version and the catalog: a four-digit year,
// a two-digit month and day, a dash, and a three-digit serial.
var versionShape = regexp.MustCompile(`^\d{4}\.\d{2}\.\d{2}-\d{3}$`)

// ValidVersion checks a version against the grammar at authoring
// time. This lets the system refuse a malformed version when it
// bundles a release, rather than let it be discovered when a machine
// fails to fetch it. Beyond the shape, the date must be a real
// calendar date: the CRD's pattern cannot know that 2026.02.30 never
// happened, but this function can.
func ValidVersion(v string) error {
	if !versionShape.MatchString(v) {
		return fmt.Errorf("version %q is not yyyy.mm.dd-nnn (for example 2026.07.11-001)", v)
	}
	date, _, _ := strings.Cut(v, "-")
	if _, err := time.Parse("2006.01.02", date); err != nil {
		return fmt.Errorf("version %q does not name a real date", v)
	}
	return nil
}

// CompareVersions orders two version strings. It returns a negative
// number when a is older, zero when they name the same version, and
// a positive number when a is newer. Every field of the grammar is
// zero-padded to a fixed width, so plain string comparison gives the
// correct order. The padding exists exactly so that no field ever
// needs numeric parsing to sort: as bare numbers, "010" and "9"
// would sort backwards.
func CompareVersions(a, b string) int {
	return strings.Compare(a, b)
}
