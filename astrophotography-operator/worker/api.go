// Package worker is the worker mode of the operator's binary. The
// operator runs one worker pod for each Reservation that is Activating
// or Ready and has an outbox. The pod mounts the outbox, extracts the
// stars from a frame, and reports the outbox's contents and free space.
// This file defines the HTTP API that the operator calls.
//
// A request that fails gets a status other than 2xx and a line of text
// that gives the reason.
package worker

import "github.com/liken-sh/liken/astrophotography-operator/sky"

const Port = 8080

const (
	// PathExtract accepts a POST with an ExtractRequest and returns an
	// ExtractResponse.
	PathExtract = "/extract"
	// PathOutbox accepts a GET and returns an OutboxResponse.
	PathOutbox = "/outbox"
	// PathCentering accepts a DELETE of PathCentering + <FRAMEID> and
	// deletes that centering frame from the outbox.
	PathCentering = "/centering/"
)

// ExtractRequest asks the worker for the stars of one frame in the
// outbox.
type ExtractRequest struct {
	// Frame is the frame's path relative to the outbox's root
	// (outbox.Frame.Path).
	Frame string `json:"frame"`
	Hint  Hint   `json:"hint"`
}

// Hint is the sky position and plate scale that the operator expects
// for the frame. The worker writes it into the .axy file, so the solver
// searches only near that position and scale.
type Hint struct {
	// Center is the expected center, J2000, in degrees.
	Center sky.Position `json:"center"`
	// Radius is how far from Center to search, in degrees. With a
	// radius of 0 the worker writes no position into the .axy, so the
	// solver searches the whole sky (a blind solve).
	Radius float64 `json:"radius"`
	// ScaleLow and ScaleHigh bound the plate scale, in arcseconds per
	// pixel.
	ScaleLow  float64 `json:"scaleLow"`
	ScaleHigh float64 `json:"scaleHigh"`
}

type ExtractResponse struct {
	// AXY is the .axy file that solve-field --just-augment wrote. The
	// operator sends it to the solver unchanged. It is empty when
	// SourceExtractor found no stars, because the solver cannot solve an
	// empty list.
	AXY []byte `json:"axy"`
	// Stars is the number of stars that SourceExtractor found.
	Stars int32 `json:"stars"`
	// MedianFWHM is the median FWHM_IMAGE of the stars, in pixels. It is
	// 0 when there are no stars.
	MedianFWHM float64 `json:"medianFWHM"`
}

type OutboxResponse struct {
	// FreeBytes is the free space that statfs reports to the worker for
	// the outbox's file system. For a local-path claim that is the free
	// space of the node's file system that all local-path claims share,
	// not the size the claim requested.
	FreeBytes int64 `json:"freeBytes"`
	// Frames lists the frames in the targets' directories, each as a path
	// relative to the outbox's root, sorted by path. It leaves out the
	// centering frames.
	Frames []string `json:"frames"`
}
