package main

// A device leaves a running INDI server when a person deletes it, when
// a retry changes the session's devices, and when a release stops the
// telescope's devices. Restarting the server would disconnect every
// other device on it and end an exposure in progress, so the operator
// keeps the server's pod and changes only the drivers it runs.
//
// The operator writes the server's device addresses, one per line, into
// the pod annotation annotationDrivers. The pod mounts the annotation
// as a file through a downward API volume, and indi-shim, which runs
// indiserver, starts and stops drivers to match the file
// (indi/shim/serve.go). An annotation is metadata, so the change does
// not replace the pod. The kubelet updates a downward API volume when
// the pod changes, while a ConfigMap volume changes only at the
// kubelet's next periodic sync of the pod, which can be a minute later.
//
// A server pod that is created again starts with the drivers in its
// annotation, which the operator sets when it creates the pod.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// reasonDriverStopped is the reason of the Event on a Telescope or an
// Observatory when its running server stops the driver of a device
// that left it.
const reasonDriverStopped = "DriverStopped"

// driverStopLimit is how long setDrivers waits for the server to
// report that a leaving device's driver stopped.
const driverStopLimit = 30 * time.Second

// driversMemo records the annotation that the operator last wrote on
// each server's pod. The operator's cached copy of a pod can be older
// than its own patch, and a pass that read that copy would patch the
// pod again and post its Events again.
type driversMemo struct {
	mu      sync.Mutex
	written map[string]writtenDrivers
}

type writtenDrivers struct {
	uid, list string
}

// current returns the value that the operator last wrote on a server's
// pod, or the cached pod's annotation when it wrote none to a pod with
// this UID. A pod created again under the same name has a new UID.
func (m *driversMemo) current(p *pod) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.written[p.Metadata.Name]; ok && w.uid == p.Metadata.UID {
		return w.list
	}
	return p.Metadata.Annotations[annotationDrivers]
}

func (m *driversMemo) wrote(p *pod, list string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.written == nil {
		m.written = map[string]writtenDrivers{}
	}
	m.written[p.Metadata.Name] = writtenDrivers{uid: p.Metadata.UID, list: list}
}

// setDrivers writes the devices of a running server into its pod's
// annotation and returns the pod names of the devices that left. When
// the server's pod does not exist or is being deleted, it changes
// nothing, because ensureServer creates the next pod with the current
// devices.
//
// A leaving device runs its deactivation before its driver stops
// (leaves.go). setDrivers returns after the server reports each leaving
// driver stopped, or after driverStopLimit, so the caller can then
// delete those pods (see stopping).
func (o *operator) setDrivers(ctx context.Context, report func(string), ref serverRef, devices []*device) (left []string, err error) {
	want, err := driverList(devices)
	if err != nil {
		return nil, err
	}
	t := o.snapshot()
	running, ok := t.pods[ref.String()]
	if !ok || running.Metadata.DeletionTimestamp != nil {
		return nil, nil
	}
	have := o.serverDrivers.current(running)
	if have == want {
		return nil, nil
	}
	after := strings.Fields(want)
	for _, address := range strings.Fields(have) {
		if !slices.Contains(after, address) {
			name, _, _ := strings.Cut(address, ":")
			left = append(left, name)
		}
	}
	o.deactivateLeavers(ctx, t, ref, left)
	stopped, cancel := o.stopping(ctx, t, ref, left)
	defer cancel()
	body, err := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]string{annotationDrivers: want}}})
	if err != nil {
		return nil, err
	}
	if err := o.send(ctx, report, "setting the drivers of pod "+ref.String(), func() error {
		return o.client.Request(http.MethodPatch, podPath(o.namespace, ref.String()), mergePatch, body, nil)
	}); err != nil {
		return nil, err
	}
	o.serverDrivers.wrote(running, want)
	return left, stopped()
}

// stopping subscribes to the server's events before setDrivers patches
// the annotation, so no event is lost, and returns a function that
// waits until the server reports each leaving driver stopped. When a
// driver exits, indiserver sends a delProperty for its device with no
// property name.
//
// The caller deletes the device pods only after this wait, because a
// driver whose device pod is deleted first reads EOF on its connection,
// and indiserver starts it again. After driverStopLimit the wait ends
// and the caller deletes the pods anyway, so a driver that does not
// stop cannot keep a device's pod running.
func (o *operator) stopping(ctx context.Context, t *tree, ref serverRef, left []string) (wait func() error, cancel func()) {
	none := func() error { return nil }
	server, open := o.servers.get(ref.String())
	if len(left) == 0 || !open || !server.client.Connected() {
		return none, func() {}
	}
	gone := map[string]string{}
	for _, name := range left {
		if p, ok := t.pods[name]; ok {
			if device := definedBy(server.client, podDriver(p)); device != "" {
				gone[device] = name
			}
		}
	}
	if len(gone) == 0 {
		return none, func() {}
	}
	bounded, cancel := context.WithTimeout(ctx, driverStopLimit)
	events := server.client.Subscribe(bounded)
	return func() error {
		for e := range events {
			switch {
			case e.Kind == indi.Disconnected:
				// No delProperty can arrive on a closed connection,
				// and a restarted server starts only the drivers in
				// the annotation.
				return nil
			case e.Kind == indi.Deleted && e.Property == "":
				delete(gone, e.Device)
			}
			if len(gone) == 0 {
				return nil
			}
		}
		// The subscription closed because bounded ended.
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, device := range slices.Sorted(maps.Keys(gone)) {
			o.logf("server %s did not report the driver of pod %s stopped within %v; deleting the pod anyway", ref, gone[device], driverStopLimit)
		}
		return nil
	}, cancel
}

// definedBy returns the INDI device whose DRIVER_INFO.DRIVER_EXEC is
// the driver program, or "" when the driver has defined none yet.
func definedBy(c *indi.Client, driver string) string {
	for _, name := range c.Devices() {
		info, ok := c.Property(name, "DRIVER_INFO")
		if !ok {
			continue
		}
		if exec, ok := info.Member("DRIVER_EXEC"); ok && exec.Text == driver {
			return name
		}
	}
	return ""
}

// podDriver returns the driver program from the EXEC address of a
// device pod's socat. It reads the pod and not the device's spec,
// because the spec can already name another driver or be deleted.
func podDriver(p *pod) string {
	for _, c := range p.Spec.Containers {
		for _, arg := range c.Args {
			if exec, ok := strings.CutPrefix(arg, "EXEC:"); ok {
				program, _, _ := strings.Cut(exec, ",")
				return program
			}
		}
	}
	return ""
}

// recordDrivers posts an Event on a Telescope or an Observatory for
// each driver that releaseLeft stopped. It names each device from its
// pod's labels in t, because the device object can already be deleted.
func (r *runner) recordDrivers(t *tree, ref serverRef, about events.ObjectReference, left []string) {
	for _, name := range left {
		device := "pod " + name
		if p, ok := t.pods[name]; ok {
			device = p.Metadata.Labels[labelKind] + " " + p.Metadata.Labels[labelResource]
		}
		r.o.recorder.Normal(about, reasonDriverStopped,
			fmt.Sprintf("Stopped the driver of %s on server %s while Reservation %s is %s. The server did not restart", device, ref, r.name, r.status.Phase))
		r.o.logf("Reservation %s: stopped the driver of %s on %s", r.name, device, ref)
	}
}
