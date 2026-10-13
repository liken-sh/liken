// Package promote is the binary's promote mode: the container of a
// promotion Job, which moves a target's frames from an outbox to the
// target's archive, records each one in the catalog, and reports
// (plan 03). This file holds the report, the contract between the Job
// and the operator.
package promote

import (
	"encoding/json"

	"github.com/liken-sh/liken/astrophotography-operator/catalog"
)

// The Job's container writes its report to the termination message,
// and the operator reads it from the pod's status when the Job ends.
// The kubelet keeps at most MaxReportBytes of the message.
const (
	TerminationLog = "/dev/termination-log"
	MaxReportBytes = 4096
)

// MaxTotals is how many totals a report holds. 25 totals of a long
// filter name take about 2,500 bytes, so they fit in MaxReportBytes
// beside a few frames that wait or failed. A target with more
// combinations of settings reports the first MaxTotals and sets
// Truncated.
const MaxTotals = 25

// Report is what one promotion Job tells the operator.
type Report struct {
	// Totals are the catalog's counts of frames, by their settings, from
	// a read of the whole catalog and not from this run alone. So a
	// report that the operator never reads is corrected by the next one.
	Totals    []Total `json:"totals"`
	Truncated bool    `json:"truncated,omitempty"`
	// Promoted and Bytes count the frames that this run moved out of the
	// outbox and the bytes that it copied.
	Promoted int32 `json:"promoted"`
	Bytes    int64 `json:"bytes"`
	// Left counts the frames of the target that this run left in the
	// outbox: each frame that waits as incomplete, and each failure. A
	// file with no FRAMEID is not this operator's frame, and Left does
	// not count it. Waiting and Failures can list fewer, to fit the
	// termination message.
	Left int32 `json:"left"`
	// Waiting lists the frames that this run left in the outbox with no
	// fault, and Failures each frame that this run failed to promote.
	Waiting  []Frame `json:"waiting,omitempty"`
	Failures []Frame `json:"failures,omitempty"`
}

// Total counts the catalog's frames of one combination of settings.
type Total = catalog.Total

// Frame is a frame that stayed in the outbox, and why.
type Frame struct {
	// File is the frame's path relative to the outbox's root
	// (outbox.Frame.Path).
	File string `json:"file"`
	// Reason is ReasonIncomplete or ReasonNoFrameID for a frame that
	// waits, and one of the False reasons of the Target's
	// ArchiveAvailable for a failure: ReasonCopyFailed,
	// ReasonVerifyFailed, or ReasonNameConflict.
	Reason  string `json:"reason"`
	Message string `json:"message,omitempty"`
}

// The reasons a frame waits.
const (
	// ReasonIncomplete is a frame shorter than its header and data
	// padded to 2,880 bytes: the driver is still writing it, or never
	// finished.
	ReasonIncomplete = "Incomplete"
	// ReasonNoFrameID is a file with no FRAMEID, which this operator did
	// not write, so it stays where it is.
	ReasonNoFrameID = "NoFrameID"
)

// The reasons a frame fails, which are reasons of the Target's
// ArchiveAvailable condition too.
const (
	// ReasonCopyFailed is a read, a write, or a rename that failed.
	ReasonCopyFailed = "CopyFailed"
	// ReasonVerifyFailed is a copy that read back with another hash.
	ReasonVerifyFailed = "VerifyFailed"
	// ReasonNameConflict is another file under the frame's name in the
	// archive.
	ReasonNameConflict = "NameConflict"
)

// Fit cuts a report to what the termination message holds: the first
// MaxTotals totals, and then as many frames that wait or failed as fit
// in MaxReportBytes. Left still counts every frame.
func (r Report) Fit() Report {
	if len(r.Totals) > MaxTotals {
		r.Totals, r.Truncated = r.Totals[:MaxTotals], true
	}
	for r.size() > MaxReportBytes && len(r.Waiting)+len(r.Failures) > 0 {
		if len(r.Waiting) > 0 {
			r.Waiting = r.Waiting[:len(r.Waiting)-1]
		} else {
			r.Failures = r.Failures[:len(r.Failures)-1]
		}
	}
	return r
}

func (r Report) size() int {
	encoded, _ := json.Marshal(r)
	return len(encoded)
}
