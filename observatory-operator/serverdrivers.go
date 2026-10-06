package main

// A device can join or leave a server while a reservation is Ready. A
// restart of the server would disconnect every other device on it and
// end an exposure in progress, so the server's pod does not change.
// The operator writes the server's devices in the annotation
// annotationDrivers, one address on each line. The pod mounts the
// annotation as a file through a downward API volume, and indi-shim,
// which runs indiserver, reads the file on each change. It writes
// `start <link>` for each device that joined and `stop <link>` for each
// device that left to indiserver's -f fifo (indi/shim/serve.go).
//
// The annotation is metadata, so it is not in the digest of the pod's
// spec, and a change of the devices replaces no pod. A change reaches
// the shim without the operator running a command in the pod: the
// kubelet remounts a downward API volume on each update of its pod, so
// the file changes within about a second. A ConfigMap volume changes
// only at the kubelet's next sync of the pod, up to about a minute
// later.
//
// A Ready reservation's runner orders the work for a device that joins
// and one that leaves (steady.go). A joining device gets its pod first
// and then its driver, so the shim dials a pod that exists. A leaving
// device loses its driver first and then its pod, so indiserver does
// not restart a shim whose pod is gone.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/liken-sh/liken/kubernetes/events"
)

const (
	// reasonDriverStarted is the reason of the Event on a Telescope or
	// an Observatory whose running server started the driver of a
	// device that joined it.
	reasonDriverStarted = "DriverStarted"
	// reasonDriverStopped is the reason of the Event on a Telescope or
	// an Observatory whose running server stopped the driver of a
	// device that left it.
	reasonDriverStopped = "DriverStopped"
)

// driversMemo records the annotation that the operator wrote last on
// each server's pod. The store's copy of a pod can be older than the
// operator's own patch. A pass that read the older copy would patch
// again and post its Events again.
type driversMemo struct {
	mu      sync.Mutex
	written map[string]writtenDrivers
}

// writtenDrivers is one patch: the UID of the pod it changed, and the
// annotation it wrote.
type writtenDrivers struct {
	uid, list string
}

// current answers the devices of a server's pod: the annotation that
// the operator wrote last on this pod, or the store's copy of it. A
// pod with another UID is a new pod, and the operator wrote nothing on
// it yet.
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

// setDrivers writes the devices of a running server in its pod's
// annotation, and answers the devices that joined the server and the
// pods of the devices that left it. A server whose pod is gone has
// nothing to change: the next pod starts with the devices of the pod
// that ensure creates.
func (o *operator) setDrivers(ctx context.Context, report func(string), ref serverRef, devices []*device) (joined []*device, left []string, err error) {
	want, err := driverList(devices)
	if err != nil {
		return nil, nil, err
	}
	running, ok := o.snapshot().pods[ref.String()]
	if !ok || running.Metadata.DeletionTimestamp != nil {
		return nil, nil, nil
	}
	have := o.serverDrivers.current(running)
	if have == want {
		return nil, nil, nil
	}
	body, err := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]string{annotationDrivers: want}}})
	if err != nil {
		return nil, nil, err
	}
	if err := o.send(ctx, report, "setting the drivers of pod "+ref.String(), func() error {
		return o.client.Request(http.MethodPatch, podPath(o.namespace, ref.String()), mergePatch, body, nil)
	}); err != nil {
		return nil, nil, err
	}
	o.serverDrivers.wrote(running, want)
	before := strings.Fields(have)
	for _, d := range devices {
		object, _ := objectName(d.kind, d.name())
		if !slices.Contains(before, object+":"+strconv.Itoa(devicePort)) {
			joined = append(joined, d)
		}
	}
	after := strings.Fields(want)
	for _, address := range before {
		if !slices.Contains(after, address) {
			name, _, _ := strings.Cut(address, ":")
			left = append(left, name)
		}
	}
	return joined, left, nil
}

// recordDrivers posts an Event on a Telescope or an Observatory for
// each driver that its running server started or stopped. t names the
// device of each pod that left, from the pod's labels, because the
// device itself can be deleted already.
func (r *runner) recordDrivers(t *tree, ref serverRef, about events.ObjectReference, joined []*device, left []string) {
	for _, d := range joined {
		r.o.recorder.Normal(about, reasonDriverStarted,
			fmt.Sprintf("Started the driver of %s %s on server %s while Reservation %s is Ready. The server did not restart.", d.kind.Name, d.name(), ref, r.name))
		r.o.logf("Reservation %s: started the driver of %s on %s", r.name, d.key(), ref)
	}
	for _, name := range left {
		device := "pod " + name
		if p, ok := t.pods[name]; ok {
			device = p.Metadata.Labels[labelKind] + " " + p.Metadata.Labels[labelResource]
		}
		r.o.recorder.Normal(about, reasonDriverStopped,
			fmt.Sprintf("Stopped the driver of %s on server %s while Reservation %s is Ready. The server did not restart.", device, ref, r.name))
		r.o.logf("Reservation %s: stopped the driver of %s on %s", r.name, device, ref)
	}
}
