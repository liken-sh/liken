// Package worker is the binary's worker mode: one pod for each
// reservation the operator holds, beside the reservation's outbox,
// that finds the stars in a frame and answers questions about the
// outbox (plan 02). This file holds the HTTP contract between the
// worker and the operator, its only client.
//
// Every answer to a request that fails is a status other than 2xx with
// a line of text that says why.
package worker

import "github.com/liken-sh/liken/astrophotography-operator/sky"

// Port is the worker's port. Its Service is <Reservation>-astrophotography.
const Port = 8080

// The paths that the worker serves.
const (
	// PathExtract takes a POST of an ExtractRequest and answers an
	// ExtractAnswer.
	PathExtract = "/extract"
	// PathOutbox takes a GET and answers an OutboxAnswer.
	PathOutbox = "/outbox"
	// PathCentering takes a DELETE of PathCentering + <FRAMEID>, and
	// removes that centering frame from the outbox. The worker deletes
	// nothing else.
	PathCentering = "/centering/"
)

// ExtractRequest names a frame in the outbox and the hints for its
// solve.
type ExtractRequest struct {
	// Frame is the frame's path relative to the outbox's root
	// (outbox.Frame.Path).
	Frame string `json:"frame"`
	Hint  Hint   `json:"hint"`
}

// Hint is what the operator expects of the frame, which the worker
// writes into the .axy so that the solver searches less.
type Hint struct {
	// Center is the expected center, J2000, in degrees.
	Center sky.Position `json:"center"`
	// Radius is how far from Center to search, in degrees. A radius of
	// 0 writes no position into the .axy, so the solver searches the
	// whole sky: a blind solve.
	Radius float64 `json:"radius"`
	// ScaleLow and ScaleHigh bound the plate scale, in arcseconds per
	// pixel.
	ScaleLow  float64 `json:"scaleLow"`
	ScaleHigh float64 `json:"scaleHigh"`
}

// ExtractAnswer is the star list for the solver, and what the
// extraction measured.
type ExtractAnswer struct {
	// AXY is the .axy file that solve-field --just-augment wrote, a few
	// kilobytes, which the operator carries to the solver as it is. JSON
	// carries it in base64. It is empty when SourceExtractor found no
	// star, because a list of no stars cannot solve.
	AXY []byte `json:"axy"`
	// Stars counts the stars that SourceExtractor found.
	Stars int32 `json:"stars"`
	// MedianFWHM is the median FWHM_IMAGE of the stars, in pixels, and 0
	// when it found none.
	MedianFWHM float64 `json:"medianFWHM"`
}

// OutboxAnswer is the state of the outbox.
type OutboxAnswer struct {
	// FreeBytes is what statfs reports as free to the worker for the
	// outbox's file system. For a local-path claim that is the node's
	// pool of claims, not the claim's request.
	FreeBytes int64 `json:"freeBytes"`
	// Frames lists each frame in a target's directory, as its path
	// relative to the outbox's root, in the order of the paths. It
	// leaves out the centering frames.
	Frames []string `json:"frames"`
}
