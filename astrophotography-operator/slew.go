package main

// The slew (plan 01, steps 3 and 4). An INDI mount takes
// EQUATORIAL_EOD_COORD at the equinox of the date, JNow, and a Target
// states J2000, the equinox of every catalog, so the runner converts
// the coordinates with sky before each slew. It reads the mount first,
// so a write goes out only when it is still needed: a mount that is
// unparked and tracking gets only the coordinates.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// slewTimeout bounds a slew, an unpark, and the wait for a slew that a
// run before a restart started. A mount crosses the sky in a few
// minutes at its fastest rate.
const slewTimeout = 10 * time.Minute

// mountSettleTime is the wait after a slew before the first frame. A
// mount reports Ok when it reaches the coordinates, and the tube and
// the pier still shake for a few seconds after a fast stop.
const mountSettleTime = 5 * time.Second

// targetPosition reads a target's J2000 coordinates.
func targetPosition(t *astrophotography.Target) (sky.Position, error) {
	ra, err := sky.ParseRA(t.Spec.Coordinates.RA)
	if err != nil {
		return sky.Position{}, err
	}
	dec, err := sky.ParseDec(t.Spec.Coordinates.Dec)
	if err != nil {
		return sky.Position{}, err
	}
	return sky.Position{RA: ra, Dec: dec}, nil
}

// slew points the mount at the target and waits for it to settle.
func (w *work) slew(ctx context.Context) error {
	position, err := targetPosition(w.target)
	if err != nil {
		return fail(astrophotography.ReasonSlewFailed, "%w", err)
	}
	if err := w.setImaging(true, astrophotography.ReasonSlewing,
		fmt.Sprintf("Reservation %s slews to %s.", w.r.name, w.target.Spec.Object)); err != nil {
		return err
	}
	if err := w.readyMount(ctx); err != nil {
		return slewFailure(err)
	}
	if err := w.goTo(ctx, position); err != nil {
		return slewFailure(err)
	}
	w.aimed = w.target.Spec.Coordinates
	return nil
}

// goTo slews the mount to a J2000 position, converted to the equinox
// of the date, and waits for the tube to settle. The mount must track
// after a slew, which readyMount sets.
func (w *work) goTo(ctx context.Context, position sky.Position) error {
	jnow := sky.ToDate(position, time.Now())
	if _, err := w.r.settle(ctx, slewTimeout, func() (indi.Sent, error) {
		return w.r.client.SetNumbers(w.mountDevice, "EQUATORIAL_EOD_COORD", map[string]float64{"RA": jnow.RAHours(), "DEC": jnow.Dec})
	}); err != nil {
		return err
	}
	if !sleep(ctx, mountSettleTime, nil) {
		return ctx.Err()
	}
	return nil
}

// readyMount waits for a slew in progress to end, then unparks the
// mount, turns tracking on, and sets the mount to track after a slew,
// each only when the mount needs it.
func (w *work) readyMount(ctx context.Context) error {
	c, mount := w.r.client, w.mountDevice
	if _, err := w.r.property(ctx, mount, "EQUATORIAL_EOD_COORD"); err != nil {
		return err
	}
	waiting, cancel := context.WithTimeout(ctx, slewTimeout)
	defer cancel()
	if err := c.WaitFor(waiting, func(s *indi.Store) bool {
		p, ok := s.Property(mount, "EQUATORIAL_EOD_COORD")
		return !ok || p.State != indi.Busy
	}); err != nil {
		return fmt.Errorf("waiting for the slew in progress: %w", err)
	}
	if park, ok := c.Property(mount, "TELESCOPE_PARK"); ok && switchOn(park, "PARK") {
		if _, err := w.r.settle(ctx, slewTimeout, func() (indi.Sent, error) {
			return c.SetSwitches(mount, "TELESCOPE_PARK", map[string]bool{"UNPARK": true})
		}); err != nil {
			return err
		}
		w.r.o.recorder.Normal(reservationRef(w.res), reasonUnparked,
			fmt.Sprintf("astrophotography-operator unparked %s to slew to %s for target %s.", mount, w.target.Spec.Object, w.target.Metadata.Name))
	}
	for _, s := range []struct{ property, member string }{
		{"TELESCOPE_TRACK_STATE", "TRACK_ON"},
		{"ON_COORD_SET", "TRACK"},
	} {
		p, ok := c.Property(mount, s.property)
		if !ok || switchOn(p, s.member) {
			continue
		}
		if _, err := w.r.settle(ctx, settingTimeout, func() (indi.Sent, error) {
			return c.SetSwitches(mount, s.property, map[string]bool{s.member: true})
		}); err != nil {
			return err
		}
	}
	return nil
}

// slewFailure fails the target with SlewFailed, unless the connection
// ended or the runner stops, which end the work without a verdict on
// the target.
func slewFailure(err error) error {
	if lostConnection(err) || errors.Is(err, context.Canceled) {
		return err
	}
	return fail(astrophotography.ReasonSlewFailed, "%w", err)
}
