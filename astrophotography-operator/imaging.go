package main

// The work on one claimed target (plan 01's order of operations). The
// runner stops guiding, slews, centers, and then guides and exposes the
// first sequence with work left, one frame at a time, until no sequence
// has any. Before each frame it reads the target again, so
// a target that a person suspends, edits, or deletes changes the work
// at the next frame, not in the middle of one.

import (
	"context"
	"errors"
	"fmt"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// work is one runner's work on one target, and the devices it drives.
type work struct {
	r      *runner
	res    *observatory.Reservation
	target *astrophotography.Target
	camera camera
	// The names of the devices on the INDI server. wheel is empty when
	// the train has no filter wheel.
	cameraDevice, wheelDevice, mountDevice string
	// aimed is the target's coordinates at the last slew. A person who
	// edits them during the night moves the mount at the next frame.
	aimed astrophotography.Coordinates
	// centered says how the mount was centered, and how says how the
	// frames are guided and centered, for the Imaging condition's
	// message.
	centered, how string
	// frames counts the frames this reservation took of the target, for
	// a dither every dither.every frames.
	frames int
	// afterFrame says that guiding.afterFrame waits for the frame that
	// follows a counted one. The runner calls it only when another
	// frame of the target follows with no slew, because a dither moves
	// the lock position for the next frame, and a settle after the last
	// frame or before a slew is time that no frame uses.
	afterFrame bool
}

// imagingFailure ends the work on a target with a reason of the
// Imaging condition, such as NoFilter. The runner leaves the target for
// the rest of the reservation, unless a person edits it.
type imagingFailure struct {
	reason string
	err    error
}

func (f *imagingFailure) Error() string { return f.err.Error() }
func (f *imagingFailure) Unwrap() error { return f.err }

func fail(reason string, format string, args ...any) error {
	return &imagingFailure{reason: reason, err: fmt.Errorf(format, args...)}
}

// image works on a claimed target until it has no work left, it fails,
// or ctx ends.
func (r *runner) image(ctx context.Context, t *astrophotography.Target) error {
	w, err := r.prepare(t)
	if err == nil {
		err = w.run(ctx)
	}
	var failure *imagingFailure
	switch {
	case ctx.Err() != nil:
		return nil
	case errors.As(err, &failure):
		r.logf("target %s: %v", targetName(t), err)
		r.skipped[t.Metadata.UID] = t.Metadata.Generation
		return r.release(t, failure.reason, fmt.Sprintf("Reservation %s: %v", r.name, failure.err))
	case errors.Is(err, apiclient.ErrNotFound):
		return nil
	}
	return err
}

// prepare finds the devices that image a target. A device that the
// server does not serve yet is an error that the runner retries; a
// session that has no camera for the target fails the target.
func (r *runner) prepare(t *astrophotography.Target) (*work, error) {
	res := r.res
	session := res.Status.Session
	cam, ok := cameraFor(session, t)
	if !ok {
		return nil, fail(astrophotography.ReasonNoTrain, "the session has no imaging camera for optical train %q", t.Spec.OpticalTrain)
	}
	mount, ok := sessionDevice(session, observatory.MountKind, "")
	if !ok {
		return nil, fail(astrophotography.ReasonSlewFailed, "the session has no Mount")
	}
	wheel, _ := sessionDevice(session, observatory.FilterWheelKind, cam.train)
	w := &work{r: r, res: res, target: t, camera: cam}
	for _, d := range []struct {
		into *string
		name string
		read func(*operator, string, string) (string, error)
	}{
		{&w.cameraDevice, cam.name, cameraDevice},
		{&w.mountDevice, mount, mountDevice},
		{&w.wheelDevice, wheel, wheelDevice},
	} {
		if d.name == "" {
			continue
		}
		var err error
		if *d.into, err = d.read(r.o, r.namespace, d.name); err != nil {
			return nil, err
		}
	}
	return w, nil
}

// run is plan 01's order of operations for one target.
func (w *work) run(ctx context.Context) error {
	if err := w.r.syncDevices(ctx, w.mountDevice, w.cameraDevice, w.wheelDevice); err != nil {
		return err
	}
	if err := w.aim(ctx); err != nil {
		return err
	}
	for ctx.Err() == nil {
		done, err := w.next(ctx)
		if done || err != nil {
			return err
		}
	}
	return nil
}

// aim stops the guiding, slews to the target, and centers it. It runs
// after each claim, and again when a person edits the coordinates
// during the night, because a slew lands near the target, not on it.
func (w *work) aim(ctx context.Context) error {
	if err := w.r.guiding.stop(ctx, w); err != nil {
		return fail(astrophotography.ReasonGuidingFailed, "stopping the guiding: %w", err)
	}
	if err := w.slew(ctx); err != nil {
		return err
	}
	centered, err := w.r.centering.center(ctx, w)
	if err != nil {
		return centeringFailure(err)
	}
	w.centered = centered
	return nil
}

// next reads the target again and exposes one frame of it. It answers
// true when the work on the target is over.
func (w *work) next(ctx context.Context) (bool, error) {
	t, err := w.r.o.readTarget(w.r.namespace, w.target.Metadata.Name)
	if err != nil {
		return true, err
	}
	if t.Metadata.UID != w.target.Metadata.UID || t.Status.Reservation != w.r.name {
		return true, nil
	}
	w.target = t
	index, left := nextSequence(aligned(t))
	if t.Spec.Suspend || !left {
		return true, w.r.releaseIneligible(t)
	}
	if t.Spec.Coordinates != w.aimed {
		w.afterFrame = false
		if err := w.aim(ctx); err != nil {
			return true, err
		}
	}
	if w.afterFrame {
		w.afterFrame = false
		if err := w.r.guiding.afterFrame(ctx, w, w.frames); err != nil {
			return false, w.guidingFailed(ctx, err)
		}
	}
	guided, err := w.r.guiding.start(ctx, w)
	if err != nil {
		return false, w.guidingFailed(ctx, err)
	}
	w.how = guided + ", " + w.centered
	err = w.expose(ctx, index)
	var full *outboxFull
	switch {
	case err == nil:
		w.frames++
		w.afterFrame = true
		return false, nil
	case errors.As(err, &full):
		return false, w.waitForSpace(ctx, full)
	case errors.Is(err, errClaimLost):
		return true, nil
	case lostConnection(err):
		// The pass that follows finishes the frame in flight from
		// status.exposure, the way a restart does, and claims the target
		// again, which slews again: the server can have restarted.
		w.r.logf("target %s: %v; waiting for the INDI server", targetName(t), err)
		return true, w.r.connected(ctx)
	}
	var failure *imagingFailure
	if errors.As(err, &failure) || ctx.Err() != nil {
		return true, err
	}
	return false, w.exposureFailed(ctx, err)
}

// setImaging writes the Imaging condition of the claimed target.
func (w *work) setImaging(imaging bool, reason, message string) error {
	_, err := w.r.o.writeTarget(w.target, func(t *astrophotography.Target, change *statusChange) bool {
		if t.Status.Reservation != w.r.name {
			return false
		}
		change.setImaging(t, imaging, reason, message)
		return true
	})
	return err
}

// release ends a reservation's claim of a target: it clears
// status.reservation and status.exposure, and sets Imaging False with
// the reason. A target that another runner claimed since is left alone.
func (r *runner) release(t *astrophotography.Target, reason, message string) error {
	_, err := r.o.writeTarget(t, func(fresh *astrophotography.Target, change *statusChange) bool {
		if fresh.Status.Reservation != r.name {
			return false
		}
		fresh.Status.Reservation, fresh.Status.Exposure = "", nil
		change.setImaging(fresh, false, reason, message)
		return true
	})
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}
