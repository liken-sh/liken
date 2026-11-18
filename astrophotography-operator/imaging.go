package main

// The work on one claimed target. The runner reads the target again
// before each frame, so when a person suspends, edits, or deletes the
// target, the change takes effect at the next frame and never in the
// middle of one.

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	// The names of the devices on the INDI server. wheelDevice is empty
	// when the train has no filter wheel.
	cameraDevice, wheelDevice, mountDevice string
	// aimed is the target's coordinates at the last slew. next slews
	// again when the target's coordinates differ from it.
	aimed astrophotography.Coordinates
	// centered describes how the mount was centered, and how describes
	// how the frames are guided and centered. Both go in the message of
	// the Imaging condition.
	centered, how string
	// frames counts the frames exposed since the claim. guiding.afterFrame
	// reads it to dither every dither.every frames.
	frames int
	// afterFrame is set after each counted frame. next calls
	// guiding.afterFrame, which can dither, before the following frame,
	// and skips the call when a slew comes first. A dither only helps the
	// frame after it, so after the last frame or before a slew it would
	// waste time.
	afterFrame bool
}

// imagingFailure is an error that ends the work on a target. reason is
// the reason of the Imaging condition, such as NoFilter. image records
// the failure in status.failed, and the runner does not claim the target
// again on this reservation until a person edits it.
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
		failed := &astrophotography.Failure{Reservation: r.name, ObservedGeneration: t.Metadata.Generation}
		return r.releaseAs(t, failed, failure.reason, fmt.Sprintf("Reservation %s released the target: %v", r.name, failure.err))
	case errors.Is(err, apiclient.ErrNotFound):
		return nil
	}
	return err
}

// prepare reads the INDI device name of the target's camera, mount, and
// filter wheel from the status of their objects. When an object is
// missing or has no status.indiDevice yet, prepare returns a plain
// error and the runner retries. When the session has no camera for the
// target's train or no mount, the target fails.
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

func (w *work) run(ctx context.Context) error {
	if err := w.r.syncDevices(ctx, w.mountDevice, w.cameraDevice, w.wheelDevice); err != nil {
		return err
	}
	if err := w.checkFilters(); err != nil {
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

// aim stops guiding, slews to the target, and centers it. Centering
// follows the slew because a slew only gets the mount near the target.
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

// next reads the target again and exposes one frame of it. It returns
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
	if t.Spec.Suspend || !left || t.Metadata.DeletionTimestamp != nil {
		return true, w.r.releaseIneligible(t)
	}
	if b := w.r.skyLimit().check(t, time.Now()); b != nil {
		return true, w.r.markBelow(t, b)
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
		// The next pass finishes the frame in flight (resume.go) and
		// claims the target again. The new work slews again, because the
		// INDI server may have restarted.
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

// release clears status.reservation and status.exposure and sets
// Imaging to False with the reason. It does not change a target that
// another reservation has claimed since.
func (r *runner) release(t *astrophotography.Target, reason, message string) error {
	return r.releaseAs(t, nil, reason, message)
}

// releaseAs is release that also records failed in status.failed when
// failed is not nil.
func (r *runner) releaseAs(t *astrophotography.Target, failed *astrophotography.Failure, reason, message string) error {
	_, err := r.o.writeTarget(t, func(fresh *astrophotography.Target, change *statusChange) bool {
		if fresh.Status.Reservation != r.name {
			return false
		}
		fresh.Status.Reservation, fresh.Status.Exposure = "", nil
		if failed != nil {
			fresh.Status.Failed = failed
		}
		change.setImaging(fresh, false, reason, message)
		return true
	})
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}
