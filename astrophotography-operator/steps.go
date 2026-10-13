package main

// The steps of plan 01's order that guiding, dithering, and centering
// fill in. The runner calls them at fixed places in a target's work, so
// pieces H and I of plan 08 each replace one implementation and leave
// the order alone:
//
//  1. guiding.stop before the slew, so PHD2 does not chase the stars
//     the mount leaves behind (stop_capture);
//  2. centering.center after the slew, the solve and sync loop of plan
//     02, with the Imaging reason Centering;
//  3. guiding.start before the first frame: loop, find_star, and guide,
//     then SettleDone, with the reason Guiding;
//  4. guiding.afterFrame after each frame the runner counts, which
//     dithers every dither.every frames and waits for SettleDone.
//
// The implementations here guide nothing and center nothing, and say
// so in the Imaging condition's message, so a person reads that the
// frames are unguided and that the mount went to the coordinates
// without a solve.

import (
	"context"
)

// centering aligns the mount with the target after the slew.
type centering interface {
	// center answers what it did, for the Imaging condition's message,
	// or an error that sets Imaging False with CenteringFailed.
	center(ctx context.Context, w *work) (string, error)
}

// guiding keeps the stars still on the camera between frames.
type guiding interface {
	// stop ends any guiding before the mount moves.
	stop(ctx context.Context, w *work) error
	// start guides before the first frame, and answers how the frames
	// are guided, for the Imaging condition's message, or an error that
	// sets Imaging False with GuidingFailed.
	start(ctx context.Context, w *work) (string, error)
	// afterFrame runs after each counted frame, with the count of the
	// frames this reservation took of the target.
	afterFrame(ctx context.Context, w *work, frames int) error
}

// noCentering slews and does not solve.
type noCentering struct{}

func (noCentering) center(context.Context, *work) (string, error) {
	return "not centered", nil
}

// noGuiding exposes unguided and does not dither.
type noGuiding struct{}

func (noGuiding) stop(context.Context, *work) error            { return nil }
func (noGuiding) start(context.Context, *work) (string, error) { return "unguided", nil }
func (noGuiding) afterFrame(context.Context, *work, int) error { return nil }
