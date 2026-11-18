package main

// The runner aims at a target by stopping guiding, slewing, and
// centering. Before each frame it starts guiding, and before a frame
// that follows a counted frame it first calls afterFrame. guiding.go
// implements guiding with PHD2, and centering.go implements centering
// with a plate solve. The tests replace both.

import (
	"context"
)

// centering points the mount at the target after the slew, which only
// gets the mount near it.
type centering interface {
	// center returns a description of how it centered, for the Imaging
	// condition's message. An error releases the target with the reason
	// CenteringFailed.
	center(ctx context.Context, w *work) (string, error)
}

// guiding keeps the stars still on the camera between frames.
type guiding interface {
	// open returns a function that keeps the runner's connection to its
	// guider up until ctx ends. The runner runs it in the background.
	open(r *runner) func(ctx context.Context)
	// stop ends any guiding before the mount slews, so PHD2 does not
	// send guide corrections against the slew. An error
	// releases the target with the reason GuidingFailed, because the
	// mount must not slew while PHD2 may still be guiding.
	stop(ctx context.Context, w *work) error
	// start begins guiding before each frame and returns a description
	// of how the frames are guided, for the Imaging condition's
	// message. An error sets Imaging to False with the reason
	// GuidingFailed, and the runner waits failedGuidingWait and tries
	// the frame again.
	start(ctx context.Context, w *work) (string, error)
	// afterFrame runs between two counted frames of the target with no
	// slew between them. frames is the number of frames that this
	// Reservation has taken of the target. An error is handled as an
	// error from start is.
	afterFrame(ctx context.Context, w *work, frames int) error
}
