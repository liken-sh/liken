package main

// After each write to the INDI server, the runner waits for the
// driver's reply in the property updates that follow. Every wait has a
// timeout, because a driver whose hardware stops responding never
// replies.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
)

// settingTimeout limits each wait for a reply that a driver sends at
// once: a property definition, a sync, or one setting such as the
// upload directory or a FITS keyword.
const settingTimeout = 30 * time.Second

func (r *runner) connected(ctx context.Context) error {
	return r.client.WaitFor(ctx, func(s *indi.Store) bool { return s.Connected() })
}

// property waits until device defines the property name, and returns
// it.
func (r *runner) property(ctx context.Context, device, name string) (indi.Property, error) {
	ctx, cancel := context.WithTimeout(ctx, settingTimeout)
	defer cancel()
	var p indi.Property
	err := r.client.WaitFor(ctx, func(s *indi.Store) bool {
		var ok bool
		p, ok = s.Property(device, name)
		return ok
	})
	if errors.Is(err, context.DeadlineExceeded) {
		return p, fmt.Errorf("%w: %s does not define %s", indi.ErrNotDefined, device, name)
	}
	return p, err
}

// syncDevices waits until the client has every property of each
// device, so the runner reads the whole device before it chooses what
// to write. After a connection the properties arrive one at a time, and
// a read can see a mount's EQUATORIAL_EOD_COORD before its
// TELESCOPE_PARK has arrived. indiserver sends the definitions of a
// sync to every client, so the runner syncs only at the start of each
// Target's work. An empty device name is skipped.
func (r *runner) syncDevices(ctx context.Context, devices ...string) error {
	for _, device := range devices {
		if device == "" {
			continue
		}
		if _, err := r.property(ctx, device, "CONNECTION"); err != nil {
			return err
		}
		syncing, cancel := context.WithTimeout(ctx, settingTimeout)
		err := r.client.Sync(syncing, device)
		cancel()
		if err != nil {
			return fmt.Errorf("reading the properties of %s: %w", device, err)
		}
	}
	return nil
}

// settle sends one change and waits up to timeout until the driver
// reports the values that were sent.
func (r *runner) settle(ctx context.Context, timeout time.Duration, send func() (indi.Sent, error)) (indi.Property, error) {
	return r.sendAndWait(ctx, timeout, send, r.client.Settle)
}

// sendAndWait sends one change and waits up to timeout for the reply that
// wait accepts: r.client.Settle accepts only a reply with the values
// that were sent, and r.client.Answered accepts any values.
func (r *runner) sendAndWait(ctx context.Context, timeout time.Duration, send func() (indi.Sent, error), wait func(context.Context, indi.Sent) (indi.Property, error)) (indi.Property, error) {
	sent, err := send()
	if err != nil {
		return indi.Property{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p, err := wait(ctx, sent)
	if err != nil {
		return p, fmt.Errorf("waiting for the driver to report %s.%s: %w", sent.Device, sent.Property, err)
	}
	return p, nil
}

// switchOn reports whether the named member of a switch property is On.
func switchOn(p indi.Property, member string) bool {
	m, ok := p.Member(member)
	return ok && m.Switch
}
