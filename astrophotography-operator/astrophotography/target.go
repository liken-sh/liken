package astrophotography

// A Target is an object that a person wants to image, its position on
// the sky, and the frames to take of it. The operator images it over as
// many reservations as the frames need, and records the progress in its
// status. Any Reservation in the target's namespace whose spec.holder is
// astrophotography-operator can image it.

import (
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type Target = Object[TargetSpec, TargetStatus]

type TargetSpec struct {
	// Object is the name of the object, and the value of OBJECT in each
	// frame's header.
	Object string `json:"object"`
	// Coordinates are J2000, as catalogs give them. The runner converts
	// them to the equinox of the date before a slew (see package sky).
	Coordinates Coordinates `json:"coordinates"`
	// A reservation images the eligible target with the highest
	// Priority. A suspended target is not eligible.
	Priority int32 `json:"priority,omitempty"`
	Suspend  bool  `json:"suspend,omitempty"`
	// OpticalTrain limits the target to the telescope of that train.
	// When it is empty, any telescope whose session has exactly one
	// imaging camera can image the target.
	OpticalTrain string     `json:"opticalTrain,omitempty"`
	Sequences    []Sequence `json:"sequences"`
	// Dither is nil when the operator should not dither.
	Dither *Dither `json:"dither,omitempty"`
	// Centering is nil when the operator should slew without solving.
	Centering *Centering `json:"centering,omitempty"`
	Archive   Archive    `json:"archive"`
	// A keyword in FITSKeywords replaces the session's keyword of the
	// same name for the imaging camera (see observatory.MergeFITSKeywords).
	FITSKeywords []observatory.FITSKeyword `json:"fitsKeywords,omitempty"`
}

// Coordinates is a sky position in the form that SIMBAD and KStars
// print: right ascension in hours as HH:MM:SS.s and declination in
// degrees as ±DD:MM:SS.s. sky.ParseRA and sky.ParseDec read them.
type Coordinates struct {
	RA  string `json:"ra"`
	Dec string `json:"dec"`
}

// Sequence is one kind of frame and how many to take. When Binning,
// Gain, or Offset is nil, the runner does not set that camera property,
// so a camera that has no gain property can still take the frames.
type Sequence struct {
	// Filter is a name from the filter wheel's FILTER_NAME. When it is
	// empty, the runner does not move the filter wheel.
	Filter string `json:"filter,omitempty"`
	// Exposure is a duration, such as 300s or 500ms.
	Exposure string `json:"exposure"`
	Count    int32  `json:"count"`
	Binning  *int32 `json:"binning,omitempty"`
	Gain     *int32 `json:"gain,omitempty"`
	Offset   *int32 `json:"offset,omitempty"`
}

// ExposureDuration returns an error only for an object that the API
// server did not validate, because the CRD accepts only strings that
// time.ParseDuration reads.
func (s Sequence) ExposureDuration() (time.Duration, error) {
	return time.ParseDuration(s.Exposure)
}

// Dither moves PHD2's lock position by up to Pixels guide-camera pixels
// after every Every frames.
type Dither struct {
	Every  int32   `json:"every"`
	Pixels float64 `json:"pixels"`
	RAOnly bool    `json:"raOnly,omitempty"`
}

// Centering names the Solver that centers the target and sets the
// limits of the centering loop. The CRD fills in Tolerance, Exposure,
// and Attempts when they are left out.
type Centering struct {
	Solver string `json:"solver"`
	// Tolerance is the largest error that counts as centered, in
	// arcseconds.
	Tolerance float64 `json:"tolerance,omitempty"`
	// Exposure is the length of a centering frame, as a duration.
	Exposure string `json:"exposure,omitempty"`
	// Filter is the filter of a centering frame. When it is empty, the
	// runner uses the first sequence's filter.
	Filter   string `json:"filter,omitempty"`
	Attempts int32  `json:"attempts,omitempty"`
}

// Archive is the claim that holds the target's frames and catalog, and
// the naming of the frames in it.
type Archive struct {
	// ClaimName is a PersistentVolumeClaim in the target's namespace. The
	// CRD refuses a change to it, because the counts in the status come
	// from the catalog in that claim.
	ClaimName string `json:"claimName"`
	// Path is a directory in the claim. The frames and the catalog are
	// under it.
	Path string `json:"path,omitempty"`
	// Format is the file path of each frame, with tokens such as
	// {object}, relative to Path and without an extension.
	Format    string    `json:"format,omitempty"`
	Promotion Promotion `json:"promotion,omitempty"`
}

// Promotion sets when frames move from the outbox to the archive.
type Promotion string

const (
	// PromotionContinuous moves frames during the night, so the outbox
	// stays small.
	PromotionContinuous Promotion = "Continuous"
	// PromotionAfterReservation waits until no Reservation that writes to
	// the outbox claim is Activating, Ready, or Deactivating, so the
	// network between the outbox and the archive is idle while the
	// telescope works.
	PromotionAfterReservation Promotion = "AfterReservation"
)

type TargetStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Progress is the number of frames archived over the number wanted,
	// such as 37/180, for the printer column.
	Progress string `json:"progress,omitempty"`
	// Reservation is the Reservation that is imaging the target now. A
	// runner claims the target by writing this field with the
	// resourceVersion it read, so when two runners choose the same
	// target, one write succeeds and the other gets a conflict.
	Reservation string             `json:"reservation,omitempty"`
	Failed      *Failure           `json:"failed,omitempty"`
	Sequences   []SequenceProgress `json:"sequences,omitempty"`
	// Outboxes lists the claims that hold frames of this target that are
	// not yet in the archive.
	Outboxes  []OutboxFrames   `json:"outboxes,omitempty"`
	Promotion *PromotionStatus `json:"promotion,omitempty"`
	// Exposure is the frame in flight. A runner writes it before the
	// exposure starts and clears it when it counts the frame, so a runner
	// that restarts counts the frame once.
	Exposure   *Exposure        `json:"exposure,omitempty"`
	Centering  *CenteringResult `json:"centering,omitempty"`
	Conditions []Condition      `json:"conditions,omitempty"`
}

// SequenceProgress is one sequence of the spec with its counts. The
// frames still to expose are Count - Archived - Pending.
type SequenceProgress struct {
	Sequence
	// Archived is the number of frames in the archive's catalog, from the
	// last promotion report.
	Archived int32 `json:"archived"`
	// Pending is the number of frames in an outbox that are not yet in
	// the archive.
	Pending int32 `json:"pending"`
}

// OutboxFrames counts the target's frames in one outbox claim. The
// runner adds one to Pending for each frame it counts. The promotion
// loop sets Pending and Left from each report, and removes the entry
// when Pending is 0.
type OutboxFrames struct {
	ClaimName string `json:"claimName"`
	Pending   int32  `json:"pending"`
	// Left is the number of frames that the last promotion Job of the
	// claim left in the outbox. A promotion Job starts only when Pending
	// is greater than Left, so after a frame fails no Job starts until a
	// new frame arrives or the retry annotation on the target sets Left
	// to 0.
	Left int32 `json:"left,omitempty"`
}

// PromotionStatus records the target's promotion Jobs. They run one at
// a time, and each Job's name ends in a number that increases.
type PromotionStatus struct {
	// Applied is the number of the last promotion Job whose report the
	// operator applied. The operator ignores the report of a Job
	// numbered at or below Applied, so no report is applied twice.
	Applied int32 `json:"applied"`
}

// Failure is a reservation that released the target after a failure,
// such as NoFilter. While metadata.generation equals
// ObservedGeneration, that reservation does not claim the target again,
// so one target with a wrong filter does not use up the night. The
// record is in the status so that it survives an operator restart. An
// edit to the spec makes the target eligible again, and other
// reservations can claim it as usual. The operator clears the record
// when the reservation ends.
type Failure struct {
	Reservation        string `json:"reservation"`
	ObservedGeneration int64  `json:"observedGeneration"`
}

type Exposure struct {
	FrameID string `json:"frameID"`
	// Sequence is the index of the sequence in spec.sequences.
	Sequence    int32  `json:"sequence"`
	Reservation string `json:"reservation"`
	// File is the frame's path in the outbox, relative to the outbox
	// root (see outbox.Frame.Path).
	File      string    `json:"file"`
	StartTime time.Time `json:"startTime"`
}

type CenteringResult struct {
	Reservation string    `json:"reservation"`
	Time        time.Time `json:"time"`
	Rounds      int32     `json:"rounds"`
	// Solution is the result of the last solve. It is nil when no solve
	// succeeded.
	Solution *Solution `json:"solution,omitempty"`
	// Error is the angle between the solved center and the target, both
	// J2000, in arcseconds.
	Error *float64 `json:"error,omitempty"`
	// RoundErrors holds the Error of each round that solved, in order,
	// so a centering that ended with a sync shows how far off the first
	// round was. spec.centering.attempts is at most 20, which limits
	// this list.
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
