package main

// While a reservation is Ready, its runner keeps the telescope as the
// steps left it, until the reservation ends:
//
//   - It creates again each pod that is gone, such as one a node's
//     eviction deleted, and records an Event for each device pod and
//     guider pod that it creates (recordPod). When the telescope's
//     devices change, it starts and stops their drivers on the running
//     server (serverdrivers.go), and deletes the pod of each device
//     that left (moves.go).
//   - It keeps the telescope's guider (guidersteady.go).
//   - It connects and configures again each device whose driver comes
//     back on its server disconnected. A driver comes back that way
//     after its pod restarts, and every driver does after the server
//     restarts, with the settings it had lost. The runner acts when
//     the driver defines CONNECTION, and only then: a device that
//     KStars disconnects on purpose stays disconnected.

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
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
	// kept records the inputs of the last keepPods that succeeded.
	kept := ""
	for ctx.Err() == nil {
		wake := r.o.structure.wait()
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
				appeared.subscribe(ctx, server, r.o.structure)
			}
		}
		if inputs := podInputs(t, ref, telescope); inputs != kept {
			if err := r.keepPods(ctx, t, ref, telescope, devices); err != nil {
				r.o.logf("keeping the pods of %s: %v", ref, err)
			} else {
				kept = inputs
			}
		}
		r.reapply(ctx, t, ref, appeared)
		r.keepGuider(ctx, r.o.snapshot(), ref, telescope)
		if site, ok := t.observatories[telescope.Spec.Observatory]; ok {
			r.reapplySite(ctx, t, site, appeared)
		}
		r.keepWaiting(ctx, wake)
	}
}

// keepWaiting waits for a change, or for spec.end. The timer is a
// clock: spec.end, while it is still ahead. A spec.end that passed
// wakes nothing, or a failed step that waits for its retry after
// spec.end would wake again at once, without end.
func (r *runner) keepWaiting(ctx context.Context, wake <-chan struct{}) {
	var end <-chan time.Time
	if r.res.Spec.End != nil && time.Now().Before(*r.res.Spec.End) {
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

// keepPods keeps the pods of the telescope's server and of the
// observatory's server (keepServer).
func (r *runner) keepPods(ctx context.Context, t *tree, ref serverRef, telescope *observatory.Telescope, devices []*device) error {
	if err := r.keepServer(ctx, t, ref, telescope.Metadata, devices); err != nil {
		return err
	}
	site, ok := t.observatories[telescope.Spec.Observatory]
	if !ok {
		return nil
	}
	siteRef := serverRef{observatory.ObservatoryKind, site.Metadata.Name}
	siteDevices := t.devicesOn(siteRef)
	lock := r.o.siteLock(site.Metadata.Name)
	if err := lock.acquire(ctx); err != nil {
		return err
	}
	defer lock.release()
	if _, running := t.pods[siteRef.String()]; len(siteDevices) == 0 && !running {
		return r.removeStrays(ctx, t, siteRef)
	}
	return r.keepServer(ctx, t, siteRef, site.Metadata, siteDevices)
}

// keepServer creates one server's pod when it is gone, creates the pod
// of each of its devices that is gone, sets the server's drivers to the
// devices, and deletes the pods of the devices that left. The order
// gives a joining device its pod before its driver, and stops a leaving
// device's driver before its pod goes (serverdrivers.go).
func (r *runner) keepServer(ctx context.Context, t *tree, ref serverRef, owner observatory.ObjectMeta, devices []*device) error {
	about := reference(ref.kind, owner)
	created, err := r.o.ensureServer(ctx, nil, ref, owner.UID, devices)
	if created {
		r.recordServerPod(ref, owner)
	}
	if err != nil {
		return err
	}
	started, err := r.o.startDevices(ctx, nil, ref, devices)
	r.recordDevicePods(started)
	if err != nil {
		return err
	}
	joined, left, err := r.o.setDrivers(ctx, nil, ref, devices)
	r.recordDrivers(t, ref, about, joined, left)
	if err != nil {
		return err
	}
	return r.removeStrays(ctx, t, ref)
}

// recordDevicePods writes an Event and a log line for each device whose
// pod the runner created.
func (r *runner) recordDevicePods(devices []*device) {
	for _, d := range devices {
		name, _ := objectName(d.kind, d.name())
		r.recordPod(reference(d.kind, d.object.Metadata), name, "")
	}
}

// recordServerPod writes an Event and a log line when the runner
// created the pod of an INDI server, on the Telescope or the
// Observatory that the server runs for.
func (r *runner) recordServerPod(ref serverRef, meta observatory.ObjectMeta) {
	r.recordPod(reference(ref.kind, meta), ref.String(), "Its drivers start disconnected, and the runner connects each device again.")
}

// recordPod writes an Event on an object and a log line when the runner
// creates the object's pod while the reservation is Ready. The status
// shows the gap only while the pod is gone, and a person reading
// `kubectl describe` later needs to know that the pod is new, because
// a new pod starts with nothing that the holder set. note follows the
// first sentence of the Event's message.
func (r *runner) recordPod(about events.ObjectReference, pod, note string) {
	message := fmt.Sprintf("Created pod %s while Reservation %s is Ready.", pod, r.name)
	if note != "" {
		message += " " + note
	}
	r.o.recorder.Normal(about, reasonPodCreated, message)
	r.o.logf("Reservation %s: created pod %s for %s %s", r.name, pod, about.Kind, about.Name)
}

// reasonPodCreated is the reason of the Event for a pod that a Ready
// reservation's runner creates.
const reasonPodCreated = "PodCreated"

// podInputs answers everything that keepPods reads from a tree, as one
// string: the owners' UIDs, each device's identity, spec generation,
// and guide camera placement, and the version of each pod and Service
// that runs. The runner wakes on each status write of the operator's
// own, and none of them changes this string. While it holds, the
// pods that keepPods builds and the pods that run are the same as at
// its last pass, so the runner skips the pass: the build of each pod
// spec, and the create of each claim, which the API server answers
// with 409.
func podInputs(t *tree, ref serverRef, telescope *observatory.Telescope) string {
	var b strings.Builder
	devices := func(server serverRef, owner string) {
		fmt.Fprintf(&b, "%s %s\n", server, owner)
		for _, d := range t.devicesOn(server) {
			fmt.Fprintf(&b, "%s %s %d %t\n", d.key(), d.object.Metadata.UID, d.object.Metadata.Generation, t.guides(server, d))
		}
	}
	devices(ref, telescope.Metadata.UID)
	if site, ok := t.observatories[telescope.Spec.Observatory]; ok {
		devices(serverRef{observatory.ObservatoryKind, site.Metadata.Name}, site.Metadata.UID)
	}
	for _, name := range slices.Sorted(maps.Keys(t.pods)) {
		fmt.Fprintf(&b, "pod %s %s\n", name, t.pods[name].Metadata.ResourceVersion)
	}
	for _, name := range slices.Sorted(maps.Keys(t.services)) {
		fmt.Fprintf(&b, "service %s %s\n", name, t.services[name].Metadata.ResourceVersion)
	}
	return b.String()
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
			if setpoint := coolSetpoint(h.d); h.d.kind == observatory.CameraKind && setpoint != nil {
				// The setpoint of the camera's activation goes back, and
				// the cooler works toward it while the holder works. The
				// runner does not wait.
				_, err := optional(ctx, h, "CCD_TEMPERATURE", map[string]float64{"CCD_TEMPERATURE_VALUE": *setpoint}, &notes)
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
		r.o.logf("setting up %s again failed: %v", h, err)
	}
}
