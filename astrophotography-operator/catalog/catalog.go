// Package catalog defines a target's frame catalog: one JSON Lines file
// for each target, at <archive.path>/catalog/<Target>.jsonl. The
// promotion Job is the only writer, and it appends one line for each
// frame that it promotes to the archive.
//
// The file is stored beside the frames, and a person may read it with
// jq long after the cluster and the operator are gone. So a field keeps
// its name and meaning once written, and no line is ever rewritten.
//
// Field names are camelCase English for what the value means, not the
// FITS keyword it came from, so a reader does not need the FITS
// standard. A quantity whose unit could be misread has the unit at the
// end of its name, as in exposureSeconds and sizeBytes. "ID" is
// capitalized as in Kubernetes, as in frameID.
package catalog

import "path"

// Dir is the catalog's directory under archive.path. The Target CRD
// refuses an archive.format that starts with it, so no frame is stored
// there.
const Dir = "catalog"

// Path returns a target's catalog file, relative to the root of the
// archive claim.
func Path(archivePath, target string) string {
	return path.Join(archivePath, Dir, target+".jsonl")
}

// RecordFrame is the Record value of a line that describes a frame.
// A later fact about a frame, such as a grade, must be a new line with
// its own Record value and the same FrameID, so the frame's line never
// changes.
const RecordFrame = "frame"

type Line struct {
	// Read skips a line whose Record is not RecordFrame.
	Record string `json:"record"`
	// FrameID is the FRAMEID header value.
	FrameID string `json:"frameID"`
	// The resources that produced the frame.
	Target      string `json:"target"`
	Reservation string `json:"reservation"`
	Telescope   string `json:"telescope"`
	Camera      string `json:"camera"`
	Train       string `json:"train,omitempty"`
	// Path is the frame's file relative to archive.path, so the line
	// stays correct when a person moves the whole archive.
	Path string `json:"path"`
	// SizeBytes and SHA256 describe the whole file. The promotion Job
	// checks the archived file against them.
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
	// StartTime is the DATE-OBS header value, the start of the exposure
	// by the driver's clock, in RFC 3339 in UTC with milliseconds, such
	// as 2026-10-12T20:08:26.864Z. It is omitted when the header has no
	// DATE-OBS that parses as a time.
	StartTime string `json:"startTime,omitempty"`
	Settings
}

// Settings are the header values that decide which sequence a frame
// counts toward: the sequence with the same filter, exposure, binning,
// gain, and offset. The promotion report totals frames by Settings too.
type Settings struct {
	// Filter is the FILTER header value, empty when the header has none.
	Filter string `json:"filter,omitempty"`
	// ExposureSeconds is the EXPTIME header value.
	ExposureSeconds float64 `json:"exposureSeconds"`
	// Binning is the XBINNING header value, or 1 when the header has
	// none. The runner uses the same binning on both axes.
	Binning int32 `json:"binning"`
	// Gain and Offset are the GAIN and OFFSET header values, omitted when
	// the header has none.
	Gain   *float64 `json:"gain,omitempty"`
	Offset *float64 `json:"offset,omitempty"`
}
