package main

import "regexp"

// sha256Hex matches a SHA-256 digest. A release lists a SHA-1 and a
// SHA-256 in one Checksum array, and the length tells them apart.
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

type fwupdRelease struct {
	AppstreamID string   `json:"AppstreamId"`
	ReleaseID   string   `json:"ReleaseId"`
	Version     string   `json:"Version"`
	Urgency     string   `json:"Urgency"`
	Checksum    []string `json:"Checksum"`
	Locations   []string `json:"Locations"`
	Size        int64    `json:"Size"`
}

// toRelease converts a release of get-updates. The cabinet carries the
// first location and the SHA-256 digest, which is what a later download
// checks the file against. A release with no location has no cabinet.
func toRelease(r fwupdRelease) Release {
	release := Release{
		Version: r.Version, ID: r.AppstreamID, ReleaseID: r.ReleaseID, Urgency: r.Urgency,
	}
	if len(r.Locations) == 0 {
		return release
	}
	release.Cabinet = &Cabinet{URL: r.Locations[0], Size: r.Size}
	for _, sum := range r.Checksum {
		if sha256Hex.MatchString(sum) {
			release.Cabinet.SHA256 = sum
		}
	}
	return release
}
