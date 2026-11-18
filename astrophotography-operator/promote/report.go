// Package promote is the promote mode of the binary. It runs in the
// container of a promotion Job, which moves a target's frames from an
// outbox to the target's archive, records each one in the catalog, and
// reports the result to the operator.
package promote

import (
	"encoding/json"

	"github.com/liken-sh/liken/astrophotography-operator/catalog"
)

// The Job's container writes its report as its termination message,
// and the operator reads it from the pod's status when the Job ends.
// The kubelet truncates a termination message to MaxReportBytes.
const (
	TerminationLog = "/dev/termination-log"
	MaxReportBytes = 4096
)

// MaxTotals is the most totals that a report lists. 25 totals with long
// filter names encode to about 2,500 bytes, so MaxReportBytes still has
// room for a few waiting or failed frames.
const MaxTotals = 25

// Report is what one promotion Job sends the operator.
type Report struct {
	// Totals are the catalog's counts of frames by their settings. They
	// come from reading the whole catalog, not from this run alone, so
	// the next report corrects one that the operator never read.
	Totals []Total `json:"totals"`
	// Truncated is true when Fit cut Totals to MaxTotals.
	Truncated bool `json:"truncated,omitempty"`
	// Promoted is the number of frames that this run moved out of the
	// outbox, and Bytes is the number of bytes it copied.
	Promoted int32 `json:"promoted"`
	Bytes    int64 `json:"bytes"`
	// Left counts the frames that this run left in the outbox, the
	// incomplete ones and the failures. It does not count a file with no
	// FRAMEID, because this operator did not write it. Fit keeps Left
	// when it removes frames from Waiting and Failures.
	Left int32 `json:"left"`
	// Waiting lists the files that this run left in the outbox without
	// a fault, including the files with no FRAMEID. Failures lists the
	// frames that this run failed to promote.
	Waiting  []Frame `json:"waiting,omitempty"`
	Failures []Frame `json:"failures,omitempty"`
}

// Total is the catalog's count of frames for one combination of settings.
type Total = catalog.Total

// Frame is a file that stayed in the outbox, with the reason.
type Frame struct {
	// File is the frame's path relative to the outbox's root
	// (outbox.Frame.Path).
	File string `json:"file"`
	// Reason is ReasonIncomplete or ReasonNoFrameID for a waiting frame.
	// For a failure it is ReasonCopyFailed, ReasonVerifyFailed, or
	// ReasonNameConflict, which are also False reasons of the Target's
	// ArchiveAvailable condition.
	Reason  string `json:"reason"`
	Message string `json:"message,omitempty"`
}

// The reasons a frame waits in the outbox.
const (
	// ReasonIncomplete is a file shorter than its FITS header and data,
	// each padded to a multiple of 2,880 bytes. The camera driver is
	// still writing it, or stopped before it finished.
	ReasonIncomplete = "Incomplete"
	// ReasonNoFrameID is a file with no FRAMEID. This operator did not
	// write it, so promotion leaves it in the outbox.
	ReasonNoFrameID = "NoFrameID"
)

// The reasons a frame fails. They are also reasons of the Target's
// ArchiveAvailable condition.
const (
	// ReasonCopyFailed means a read, a write, or a rename failed.
	ReasonCopyFailed = "CopyFailed"
	// ReasonVerifyFailed means the copy read back with a different hash.
	ReasonVerifyFailed = "VerifyFailed"
	// ReasonNameConflict means the archive already has a different file
	// under the frame's name.
	ReasonNameConflict = "NameConflict"
)

// Fit cuts a report to fit the termination message. It keeps the first
// MaxTotals totals, then removes waiting frames, and then failures, from
// the end of each list until the report fits in MaxReportBytes.
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
