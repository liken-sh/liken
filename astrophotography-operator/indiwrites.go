package main

// The runner's writes to the INDI server. Each write settles with the
// INDI client's Settle, which reads the driver's answer from the updates
// after the write, and each wait has a limit, because a driver whose
// hardware stops answering never answers at all.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
)

// settingTimeout bounds the answer to one setting, such as the upload
// directory or a FITS keyword. plan 20's drill measured 2 to 43 ms for
// a keyword.
const settingTimeout = 30 * time.Second

// connected waits until the client has a connection to the server.
func (r *runner) connected(ctx context.Context) error {
	return r.client.WaitFor(ctx, func(s *indi.Store) bool { return s.Connected() })
}

// property waits until a device defines a property, and answers it. A
// device that a connected driver serves defines its properties at
// once, so the wait has the limit of one setting.
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

// syncDevices waits until the client holds every property that each
// device defined, before the runner reads a device to decide what to
// write. A new connection receives each device's properties one by one,
// so a read just after one property arrives can miss the next, such as
// a mount's TELESCOPE_PARK after its EQUATORIAL_EOD_COORD. Each sync
// makes the driver define its properties again for every client, so the
// runner syncs once at the start of each target's work.
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

// settle sends one change and waits up to timeout for the driver's
// answer with the values sent.
func (r *runner) settle(ctx context.Context, timeout time.Duration, send func() (indi.Sent, error)) (indi.Property, error) {
	return r.answer(ctx, timeout, send, r.client.Settle)
}

// answer sends one change and waits up to timeout for the driver's
// answer, as wait reads it: Settle for the values sent, or Answered for
// any answer that is not Busy.
func (r *runner) answer(ctx context.Context, timeout time.Duration, send func() (indi.Sent, error), wait func(context.Context, indi.Sent) (indi.Property, error)) (indi.Property, error) {
	sent, err := send()
	if err != nil {
		return indi.Property{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p, err := wait(ctx, sent)
	if err != nil {
		return p, fmt.Errorf("%s.%s: %w", sent.Device, sent.Property, err)
	}
	return p, nil
}

// switchOn reports whether a switch property has one member On.
func switchOn(p indi.Property, member string) bool {
	m, ok := p.Member(member)
	return ok && m.Switch
}
