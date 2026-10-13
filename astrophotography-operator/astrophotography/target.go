package astrophotography

// A Target states what a person wants to image: an object, where it is
// on the sky, and the frames wanted of it (plan 01). It lasts as long as
// the work does, across as many reservations as it takes, and its
// status keeps the progress. Every Reservation in the target's
// namespace whose holder is astrophotography-operator can image it.

import (
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type Target = Object[TargetSpec, TargetStatus]

type TargetSpec struct {
	// Object is the name of the object, and the value of OBJECT in each
	// frame's header.
	Object string `json:"object"`
	// Coordinates are J2000, the equinox of every catalog. The runner
	// converts them to the equinox of the date before a slew (sky).
	Coordinates Coordinates `json:"coordinates"`
	// Priority and Suspend decide which targets a reservation images:
	// the eligible target with the highest priority first.
	Priority int32 `json:"priority,omitempty"`
	Suspend  bool  `json:"suspend,omitempty"`
	// OpticalTrain limits the target to that train's telescope. With no
	// train, any telescope whose session has exactly one imaging camera
	// can image it.
	OpticalTrain string     `json:"opticalTrain,omitempty"`
	Sequences    []Sequence `json:"sequences"`
	// Dither is absent when the operator does not dither.
	Dither *Dither `json:"dither,omitempty"`
	// Centering is absent when the operator slews and does not solve.
	Centering *Centering `json:"centering,omitempty"`
	Archive   Archive    `json:"archive"`
	// FITSKeywords is the target's level of the declared keywords. The
	// runner merges it after the session's keywords for the imaging
	// camera (observatory.MergeFITSKeywords).
	FITSKeywords []observatory.FITSKeyword `json:"fitsKeywords,omitempty"`
}

// Coordinates is a position in the form that SIMBAD and KStars print:
// the right ascension in hours as HH:MM:SS.s and the declination in
// degrees as ±DD:MM:SS.s. sky.ParseRA and sky.ParseDec read them.
type Coordinates struct {
	RA  string `json:"ra"`
	Dec string `json:"dec"`
}

// Sequence is one kind of frame and how many of it. A field that is
// absent leaves the camera's own value, so a camera with no gain
// property still works.
type Sequence struct {
	// Filter is a name in the filter wheel's FILTER_NAME. With no
	// filter, the light path stays as it is.
	Filter string `json:"filter,omitempty"`
	// Exposure is a duration, such as 300s or 500ms.
	Exposure string `json:"exposure"`
	Count    int32  `json:"count"`
	Binning  *int32 `json:"binning,omitempty"`
	Gain     *int32 `json:"gain,omitempty"`
	Offset   *int32 `json:"offset,omitempty"`
}

// ExposureDuration answers the exposure as a time.Duration. The CRD
// accepts only what time.ParseDuration reads, so an error means the
// object did not come through the API server.
func (s Sequence) ExposureDuration() (time.Duration, error) {
	return time.ParseDuration(s.Exposure)
}

// Dither moves PHD2's lock position after every Every frames, by up to
// Pixels pixels of the guide camera.
type Dither struct {
	Every  int32   `json:"every"`
	Pixels float64 `json:"pixels"`
	RAOnly bool    `json:"raOnly,omitempty"`
}

// Centering names the Solver that centers the target, and the limits of
// the loop of plan 02. The CRD fills in Tolerance, Exposure, and
// Attempts when a person leaves them out.
type Centering struct {
	Solver string `json:"solver"`
	// Tolerance is the largest error that counts as centered, in
	// arcseconds.
	Tolerance float64 `json:"tolerance,omitempty"`
	// Exposure is the length of a centering frame, as a duration.
	Exposure string `json:"exposure,omitempty"`
	// Filter is the filter of a centering frame. With none, the runner
	// uses the first sequence's filter.
	Filter   string `json:"filter,omitempty"`
	Attempts int32  `json:"attempts,omitempty"`
}

// Archive is the claim that keeps the target's frames and catalog, and
// the names of the frames in it (plan 03).
type Archive struct {
	// ClaimName is a PersistentVolumeClaim in the target's namespace. It
	// cannot change, because the counts come from the archive's catalog.
	ClaimName string `json:"claimName"`
	// Path is a directory in the claim. The frames and the catalog are
	// under it.
	Path string `json:"path,omitempty"`
	// Format is the file name of each frame, with tokens such as
	// {object}, relative to Path and with no extension.
	Format    string    `json:"format,omitempty"`
	Promotion Promotion `json:"promotion,omitempty"`
}

// Promotion is when frames move from the outbox to the archive.
type Promotion string

const (
	// PromotionContinuous promotes while the night runs, so the outbox
	// stays small.
	PromotionContinuous Promotion = "Continuous"
	// PromotionAfterReservation promotes when the reservation that wrote
	// the frames ends, so the link stays quiet while the telescope works.
	PromotionAfterReservation Promotion = "AfterReservation"
)

type TargetStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Progress is the frames archived of the frames wanted, such as
	// 37/180, for the printer column.
	Progress string `json:"progress,omitempty"`
	// Reservation is the Reservation that images the target now. A
	// runner claims the target by writing it, with the resourceVersion
	// it read, so two runners that choose one target get one success
	// and one conflict.
	Reservation string             `json:"reservation,omitempty"`
	Sequences   []SequenceProgress `json:"sequences,omitempty"`
	// Outboxes lists the claims that hold frames of this target that
	// are not yet in the archive.
	Outboxes []OutboxFrames `json:"outboxes,omitempty"`
	// Promotion records the target's promotion Jobs (plan 03).
	Promotion *PromotionStatus `json:"promotion,omitempty"`
	// Exposure is the frame in flight, written before the exposure
	// starts and cleared after it ends, so a runner that restarts counts
	// the frame once.
	Exposure   *Exposure        `json:"exposure,omitempty"`
	Centering  *CenteringResult `json:"centering,omitempty"`
	Conditions []Condition      `json:"conditions,omitempty"`
}

// SequenceProgress is one sequence of the spec with its counts. Work
// left is Count - Archived - Pending.
type SequenceProgress struct {
	Sequence
	// Archived counts the frames in the archive's catalog, from the last
	// promotion report.
	Archived int32 `json:"archived"`
	// Pending counts the frames in an outbox and not yet in the archive.
	Pending int32 `json:"pending"`
}

// OutboxFrames is one outbox claim and the frames of the target in it.
// The runner adds one to Pending after each exposure, and creates the
// entry when the claim has none. The promotion loop owns Left, sets
// Pending from each report, and removes the entry when Pending is 0.
type OutboxFrames struct {
	ClaimName string `json:"claimName"`
	Pending   int32  `json:"pending"`
	// Left is the frames that the last promotion Job of the claim left
	// in it. A promotion starts when Pending is more than Left, so a
	// frame that failed starts no Job until a new frame arrives.
	Left int32 `json:"left,omitempty"`
}

// PromotionStatus records the target's promotion Jobs. They run one at
// a time, and their names end in a number that counts up.
type PromotionStatus struct {
	// Applied is the number of the last promotion Job whose report the
	// operator applied. The next Job takes the next number, and a Job at
	// or below it is never applied twice.
	Applied int32 `json:"applied"`
}

// Exposure is the frame in flight.
type Exposure struct {
	FrameID string `json:"frameID"`
	// Sequence is the index of the sequence in spec.sequences.
	Sequence    int32  `json:"sequence"`
	Reservation string `json:"reservation"`
	// File is the frame's path in the outbox, relative to the outbox's
	// root (outbox.Frame).
	File      string    `json:"file"`
	StartTime time.Time `json:"startTime"`
}

// CenteringResult is the last result of centering (plan 02).
type CenteringResult struct {
	Reservation string    `json:"reservation"`
	Time        time.Time `json:"time"`
	Rounds      int32     `json:"rounds"`
	// Solution is the last solve's answer, absent when no solve
	// succeeded.
	Solution *Solution `json:"solution,omitempty"`
	// Error is the angle between the solved center and the target, both
	// J2000, in arcseconds.
	Error *float64 `json:"error,omitempty"`
	// RoundErrors holds the Error of each round that solved, in order,
	// so a centering that a sync corrected shows how far the first
	// round was off. spec.centering.attempts bounds it at 20.
	RoundErrors []float64 `json:"roundErrors,omitempty"`
}

// Solution is where a solved frame points.
type Solution struct {
	// Center is J2000.
	Center Coordinates `json:"center"`
	// Rotation is the angle of the frame on the sky, in degrees.
	Rotation float64 `json:"rotation"`
	// Scale is the plate scale, in arcseconds per pixel.
	Scale float64 `json:"scale"`
}
