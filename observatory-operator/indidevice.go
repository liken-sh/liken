package main

// Each change to an INDI device first reads the property, and sends
// nothing when the device already has the value. So a step that runs
// again after an operator restart sends only what the device lacks.

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
)

// propertyWait is how long a step waits for a property to be defined.
// Some drivers define a property just after they report the
// connection, such as CAP_PARK of the dust cover simulator. After this
// wait, the step treats the property as one the driver does not have.
const propertyWait = 3 * time.Second

// indiName returns the name of the INDI device that serves a resource,
// or "" until the driver defines it. It finds the device whose
// DRIVER_INFO.DRIVER_EXEC equals the runner's spec.driver.name, because
// socat starts the driver by that name, and a server runs one device
// for each driver (shareddrivers.go). The runner of a served resource
// is the resource that its spec.device names (served.go).
func (t *tree) indiName(c *indi.Client, d *device) string {
	runner, ok := t.runnerOf(d)
	if !ok {
		return ""
	}
	return definedBy(c, runner.object.Spec.Driver.Name)
}

// handle is one INDI device on its server.
type handle struct {
	d      *device
	server *indiServer
	name   string
}

func (h handle) String() string { return h.d.kind.Name + " " + h.d.name() }

func (h handle) client() *indi.Client { return h.server.client }

// property returns a property of the device. It waits up to
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

// switchOn turns one member of a switch property On and returns false
// when it was already On. Under OneOfMany and AtMostOne, the driver
// turns the other members Off.
func (h handle) switchOn(ctx context.Context, property, member string) (bool, error) {
	return h.setSwitches(ctx, property, map[string]bool{member: true})
}

// setSwitches sets members of a switch property On or Off. It returns
// false when every member already had the requested state.
func (h handle) setSwitches(ctx context.Context, property string, values map[string]bool) (bool, error) {
	p, ok := h.property(ctx, property)
	if !ok {
		return false, fmt.Errorf("no %s on %s", property, h)
	}
	same := p.State != indi.Alert
	for name, on := range values {
		m, has := p.Member(name)
		same = same && has && m.Switch == on
	}
	if same && p.State == indi.Busy {
		// An earlier run of this step sent the change before an operator
		// restart, and the driver is still carrying it out. Sending it
		// again could stop it: libindi's telescope aborts a park when
		// TELESCOPE_PARK arrives during the park.
		return true, h.awaitBusy(ctx, property)
	}
	if same {
		return false, nil
	}
	return true, h.settle(ctx, func() (indi.Sent, error) {
		return h.client().SetSwitches(h.name, property, values)
	})
}

// setNumbers sets members of a number property. It returns false when
// every member already had its value.
func (h handle) setNumbers(ctx context.Context, property string, values map[string]float64) (bool, error) {
	p, ok := h.property(ctx, property)
	if !ok {
		return false, fmt.Errorf("no %s on %s", property, h)
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

// setTexts sets members of a text property. It returns false when
// every member already had its value.
func (h handle) setTexts(ctx context.Context, property string, values map[string]string) (bool, error) {
	p, ok := h.property(ctx, property)
	if !ok {
		return false, fmt.Errorf("no %s on %s", property, h)
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

// awaitBusy waits until a Busy property leaves that state. Like
// settle, it fails when the property ends in Alert or the device
// deletes it.
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

func number(p indi.Property, member string) (float64, bool) {
	m, ok := p.Member(member)
	return m.Number, ok
}
