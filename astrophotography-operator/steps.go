package main

// The steps of plan 01's order that guiding, dithering, and centering
// fill in. The runner calls them at fixed places in a target's work, so
// pieces H and I of plan 08 each replace one implementation and leave
// the order alone:
//
//  1. guiding.stop before each slew, so PHD2 does not chase the stars
//     the mount leaves behind (stop_capture);
//  2. centering.center after the slew, the solve and sync loop of plan
//     02, with the Imaging reason Centering;
//  3. guiding.start before each frame: loop, find_star, and guide,
//     then SettleDone, with the reason Guiding, when PHD2 does not
//     guide a settled star;
//  4. guiding.afterFrame after each frame the runner counts, which
//     dithers every dither.every frames and waits for SettleDone.
//
// guiding.go guides with PHD2. The centering here centers nothing, and
// says so in the Imaging condition's message, so a person reads that
// the mount went to the coordinates without a solve.

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
	// open prepares a runner's connection to its guider, and answers
	// the function that keeps it open until ctx ends. The runner calls
	// open before its first step, and runs the function beside its
	// work.
	open(r *runner) func(ctx context.Context)
	// stop ends any guiding before the mount moves. An error releases
	// the target with GuidingFailed, because the mount does not slew
	// while PHD2 may still guide.
	stop(ctx context.Context, w *work) error
	// start guides before each frame, and answers how the frames are
	// guided, for the Imaging condition's message, or an error that
	// sets Imaging False with GuidingFailed until a later start
	// succeeds.
	start(ctx context.Context, w *work) (string, error)
	// afterFrame runs after each counted frame, with the count of the
	// frames this reservation took of the target. An error sets
	// Imaging False with GuidingFailed, as start's does.
	afterFrame(ctx context.Context, w *work, frames int) error
}

// noCentering slews and does not solve.
type noCentering struct{}

func (noCentering) center(context.Context, *work) (string, error) {
	return "not centered", nil
}
