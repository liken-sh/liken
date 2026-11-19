package main

// While a reservation is Ready, its runner keeps the telescope as the
// session's steps set it up, until the reservation ends:
//
//   - It creates each pod again that is gone, such as one that a
//     node's eviction deleted, and posts an Event for each pod that it
//     creates (recordPod). When a person deletes a device, it stops the
//     device's driver on the running server (serverdrivers.go) and
//     deletes the device's pod (deletes.go). A device created during
//     the session waits for the next session (tree.server).
//   - It keeps the telescope's guider running (guidersteady.go).
//   - When a driver defines CONNECTION again and the device is
//     disconnected, it connects and configures the device again. A
//     driver does that after its pod restarts, and every driver does
//     after the server restarts, with its settings lost. The driver of
//     a device that KStars disconnects on purpose does not define
//     CONNECTION again, so the device stays disconnected.

import (
	"context"
	"errors"
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

// reapplyLimit bounds reconnecting and configuring one device,
// including the wait for the locks (withLocks).
const reapplyLimit = 2 * time.Minute

// steady runs the loop described at the top of the file until the
// reservation ends or is deleted. It runs a pass each time the tree
// changes or a device defines CONNECTION.
func (r *runner) steady(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	returned := newAppearances()
	// subscribed records the client of each server that returned
	// subscribes to, so the runner subscribes again when a server gets a
	// new client.
	subscribed := map[string]*indi.Client{}
	// lastInputs is the podInputs of the last keepPods that succeeded.
	lastInputs := ""
	// kept holds the replacements of the pods that the kubelet ended.
	// Its timer notifies structure, which this loop waits on, so the wait
	// for a pod's replacement is a deadline of the loop and holds no
	// lock. A keepPods that returns errReplaceLater does not set
	// lastInputs, so the pass at the deadline runs keepPods again.
	r.kept = newReplacements(r.o.structure, true)
	for ctx.Err() == nil {
		// Get the channel before reading any state, so a change during
		// this pass ends the next wait at once.
		wake := r.o.structure.wait()
		if !r.refresh() || ending(r.o.snapshot(), r.res, time.Now()) {
			return
		}
		r.observe(ctx)
		t := r.o.snapshot()
		ref, telescope, devices, err := r.telescope(t)
		if err != nil {
			r.waitForChange(ctx, wake)
			continue
		}
		site := serverRef{observatory.ObservatoryKind, telescope.Spec.Observatory}
		for _, s := range []serverRef{ref, site} {
			if server, ok := r.o.servers.get(s.String()); ok && subscribed[s.String()] != server.client {
				subscribed[s.String()] = server.client
				returned.subscribe(ctx, server, r.o.structure)
			}
		}
		if inputs := podInputs(t, ref, telescope); inputs != lastInputs {
			if err := r.keepPods(ctx, t, ref, telescope, devices); err != nil {
				r.o.logf("keeping the pods of %s: %v", ref, err)
			} else {
				lastInputs = inputs
			}
		}
		r.reapply(ctx, t, ref, returned)
		r.keepGuider(ctx, r.o.snapshot(), ref, telescope)
		if site, ok := t.observatories[telescope.Spec.Observatory]; ok {
			r.reapplySite(ctx, t, site, returned)
		}
		for _, s := range []serverRef{ref, site} {
			r.checkInterfaces(ctx, t, s)
		}
		r.waitForChange(ctx, wake)
	}
}

// waitForChange waits until the tree changes, spec.end passes, or ctx
// ends. It sets the timer only while spec.end is in the future. A timer
// for a past time fires at once, so a runner that retries a failed
// step after spec.end would spin.
func (r *runner) waitForChange(ctx context.Context, wake <-chan struct{}) {
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

// keepPods runs keepServer for the telescope's server, and then for its
// observatory's server under the site's lock, because the runners of
// several telescopes share that server. When the observatory has no
// devices and no server pod, it only removes stray objects.
func (r *runner) keepPods(ctx context.Context, t *tree, ref serverRef, telescope *observatory.Telescope, devices []*device) error {
	later := r.keepServer(ctx, t, ref, telescope.Metadata, devices)
	if later != nil && !errors.Is(later, errReplaceLater) {
		return later
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
		return errors.Join(later, r.removeStrays(ctx, t, t, siteRef))
	}
	return errors.Join(later, r.keepServer(ctx, t, siteRef, site.Metadata, siteDevices))
}

// keepServer creates the server's pod and each device's pod when they
// are gone, and releases the devices that left the server
// (releaseLeft). It does not switch a Switch's outputs on, because a
// new server pod starts every driver disconnected. reapply switches
// them on when the Switch's driver defines CONNECTION.
func (r *runner) keepServer(ctx context.Context, t *tree, ref serverRef, owner observatory.ObjectMeta, devices []*device) error {
	about := reference(ref.kind, owner)
	// A pod that waits for its replacement (errReplaceLater) does not
	// hold up the other pods, and keepServer returns the error at the
	// end, so steady runs keepPods again when the wait ends.
	created, later := r.o.ensureServer(ctx, nil, r.kept, ref, owner.UID, devices)
	if created {
		r.recordServerPod(ref, owner)
	}
	if later != nil && !errors.Is(later, errReplaceLater) {
		return later
	}
	started, err := r.o.startDevices(ctx, nil, r.kept, ref, devices)
	for _, d := range started {
		name, _ := objectName(d.kind, d.name())
		r.recordPod(reference(d.kind, d.object.Metadata), name, "")
	}
	if err != nil && !errors.Is(err, errReplaceLater) {
		return err
	}
	return errors.Join(later, err, r.releaseLeft(ctx, t, t, ref, about))
}

// releaseLeft releases each device that the server still runs but that
// no longer belongs on it. leaveServed deactivates the served devices
// first, because their deactivation goes through the owner's driver.
// Then setDrivers stops the drivers of the devices that left, and
// removeStrays deletes their pods, so a driver stops before its pod is
// deleted. from is the tree that placed the devices on the server
// (removeStrays).
func (r *runner) releaseLeft(ctx context.Context, t, from *tree, ref serverRef, about events.ObjectReference) error {
	if err := r.leaveServed(ctx, t, ref); err != nil {
		return err
	}
	left, err := r.o.setDrivers(ctx, nil, ref, t.devicesOn(ref))
	r.recordDrivers(t, ref, about, left)
	if err != nil {
		return err
	}
	return r.removeStrays(ctx, t, from, ref)
}

// recordServerPod posts the Event for a new INDI server pod on the
// Telescope or Observatory that the server runs for.
func (r *runner) recordServerPod(ref serverRef, meta observatory.ObjectMeta) {
	r.recordPod(reference(ref.kind, meta), ref.String(), "Its drivers start disconnected, and the runner connects each device again")
}

// recordPod posts an Event on an object, and writes a log line, when
// the runner creates the object's pod while the reservation is Ready.
// The status shows a missing pod only until the pod is back, so the
// Event tells a person who runs `kubectl describe` later that the pod
// is new and started without the settings that the reservation's
// holder made. note, when not empty, is a second sentence with no
// period.
func (r *runner) recordPod(about events.ObjectReference, pod, note string) {
	message := fmt.Sprintf("Created pod %s while Reservation %s is Ready", pod, r.name)
	if note != "" {
		message += ". " + note
	}
	r.o.recorder.Normal(about, reasonPodCreated, message)
	r.o.logf("Reservation %s: created pod %s for %s %s", r.name, pod, about.Kind, about.Name)
}

const reasonPodCreated = "PodCreated"

// podInputs returns, as one string, everything that keepPods reads from
// a tree: the owners' UIDs, each device's key, UID, generation, and
// whether it guides, and the resourceVersion of each pod and Service.
// The runner wakes on every status write that the operator makes, and
// those writes do not change the string. While the string equals the
// one from the last keepPods that succeeded, the pods that keepPods
// would build are the pods that run, so the runner skips keepPods. That
// saves building every pod spec and sending a create for each
// ResourceClaimTemplate, which the API server refuses with 409.
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

// appearances records which INDI devices defined CONNECTION on each
// server since the runner last checked them (take).
type appearances struct {
	mu      sync.Mutex
	devices map[string]bool
	// counted records, for each subscribed server, the devices that take
	// has checked since the subscription opened. The client can receive
	// a server's definitions before the subscription opens, so take
	// reports each device once on its first check. The runner finds a
	// device only after its driver defines DRIVER_INFO, which can be
	// several passes later, so each device is counted at its own first
	// check.
	counted map[string]map[string]bool
}

func newAppearances() *appearances {
	return &appearances{devices: map[string]bool{}, counted: map[string]map[string]bool{}}
}

func (a *appearances) opened(server string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.counted[server] = map[string]bool{}
}

// subscribe records each CONNECTION definition that the server's client
// receives, and notifies changed so the runner runs another pass.
func (a *appearances) subscribe(ctx context.Context, server *indiServer, changed *changeNotifier) {
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

// take reports whether the device defined CONNECTION since the last
// call, or is checked for the first time since the subscription
// opened, and clears the record.
func (a *appearances) take(server, device string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := server + "/" + device
	defined := a.devices[key]
	delete(a.devices, key)
	if counted, open := a.counted[server]; open && !counted[device] {
		counted[device] = true
		defined = true
	}
	return defined
}

// reapply reconnects and configures again each of the telescope's
// devices whose driver came back disconnected, and switches a Switch's
// outputs on again. An INDI device can serve several resources, so the
// resource that runs its driver connects it, and then each resource of
// the device is checked and configured.
func (r *runner) reapply(ctx context.Context, t *tree, ref serverRef, returned *appearances) {
	devices := t.devicesOn(ref)
	switches, others := t.partition(devices)
	all := r.o.handlesOf(t, ref, inOrder(others, connectOrder))
	_, site, err := r.siteOf(t)
	if err != nil {
		return
	}
	for _, group := range byDevice(r.o.handlesOf(t, ref, switches)) {
		if !returned.take(ref.String(), group[0].name) || group[0].connected() {
			continue
		}
		r.reapplyDevice(ctx, ref, group, func(ctx context.Context) error {
			if err := connectGroup(ctx, r.o, group); err != nil {
				return err
			}
			_, err := r.o.setOutputs(ctx, r.o.snapshot(), devices, true)
			return err
		})
	}
	for _, group := range byDevice(all) {
		if !returned.take(ref.String(), group[0].name) || group[0].connected() {
			continue
		}
		r.reapplyDevice(ctx, ref, group, func(ctx context.Context) error {
			if err := connectGroup(ctx, r.o, group); err != nil {
				return err
			}
			var notes []string
			for _, h := range group {
				if _, err := r.o.configureDevice(ctx, t, site, h, all, &notes); err != nil {
					return err
				}
				if setpoint := coolSetpoint(h.d); interfaces[h.d.kind].cooler && setpoint != nil && h.serving() {
					// Write the setpoint from the camera's activation
					// again, without waiting for the sensor to reach it.
					if _, err := optional(ctx, h, "CCD_TEMPERATURE", map[string]float64{"CCD_TEMPERATURE_VALUE": *setpoint}, &notes); err != nil {
						return err
					}
				}
			}
			return nil
		})
	}
}

// reapplySite reconnects each of the observatory's devices whose driver
// came back disconnected. For a Switch it switches the outputs on
// again. It configures every other device again and sets the dome
// lock policies again (domePolicies).
func (r *runner) reapplySite(ctx context.Context, t *tree, site *observatory.Observatory, returned *appearances) {
	ref := serverRef{observatory.ObservatoryKind, site.Metadata.Name}
	devices := t.devicesOn(ref)
	switches, others := t.partition(devices)
	ordered := append(switches, inOrder(others, siteOrder)...)
	for _, group := range byDevice(r.o.handlesOf(t, ref, ordered)) {
		if !returned.take(ref.String(), group[0].name) || group[0].connected() {
			continue
		}
		r.reapplyDevice(ctx, ref, group, func(ctx context.Context) error {
			if err := connectGroup(ctx, r.o, group); err != nil {
				return err
			}
			if t.powers(group[0].d) {
				_, err := r.o.setOutputs(ctx, r.o.snapshot(), devices, true)
				return err
			}
			var notes []string
			byKey := map[string]handle{}
			for _, h := range group {
				if _, err := r.o.configureDevice(ctx, t, site, h, nil, &notes); err != nil {
					return err
				}
				byKey[h.d.key()] = h
			}
			_, err := domePolicies(ctx, t, site, byKey)
			return err
		})
	}
}

// byDevice groups handles by their INDI device. The groups follow the
// order of the first handle of each, and in each group the handle of
// the resource that runs the driver comes first. A handle of a served
// resource whose owner is not among the handles is left out, because
// the owner's group is the one that connects the device.
func byDevice(handles []handle) [][]handle {
	var out [][]handle
	at := map[string]int{}
	for _, h := range owners(handles) {
		at[h.name] = len(out)
		out = append(out, []handle{h})
	}
	for _, h := range handles {
		if i, ok := at[h.name]; ok && !h.d.runsDriver() {
			out[i] = append(out[i], h)
		}
	}
	return out
}

// connectGroup connects one INDI device through the resource that runs
// its driver, and then checks each resource of the group (detect).
func connectGroup(ctx context.Context, o *operator, group []handle) error {
	if err := group[0].connect(ctx); err != nil {
		return err
	}
	for _, h := range group {
		o.detect(ctx, h)
	}
	return nil
}

// reapplyDevice runs work for one INDI device under the server's locks
// (withLocks). It records a failure as a fault on the resource that runs
// the driver and does not end the reservation, because the holder can
// still use the other devices. A success clears the fault of each
// resource on the device.
func (r *runner) reapplyDevice(ctx context.Context, ref serverRef, group []handle, work func(context.Context) error) {
	h := group[0]
	if err := withLocks(ctx, r.o.locksOf(ref, h.server), work); err != nil {
		r.o.fault(h.d, err)
		r.o.logf("setting up %s again: %v", h, err)
		return
	}
	for _, member := range group {
		r.o.fault(member.d, nil)
	}
}

// locksOf returns the locks that a runner takes to change the devices
// on a server. The observatory's server is shared by the runners of
// several telescopes, so it takes the site's lock and then the
// server's. A telescope's server takes the server's lock alone.
func (o *operator) locksOf(ref serverRef, server *indiServer) []lock {
	if ref.kind == observatory.ObservatoryKind {
		return []lock{o.siteLock(ref.name), server.lock}
	}
	return []lock{server.lock}
}

// withLocks takes the locks in order, runs work, and releases them, with
// a deadline of reapplyLimit over the waits and the work. Every caller
// that takes a site's lock and its server's lock takes the site's
// first, so two runners cannot deadlock. The deadline keeps a driver
// that never responds from blocking the runner and the locks.
func withLocks(ctx context.Context, locks []lock, work func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, reapplyLimit)
	defer cancel()
	for _, l := range locks {
		if err := l.acquire(ctx); err != nil {
			return err
		}
		defer l.release()
	}
	return work(ctx)
}
