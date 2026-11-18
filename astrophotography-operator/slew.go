package main

// An INDI mount takes EQUATORIAL_EOD_COORD at the equinox of the date
// (JNow), but a Target gives J2000, the equinox that catalogs use. So
// the runner converts the coordinates with the sky package before each
// slew.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// slewTimeout is how long the runner waits for a slew, for an unpark,
// and for a slew that was already running when the runner started, such
// as one from before an operator restart. A mount crosses the sky in a
// few minutes at its fastest rate.
const slewTimeout = 10 * time.Minute

// mountSettleTime is how long the runner waits after a slew before it
// exposes. The mount reports Ok as soon as it reaches the coordinates,
// but the telescope can still vibrate for a few seconds after a fast
// stop.
const mountSettleTime = 5 * time.Second

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

func (w *work) slew(ctx context.Context) error {
	position, err := targetPosition(w.target)
	if err != nil {
		return fail(astrophotography.ReasonSlewFailed, "%w", err)
	}
	if err := w.setImaging(true, astrophotography.ReasonSlewing,
		fmt.Sprintf("Reservation %s slews to %s", w.r.name, w.target.Spec.Object)); err != nil {
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

// goTo slews the mount to a J2000 position, converted to the equinox of
// the date, and waits mountSettleTime. readyMount has already set the
// mount to track after the slew.
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

// readyMount waits for a slew in progress to end. Then it unparks the
// mount, turns tracking on, and sets the mount to track after a slew.
// It writes each property only when the mount is not already in that
// state, so a mount that is unparked and tracking receives only the
// coordinates.
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
			fmt.Sprintf("Unparked mount %s to slew to %s for target %s", mount, w.target.Spec.Object, w.target.Metadata.Name))
	}
	// INDI's telescope base sets TELESCOPE_TRACK_STATE to Busy when it
	// accepts TRACK_ON, and keeps it Busy for as long as the mount
	// tracks. Settle waits for a state other than Busy and would time
	// out, so for that property the runner waits only for Accepted.
	for _, s := range []struct {
		property, member string
		busyIsOn         bool
	}{
		{"TELESCOPE_TRACK_STATE", "TRACK_ON", true},
		{"ON_COORD_SET", "TRACK", false},
	} {
		p, ok := c.Property(mount, s.property)
		if !ok || switchOn(p, s.member) {
			continue
		}
		sent, err := c.SetSwitches(mount, s.property, map[string]bool{s.member: true})
		if err != nil {
			return err
		}
		answered, cancel := context.WithTimeout(ctx, settingTimeout)
		if s.busyIsOn {
			_, err = c.Accepted(answered, sent)
		} else {
			_, err = c.Settle(answered, sent)
		}
		cancel()
		if err != nil {
			return fmt.Errorf("%s.%s: %w", mount, s.property, err)
		}
	}
	return nil
}

// slewFailure fails the target with the reason SlewFailed. It returns
// the error unchanged when the INDI connection ended or the runner is
// stopping, because those say nothing about whether the target can be
// reached.
func slewFailure(err error) error {
	if lostConnection(err) || errors.Is(err, context.Canceled) {
		return err
	}
	return fail(astrophotography.ReasonSlewFailed, "%w", err)
}
