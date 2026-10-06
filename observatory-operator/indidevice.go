package main

// One device on its INDI server, and the changes the operator sends it.
// Each change first reads what the device reports, and sends nothing
// when the device already has the value, so a step that runs again
// after an operator restart sends a device only what it still needs.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
)

// propertyWait bounds the wait for a property that a step needs. A
// driver defines its properties when it starts and when it connects,
// in a burst a few milliseconds long, and some drivers define a
// property just after they report the connection: the dust cover
// simulator defines CAP_PARK after CONNECTION. A property still missing
// after this wait is one the driver does not have, and the step records
// that and goes on.
const propertyWait = 3 * time.Second

// errAmbiguous is the error of two devices on one server that run the
// same driver. INDI names a device after its model, so both drivers
// register one name, and the operator cannot tell them apart.
var errAmbiguous = errors.New("another device on the same server runs the same driver, and INDI gives both one name")

// indiName answers the INDI device that a resource's driver defines on
// its server, or "" while the driver has not defined it. Every driver
// defines DRIVER_INFO, whose DRIVER_EXEC is the program's name, which
// is the resource's spec.driver.name because socat starts the driver by
// that name.
func indiName(c *indi.Client, onServer []*device, d *device) (string, error) {
	driver := d.object.Spec.Driver.Name
	for _, other := range onServer {
		if other.key() != d.key() && other.object.Spec.Driver.Name == driver {
			return "", fmt.Errorf("%s %s: %w (%s)", d.kind.Name, d.name(), errAmbiguous, driver)
		}
	}
	for _, name := range c.Devices() {
		info, ok := c.Property(name, "DRIVER_INFO")
		if !ok {
			continue
		}
		if exec, ok := info.Member("DRIVER_EXEC"); ok && exec.Text == driver {
			return name, nil
		}
	}
	return "", nil
}

// handle is one device on its server.
type handle struct {
	d      *device
	server *indiServer
	name   string
}

func (h handle) String() string { return h.d.kind.Name + " " + h.d.name() }

func (h handle) client() *indi.Client { return h.server.client }

// property answers a property of the device, after it waits up to
// propertyWait for the driver to define it.
func (h handle) property(ctx context.Context, name string) (indi.Property, bool) {
	wait, cancel := context.WithTimeout(ctx, propertyWait)
	defer cancel()
	_ = h.client().WaitFor(wait, func(s *indi.Store) bool {
		_, ok := s.Property(h.name, name)
		return ok
	})
	return h.client().Property(h.name, name)
}

// connected reports whether the driver reports its device connected.
func (h handle) connected() bool {
	p, ok := h.client().Property(h.name, "CONNECTION")
	return ok && p.State != indi.Alert && slices.Equal(p.On(), []string{"CONNECT"})
}

func (h handle) connect(ctx context.Context) error {
	if err := h.client().ConnectDevice(ctx, h.name); err != nil {
		return fmt.Errorf("%s: connecting %s: %w", h, h.name, err)
	}
	return nil
}

func (h handle) disconnect(ctx context.Context) error {
	if err := h.client().DisconnectDevice(ctx, h.name); err != nil {
		return fmt.Errorf("%s: disconnecting %s: %w", h, h.name, err)
	}
	return nil
}

// switchOn turns one member of a switch property On, and answers false
// when it was On already. Under OneOfMany and AtMostOne, the others
// turn Off.
func (h handle) switchOn(ctx context.Context, property, member string) (bool, error) {
	return h.setSwitches(ctx, property, map[string]bool{member: true})
}

// setSwitches turns members of a switch property On or Off, and answers
// false when every member had its state already.
func (h handle) setSwitches(ctx context.Context, property string, values map[string]bool) (bool, error) {
	p, ok := h.property(ctx, property)
	if !ok {
		return false, fmt.Errorf("%s defines no %s", h, property)
	}
	same := p.State != indi.Alert
	for name, on := range values {
		m, has := p.Member(name)
		same = same && has && m.Switch == on
	}
	if same && p.State == indi.Busy {
		// The change runs already, sent by this step before an operator
		// restart. Sending it again would stop it: libindi's telescope
		// aborts a park when TELESCOPE_PARK arrives during the park.
		return true, h.awaitBusy(ctx, property)
	}
	if same {
		return false, nil
	}
	return true, h.settle(ctx, func() (indi.Sent, error) {
		return h.client().SetSwitches(h.name, property, values)
	})
}

// setNumbers gives members of a number property new values, and answers
// false when every member had its value already.
func (h handle) setNumbers(ctx context.Context, property string, values map[string]float64) (bool, error) {
	p, ok := h.property(ctx, property)
	if !ok {
		return false, fmt.Errorf("%s defines no %s", h, property)
	}
	same := p.State != indi.Alert
	for name, value := range values {
		m, has := p.Member(name)
		same = same && has && math.Abs(m.Number-value) <= 1e-9*math.Max(1, math.Abs(value))
	}
	if same {
		return false, nil
	}
	return true, h.settle(ctx, func() (indi.Sent, error) {
		return h.client().SetNumbers(h.name, property, values)
	})
}

// setTexts gives members of a text property new values, and answers
// false when every member had its value already.
func (h handle) setTexts(ctx context.Context, property string, values map[string]string) (bool, error) {
	p, ok := h.property(ctx, property)
	if !ok {
		return false, fmt.Errorf("%s defines no %s", h, property)
	}
	same := p.State != indi.Alert
	for name, value := range values {
		m, has := p.Member(name)
		same = same && has && m.Text == value
	}
	if same {
		return false, nil
	}
	return true, h.settle(ctx, func() (indi.Sent, error) {
		return h.client().SetTexts(h.name, property, values)
	})
}

func (h handle) settle(ctx context.Context, send func() (indi.Sent, error)) error {
	sent, err := send()
	if err == nil {
		_, err = h.client().Settle(ctx, sent)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", h, err)
	}
	return nil
}

// awaitBusy waits until a Busy property ends, and fails as settle does
// when it ends in Alert or the device deletes it.
func (h handle) awaitBusy(ctx context.Context, property string) error {
	err := h.client().WaitFor(ctx, func(s *indi.Store) bool {
		p, ok := s.Property(h.name, property)
		return !ok || p.State != indi.Busy
	})
	if err != nil {
		return fmt.Errorf("%s: waiting for %s: %w", h, property, err)
	}
	p, ok := h.client().Property(h.name, property)
	switch {
	case !ok:
		return fmt.Errorf("%s: %w: waiting for %s", h, indi.ErrDeleted, property)
	case p.State == indi.Alert:
		return fmt.Errorf("%s: %w", h, &indi.AlertError{Property: p})
	}
	return nil
}

// number answers one member's value.
func number(p indi.Property, member string) (float64, bool) {
	m, ok := p.Member(member)
	return m.Number, ok
}
