// Package catalog holds the line of a target's frame catalog (plan 01).
// The catalog is one JSON Lines file for each target, in the target's
// archive at <archive.path>/catalog/<Target>.jsonl. The promotion Job
// appends one line when a frame reaches the archive and is verified,
// and is the only writer. The file outlives the cluster and the
// operator, beside the frames it describes, so its lines are a format
// that a person reads with jq years later: a field once written keeps
// its name and its meaning.
package catalog

import "path"

// Dir is the catalog's directory under the archive's path. A target's
// archive.format cannot start with it, so no frame lands there.
const Dir = "catalog"

// Path answers the catalog of a target, relative to the archive
// claim's root.
func Path(archivePath, target string) string {
	return path.Join(archivePath, Dir, target+".jsonl")
}

// RecordFrame is the record of a Line that describes a frame. Later
// records about a frame, such as a grade from plan 06, are new lines
// keyed by its FRAMEID with a record of their own, so a frame's line
// never changes and a reader skips a record it does not know.
const RecordFrame = "frame"

// Line is one line of the catalog.
type Line struct {
	Record  string `json:"record"`
	FrameID string `json:"frameID"`
	// The resources that took the frame.
	Target      string `json:"target"`
	Reservation string `json:"reservation"`
	Telescope   string `json:"telescope"`
	Camera      string `json:"camera"`
	Train       string `json:"train,omitempty"`
	// Path is the frame's file relative to archive.path, so the catalog
	// stays true when a person moves the whole archive.
	Path string `json:"path"`
	// Size and SHA256 are of the whole file, which the promotion Job
	// hashed as it copied and again when it read the copy back.
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// DateObs is DATE-OBS from the header as the driver wrote it, such
	// as 2026-10-12T20:08:26.864, in UTC with no zone.
	DateObs string `json:"dateObs"`
	Settings
}

// Settings are the header's values that decide which sequence a frame
// counts toward: a frame counts toward a sequence with the same
// filter, exposure, binning, gain, and offset (plan 01). The promotion
// report's totals are by Settings too.
type Settings struct {
	// Filter is FILTER, empty when the header has none.
	Filter string `json:"filter,omitempty"`
	// Exposure is EXPTIME, in seconds.
	Exposure float64 `json:"exptime"`
	// Binning is XBINNING.
	Binning int32 `json:"xbinning"`
	// Gain and Offset are GAIN and OFFSET, absent when the header has
	// none.
	Gain   *float64 `json:"gain,omitempty"`
	Offset *float64 `json:"offset,omitempty"`
}
