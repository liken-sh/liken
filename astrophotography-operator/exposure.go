package main

// One frame (plan 01, step 7). Before each exposure the runner writes
// every setting the frame depends on, not once for each target:
// observatory-operator writes the upload settings and its keywords
// again when a camera's driver comes back in a new pod, and a person in
// KStars can change a setting during the night. A full write before
// each frame states the frame's header in full, so no earlier write
// leaks into it. Ten keywords cost well under a second against an
// exposure of minutes.
//
// The runner records the frame in status.exposure before the exposure
// starts and clears it when the frame is counted, so a runner that
// restarts in between counts the frame once (resume.go).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/outbox"
	"github.com/liken-sh/liken/astrophotography-operator/promote"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// filterTimeout bounds a turn of the filter wheel.
const filterTimeout = 2 * time.Minute

// readoutAllowance is the time past the exposure that the runner waits
// for the frame: the camera reads the sensor out, and the driver writes
// the file.
const readoutAllowance = 2 * time.Minute

// headerBlocks is the space of a frame's header in the outbox: three
// FITS blocks of 2,880 bytes.
const headerBlocks = 3 * 2880

// expose takes one frame of the sequence at index, and counts it.
func (w *work) expose(ctx context.Context, index int) error {
	t := w.target
	settings, err := settingsOf(t.Spec.Sequences[index])
	if err != nil {
		return fail(astrophotography.ReasonExposureFailed, "sequence %d: %w", index, err)
	}
	if err := w.checkSpace(ctx, settings); err != nil {
		return err
	}
	requested := time.Now().UTC()
	frameID := newFrameID(requested)
	progress := aligned(t).Status.Sequences[index]
	file := outbox.Frame{Camera: w.camera.name, Reservation: w.r.name, Target: t.Metadata.Name, Name: promote.Name(t.Spec.Archive.Format, promote.Values{
		Target: t.Metadata.Name, Object: t.Spec.Object, Filter: settings.filter, Exposure: settings.exposure,
		Binning: settings.binning, Gain: settings.gain, Offset: settings.offset,
		Camera: w.camera.name, Telescope: w.res.Spec.Telescope, Train: w.camera.train, Reservation: w.r.name,
		Requested: requested, Longitude: longitudeOf(w.r.o, w.res),
		Number: int(progress.Archived + progress.Pending + 1), FrameID: frameID,
	})}
	prepared := time.Now()
	if err := w.writeFrame(ctx, settings, file, frameID); err != nil {
		return err
	}
	w.r.o.metrics.runner.preparation.Observe(time.Since(prepared).Seconds())
	exposure := &astrophotography.Exposure{
		FrameID: frameID, Sequence: int32(index), Reservation: w.r.name,
		File: file.Path(), StartTime: requested.Truncate(time.Second),
	}
	if err := w.record(exposure, settings); err != nil {
		return err
	}
	if err := w.exposeAndWait(ctx, settings, file); err != nil {
		return err
	}
	w.r.o.metrics.runner.framesExposed.WithLabelValues(t.Metadata.Namespace, t.Metadata.Name).Inc()
	_, err = w.r.count(t, exposure, true, w.res.Status.Session.Outbox.ClaimName,
		fmt.Sprintf("Reservation %s exposes %s, %s.", w.r.name, settings, w.how))
	return err
}

// writeFrame writes the settings of one frame, in plan 01's order: the
// upload, the frame type, the binning, gain, and offset that the
// sequence states, the filter, and the keywords, INDI_CLEAR first and
// FRAMEID last.
func (w *work) writeFrame(ctx context.Context, s sequenceSettings, file outbox.Frame, frameID string) error {
	c, cam := w.r.client, w.cameraDevice
	dir, prefix := file.Upload()
	writes := []func() (indi.Sent, error){
		func() (indi.Sent, error) {
			return c.SetSwitches(cam, "UPLOAD_MODE", map[string]bool{"UPLOAD_LOCAL": true})
		},
		func() (indi.Sent, error) {
			return c.SetTexts(cam, "UPLOAD_SETTINGS", map[string]string{"UPLOAD_DIR": dir, "UPLOAD_PREFIX": prefix})
		},
		func() (indi.Sent, error) {
			return c.SetSwitches(cam, "CCD_FRAME_TYPE", map[string]bool{"FRAME_LIGHT": true})
		},
	}
	if s.binning != nil {
		writes = append(writes, func() (indi.Sent, error) {
			return c.SetNumbers(cam, "CCD_BINNING", map[string]float64{"HOR_BIN": float64(*s.binning), "VER_BIN": float64(*s.binning)})
		})
	}
	if s.gain != nil {
		writes = append(writes, func() (indi.Sent, error) {
			return c.SetNumbers(cam, "CCD_GAIN", map[string]float64{"GAIN": float64(*s.gain)})
		})
	}
	if s.offset != nil {
		writes = append(writes, func() (indi.Sent, error) {
			return c.SetNumbers(cam, "CCD_OFFSET", map[string]float64{"OFFSET": float64(*s.offset)})
		})
	}
	for _, write := range writes {
		if _, err := w.r.settle(ctx, settingTimeout, write); err != nil {
			return err
		}
	}
	if err := w.selectFilter(ctx, s.filter); err != nil {
		return err
	}
	session := w.res.Status.Session.FITSKeywords[w.camera.key]
	for _, k := range frameKeywords(session, w.target.Spec.FITSKeywords, w.target.Spec.Object, frameID) {
		if _, err := w.r.settle(ctx, settingTimeout, func() (indi.Sent, error) {
			return c.SetTexts(cam, "FITS_HEADER", map[string]string{
				"KEYWORD_NAME": k.Name, "KEYWORD_VALUE": k.Value, "KEYWORD_COMMENT": k.Comment,
			})
		}); err != nil {
			return fmt.Errorf("writing the FITS keyword %s: %w", k.Name, err)
		}
	}
	return nil
}

// record writes the frame in flight into status.exposure. Imaging turns
// True with the reason Exposing, except after a failed exposure: that
// condition stays until a frame is counted, so a retry that fails again
// posts no second pair of Events.
func (w *work) record(exposure *astrophotography.Exposure, s sequenceSettings) error {
	wrote := false
	_, err := w.r.o.writeTarget(w.target, func(t *astrophotography.Target, change *statusChange) bool {
		wrote = t.Status.Reservation == w.r.name
		if !wrote {
			return false
		}
		t.Status.Exposure = exposure
		if !imagingReason(t, astrophotography.ReasonExposureFailed) {
			change.setImaging(t, true, astrophotography.ReasonExposing,
				fmt.Sprintf("Reservation %s exposes %s, %s.", w.r.name, s, w.how))
		}
		return true
	})
	if err == nil && !wrote {
		return errClaimLost
	}
	return err
}

// errClaimLost ends the work on a target whose status.reservation no
// longer names the runner's reservation.
var errClaimLost = errors.New("the target's status.reservation names another reservation")

// exposeAndWait starts the exposure and waits for the frame. The driver
// sends the message "[INFO] Image saved to <path>" before CCD_EXPOSURE
// turns Ok (a probe of the pinned indi-simulators image on 2026-10-12),
// so the runner listens for messages before it starts the exposure, and
// checks that the driver saved the file the runner named.
func (w *work) exposeAndWait(ctx context.Context, s sequenceSettings, file outbox.Frame) error {
	listening, stop := context.WithCancel(ctx)
	defer stop()
	saved := savedFrames(listening, w.r.client, w.cameraDevice)
	_, err := w.r.settle(ctx, s.exposure+readoutAllowance, func() (indi.Sent, error) {
		return w.r.client.SetNumbers(w.cameraDevice, "CCD_EXPOSURE", map[string]float64{"CCD_EXPOSURE_VALUE": s.exposure.Seconds()})
	})
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		// A driver that never finishes still exposes, so the runner
		// aborts the exposure before it writes the next one's settings.
		_, _ = w.r.client.SetSwitches(w.cameraDevice, "CCD_ABORT_EXPOSURE", map[string]bool{"ABORT": true})
		return fmt.Errorf("the driver did not finish the exposure of %s in %s", s.exposure, s.exposure+readoutAllowance)
	}
	if err != nil {
		return err
	}
	return w.checkSaved(ctx, saved, file)
}

// imagingReason reports whether a target's Imaging condition has a
// reason.
func imagingReason(t *astrophotography.Target, reason string) bool {
	c, _ := conditions.Find(t.Status.Conditions, astrophotography.ConditionImaging)
	return c.Reason == reason
}
