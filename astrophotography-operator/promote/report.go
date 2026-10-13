// Package promote is the binary's promote mode: the container of a
// promotion Job, which moves a target's frames from an outbox to the
// target's archive, records each one in the catalog, and reports
// (plan 03). This file holds the report, the contract between the Job
// and the operator.
package promote

import "github.com/liken-sh/liken/astrophotography-operator/catalog"

// The Job's container writes its report to the termination message,
// and the operator reads it from the pod's status when the Job ends.
// The kubelet keeps at most MaxReportBytes of the message.
const (
	TerminationLog = "/dev/termination-log"
	MaxReportBytes = 4096
)

// MaxTotals is how many totals a report holds. 25 totals of a long
// filter name take about 2,300 bytes, so they fit in MaxReportBytes
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
	// Promoted and Bytes count the frames and the bytes that this run
	// moved to the archive.
	Promoted int32 `json:"promoted"`
	Bytes    int64 `json:"bytes"`
	// Waiting lists the frames that this run left in the outbox with no
	// fault, and Failures each frame that this run failed to promote.
	Waiting  []Frame `json:"waiting,omitempty"`
	Failures []Frame `json:"failures,omitempty"`
}

// Total counts the catalog's frames of one combination of settings.
type Total struct {
	catalog.Settings
	Frames int32 `json:"frames"`
}

// Frame is a frame that stayed in the outbox, and why.
type Frame struct {
	// File is the frame's path relative to the outbox's root
	// (outbox.Frame.Path).
	File string `json:"file"`
	// Reason is ReasonIncomplete for a frame that waits, and one of the
	// False reasons of the Target's ArchiveAvailable for a failure:
	// CopyFailed, VerifyFailed, or NameConflict.
	Reason  string `json:"reason"`
	Message string `json:"message,omitempty"`
}

// ReasonIncomplete is a frame shorter than its header and data padded
// to 2,880 bytes: the driver is still writing it, or never finished.
const ReasonIncomplete = "Incomplete"
