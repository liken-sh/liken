package main

// An INDI camera driver takes a cooler setpoint written to
// CCD_TEMPERATURE, cools or warms toward it, and reports the sensor's
// temperature in the same property.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// noCooler returns why the operator cannot set the camera's cooler, or
// "" when it can.
func noCooler(ctx context.Context, h handle) string {
	p, ok := h.property(ctx, "CCD_TEMPERATURE")
	switch {
	case !ok:
		return fmt.Sprintf("no cooler on %s: no CCD_TEMPERATURE", h)
	case p.Perm == indi.ReadOnly:
		return fmt.Sprintf("no cooler on %s: CCD_TEMPERATURE is read-only", h)
	}
	return ""
}

// coolerOffLimit is the deadline for switching off the cooler at the
// end of warm. warm can get there after the action's timeout has
// passed, so the switch-off needs a deadline of its own.
const coolerOffLimit = 30 * time.Second

// within returns the tolerance around the setpoint in degrees Celsius.
func within(t observatory.Temperature) float64 {
	if t.Within <= 0 {
		return observatory.DefaultWithin
	}
	return t.Within
}

// setTemperature sends a cooler setpoint and waits until the sensor is
// within tolerance of it, the driver replies Alert, or ctx ends. Only
// the reply to this setpoint can fail it, so an Alert state left from
// an earlier setpoint does not fail a retry.
func setTemperature(ctx context.Context, h handle, target, tolerance float64, report func(string)) error {
	sent, err := h.client().SetNumbers(h.name, "CCD_TEMPERATURE", map[string]float64{"CCD_TEMPERATURE_VALUE": target})
	if err != nil {
		return fmt.Errorf("%s: %w", h, err)
	}
	last := math.NaN()
	reached := func(s *indi.Store) bool {
		p, ok := s.Property(h.name, "CCD_TEMPERATURE")
		if !ok {
			return false
		}
		last, _ = number(p, "CCD_TEMPERATURE_VALUE")
		return math.Abs(last-target) <= tolerance
	}
	_, err = h.client().Answered(ctx, sent)
	var alert *indi.AlertError
	if errors.As(err, &alert) {
		return fmt.Errorf("%s: %w", h, err)
	}
	if err == nil {
		err = h.client().WaitFor(ctx, reached)
	}
	if err != nil {
		if p, ok := h.client().Property(h.name, "CCD_TEMPERATURE"); ok {
			last, _ = number(p, "CCD_TEMPERATURE_VALUE")
		}
		return fmt.Errorf("%s stopped at %s before reaching %s: %w", h, quantity(last, 1, "°C"), quantity(target, 1, "°C"), err)
	}
	report(fmt.Sprintf("reached %s on %s", quantity(target, 1, "°C"), h))
	return nil
}

// cool skips a camera whose cooler the operator cannot set.
func cool(ctx context.Context, h handle, t observatory.Temperature, report func(string)) (string, error) {
	if why := noCooler(ctx, h); why != "" {
		return "", skip(why)
	}
	report(fmt.Sprintf("cooling %s to %s", h, quantity(t.Celsius, 1, "°C")))
	if err := setTemperature(ctx, h, t.Celsius, within(t), report); err != nil {
		return "", err
	}
	return fmt.Sprintf("cooled %s to %s", h, quantity(t.Celsius, 1, "°C")), nil
}

// warm raises the setpoint to t.Celsius and then switches the cooler
// off. A cold sensor whose cooler switches off warms by tens of degrees
// in seconds, and that thermal stress can crack it, so the cooler
// brings it up to the setpoint first. A cooler cannot warm a sensor above the air
// temperature, so on a cold night the sensor may never reach the
// setpoint, and the end of wait does not fail the action. wait has
// the action's deadline and ctx does not, so the cooler is still
// switched off after wait ends.
func warm(ctx, wait context.Context, h handle, t observatory.Temperature, report func(string)) (string, error) {
	if why := noCooler(wait, h); why != "" {
		return "", skip(why)
	}
	p, _ := h.client().Property(h.name, "CCD_TEMPERATURE")
	now, _ := number(p, "CCD_TEMPERATURE_VALUE")
	note := fmt.Sprintf("found %s at %s", h, quantity(now, 1, "°C"))
	if now < t.Celsius-within(t) {
		report(fmt.Sprintf("warming %s from %s to %s", h, quantity(now, 1, "°C"), quantity(t.Celsius, 1, "°C")))
		err := setTemperature(wait, h, t.Celsius, within(t), report)
		switch {
		case err == nil:
			note = fmt.Sprintf("warmed %s to %s", h, quantity(t.Celsius, 1, "°C"))
		case ctx.Err() != nil:
			return "", err
		case wait.Err() != nil:
			p, _ := h.client().Property(h.name, "CCD_TEMPERATURE")
			now, _ := number(p, "CCD_TEMPERATURE_VALUE")
			note = fmt.Sprintf("warmed %s to %s by the timeout", h, quantity(now, 1, "°C"))
		default:
			return "", err
		}
	}
	if _, ok := h.client().Property(h.name, "CCD_COOLER"); ok {
		off, cancel := context.WithTimeout(ctx, coolerOffLimit)
		defer cancel()
		changed, err := h.switchOn(off, "CCD_COOLER", "COOLER_OFF")
		if err != nil {
			return "", err
		}
		if changed {
			note += "; switched off the cooler of " + h.String()
		} else {
			note += "; found the cooler of " + h.String() + " off"
		}
	}
	return note, nil
}
