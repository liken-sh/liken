package main

// While a reservation is Ready, its runner keeps the telescope as the
// steps left it, until the reservation ends:
//
//   - It creates again each pod that is gone, such as one a node's
//     eviction deleted, and replaces the server when the telescope's
//     devices change, because the server's links name every device.
//   - It connects and configures again each device whose driver comes
//     back on its server disconnected. A driver comes back that way
//     after its pod restarts, and every driver does after the server
//     restarts, with the settings it had lost. The runner acts when
//     the driver defines CONNECTION, and only then: a device that
//     KStars disconnects on purpose stays disconnected.

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// reapplyLimit bounds the work of one device that came back.
const reapplyLimit = 2 * time.Minute

func (r *runner) steady(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	appeared := newAppearances()
	subscribed := map[string]*indi.Client{}
	for ctx.Err() == nil {
		wake := r.o.changed.wait()
		if !r.refresh() || ending(r.res, time.Now()) {
			return
		}
		t := r.o.snapshot()
		ref, telescope, devices, err := r.telescope(t)
		if err != nil {
			r.keepWaiting(ctx, wake)
			continue
		}
		site := serverRef{observatory.ObservatoryKind, telescope.Spec.Observatory}
		for _, s := range []serverRef{ref, site} {
			if server, ok := r.o.servers.get(s.String()); ok && subscribed[s.String()] != server.client {
				subscribed[s.String()] = server.client
				appeared.subscribe(ctx, server, r.o.changed)
			}
		}
		if err := r.keepPods(ctx, t, ref, telescope, devices); err != nil {
			fmt.Fprintf(os.Stderr, "observatory-operator: keeping the pods of %s: %v\n", ref, err)
		}
		r.reapply(ctx, t, ref, appeared)
		if site, ok := t.observatories[telescope.Spec.Observatory]; ok {
			r.reapplySite(ctx, t, site, appeared)
		}
		r.keepWaiting(ctx, wake)
	}
}

// keepWaiting waits for a change, or for spec.end. The timer is a
// clock: spec.end.
func (r *runner) keepWaiting(ctx context.Context, wake <-chan struct{}) {
	var end <-chan time.Time
	if r.res.Spec.End != nil {
		timer := time.NewTimer(time.Until(*r.res.Spec.End))
		defer timer.Stop()
		end = timer.C
	}
	select {
	case <-ctx.Done():
	case <-wake:
	case <-end:
	}
}

// keepPods creates each of the telescope's pods that is gone, and the
// observatory's, and replaces each whose spec changed.
func (r *runner) keepPods(ctx context.Context, t *tree, ref serverRef, telescope *observatory.Telescope, devices []*device) error {
	if err := r.o.startServer(ctx, nil, ref, telescope.Metadata.UID, devices); err != nil {
		return err
	}
	if err := r.o.startDevices(ctx, nil, ref, devices); err != nil {
		return err
	}
	site, ok := t.observatories[telescope.Spec.Observatory]
	if !ok {
		return nil
	}
	siteRef := serverRef{observatory.ObservatoryKind, site.Metadata.Name}
	siteDevices := t.devicesOn(siteRef)
	if len(siteDevices) == 0 {
		return nil
	}
	lock := r.o.siteLock(site.Metadata.Name)
	if err := lock.acquire(ctx); err != nil {
		return err
	}
	defer lock.release()
	if err := r.o.startServer(ctx, nil, siteRef, site.Metadata.UID, siteDevices); err != nil {
		return err
	}
	return r.o.startDevices(ctx, nil, siteRef, siteDevices)
}

// appearances records the INDI devices that defined CONNECTION on a
// server since the runner last looked, by server and device name.
type appearances struct {
	mu      sync.Mutex
	devices map[string]bool
	// counted holds, for each server whose client the runner
	// subscribed to, the devices that counted as come back since the
	// subscription opened. A connection can define its devices before
	// the subscription opens, so every device of such a server counts
	// as come back once. The runner finds a device only after its
	// driver defines DRIVER_INFO, which can be several looks later, so
	// each device counts on its own first look.
	counted map[string]map[string]bool
}

func newAppearances() *appearances {
	return &appearances{devices: map[string]bool{}, counted: map[string]map[string]bool{}}
}

// opened starts the count of a server's devices for a new subscription.
func (a *appearances) opened(server string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.counted[server] = map[string]bool{}
}

func (a *appearances) subscribe(ctx context.Context, server *indiServer, changed *bell) {
	events := server.client.Subscribe(ctx)
	a.opened(server.name)
	go func() {
		for e := range events {
			if e.Kind == indi.Defined && e.Property == "CONNECTION" {
				a.mu.Lock()
				a.devices[server.name+"/"+e.Device] = true
				a.mu.Unlock()
				changed.notify()
			}
		}
	}()
}

// take answers whether a device appeared since the last call.
func (a *appearances) take(server, device string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := server + "/" + device
	appeared := a.devices[key]
	delete(a.devices, key)
	if counted, open := a.counted[server]; open && !counted[device] {
		counted[device] = true
		appeared = true
	}
	return appeared
}

// reapply connects and configures each of the telescope's devices that
// came back disconnected, and turns its Switch outputs on again.
func (r *runner) reapply(ctx context.Context, t *tree, ref serverRef, appeared *appearances) {
	devices := t.devicesOn(ref)
	switches, others := partition(devices)
	all := r.o.handlesOf(t, ref, inOrder(others, connectOrder))
	_, site, err := r.siteOf(t)
	if err != nil {
		return
	}
	for _, h := range r.o.handlesOf(t, ref, switches) {
		if !appeared.take(ref.String(), h.name) || h.connected() {
			continue
		}
		r.again(ctx, h, func(ctx context.Context) error {
			if err := h.connect(ctx); err != nil {
				return err
			}
			_, err := r.o.setOutputs(ctx, r.o.snapshot(), devices, true)
			return err
		})
	}
	for _, h := range all {
		if !appeared.take(ref.String(), h.name) || h.connected() {
			continue
		}
		r.again(ctx, h, func(ctx context.Context) error {
			if err := h.connect(ctx); err != nil {
				return err
			}
			var notes []string
			if _, err := configureDevice(ctx, t, site, h, all, &notes); err != nil {
				return err
			}
			if h.d.kind == observatory.CameraKind && h.d.object.Spec.Temperature != nil {
				// The setpoint goes back, and the cooler works toward
				// it while the holder works. The runner does not wait.
				_, err := optional(ctx, h, "CCD_TEMPERATURE", map[string]float64{"CCD_TEMPERATURE_VALUE": *h.d.object.Spec.Temperature}, &notes)
				return err
			}
			return nil
		})
	}
}

// reapplySite connects each of the observatory's devices that came back
// disconnected, and writes the dome's policies again.
func (r *runner) reapplySite(ctx context.Context, t *tree, site *observatory.Observatory, appeared *appearances) {
	ref := serverRef{observatory.ObservatoryKind, site.Metadata.Name}
	devices := t.devicesOn(ref)
	switches, others := partition(devices)
	ordered := append(switches, inOrder(others, siteOrder)...)
	for _, h := range r.o.handlesOf(t, ref, ordered) {
		if !appeared.take(ref.String(), h.name) || h.connected() {
			continue
		}
		r.again(ctx, h, func(ctx context.Context) error {
			lock := r.o.siteLock(site.Metadata.Name)
			if err := lock.acquire(ctx); err != nil {
				return err
			}
			defer lock.release()
			if err := h.connect(ctx); err != nil {
				return err
			}
			if h.d.kind == observatory.SwitchKind {
				_, err := r.o.setOutputs(ctx, r.o.snapshot(), devices, true)
				return err
			}
			_, err := domePolicies(ctx, t, site, map[string]handle{h.d.key(): h})
			return err
		})
	}
}

// again runs the work for one device that came back, and records a
// failure in the device's status. A failure does not end the
// reservation: the holder may still use the other devices.
func (r *runner) again(ctx context.Context, h handle, work func(context.Context) error) {
	ctx, cancel := context.WithTimeout(ctx, reapplyLimit)
	defer cancel()
	err := h.server.lock.acquire(ctx)
	if err == nil {
		defer h.server.lock.release()
		err = work(ctx)
	}
	r.o.fault(h.d, err)
	if err != nil {
		fmt.Fprintf(os.Stderr, "observatory-operator: %s came back, and setting it up again failed: %v\n", h, err)
	}
}
