package main

// The steps create the pods, Services, and claim templates of the INDI
// servers and their devices, wait for the pods to be Ready and for each
// driver to define its device, and delete the objects when a session
// ends.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// ensure creates a pod, its Service, and its claim template, or
// replaces a pod that the kubelet ended (keepPod) or whose spec or
// server differs from built (current). The new pod has the old pod's
// name, so ensure waits until the old pod is gone. send retries a write
// that the API server refuses, until the step's deadline. created reports whether the API
// server created the pod, so the runner of a Ready reservation can post
// an Event for each pod that it creates.
func (o *operator) ensure(ctx context.Context, report func(string), carried *replacements, built *pod, svc *service, template *claimTemplate) (created bool, err error) {
	name := built.Metadata.Name
	if carried == nil {
		carried = newReplacements(o.changed, false)
	}
	err = o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		if template != nil {
			// A claim template's spec is immutable, and an existing one is
			// the operator's own, so create treats the 409 Conflict for it
			// as success.
			if err := o.send(ctx, report, fmt.Sprintf("creating ResourceClaimTemplate %s", name), func() error {
				return o.create("/apis/resource.k8s.io/v1/namespaces/"+o.namespace+"/resourceclaimtemplates", template)
			}); err != nil {
				return false, "", err
			}
		}
		// A Service's spec depends only on its name, so an existing
		// Service is current.
		if _, ok := t.services[svc.Metadata.Name]; !ok {
			if err := o.send(ctx, report, fmt.Sprintf("creating Service %s", name), func() error { return o.create("/api/v1/namespaces/"+o.namespace+"/services", svc) }); err != nil {
				return false, "", err
			}
		}
		result, err := o.keepPod(ctx, report, t, built, carried)
		created = created || result.created
		if err != nil {
			return false, "", err
		}
		if !result.due.IsZero() && carried.later {
			return false, "", fmt.Errorf("%w: %s", errReplaceLater, result.waiting)
		}
		// A pod that keepPod just created, or found created by an earlier
		// pass, is not in this tree yet, and it is built from built.
		running, ok := t.pods[name]
		if !result.done || !ok {
			return result.done, result.waiting, nil
		}
		if !current(running, built) {
			if err := o.send(ctx, report, fmt.Sprintf("replacing pod %s: its spec or its server changed", name), func() error { return o.deleteObject(podPath(o.namespace, name)) }); err != nil {
				return false, "", err
			}
			return false, "replacing the pod " + name + ", whose spec or server changed", nil
		}
		return true, "", nil
	})
	return created, err
}

// startServer creates the pod and the Service of one INDI server, and
// sets the server's drivers to the drivers of the devices.
func (o *operator) startServer(ctx context.Context, report func(string), ref serverRef, ownerUID string, devices []*device) (created bool, err error) {
	created, err = o.ensureServer(ctx, report, nil, ref, ownerUID, devices)
	if err != nil {
		return created, err
	}
	_, err = o.setDrivers(ctx, report, ref, devices)
	return created, err
}

// ensureServer creates the pod and the Service of one INDI server, with
// the devices in its annotation, when the pod is gone. setDrivers
// changes the devices of a running server.
func (o *operator) ensureServer(ctx context.Context, report func(string), carried *replacements, ref serverRef, ownerUID string, devices []*device) (created bool, err error) {
	built, svc, err := serverPod(o.namespace, ref, ownerUID, devices)
	if err != nil {
		return false, err
	}
	return o.ensure(ctx, report, carried, built, svc, nil)
}

// startDevices adds the finalizer to each device that runs a driver and
// to each device that the driver serves, then creates the pod, Service,
// and claim template of the device that runs the driver. It returns the
// devices whose pod it created.
func (o *operator) startDevices(ctx context.Context, report func(string), carried *replacements, ref serverRef, devices []*device) ([]*device, error) {
	t := o.snapshot()
	var started []*device
	var later error
	for _, d := range driverDevices(devices) {
		built, svc, template, err := devicePod(o.namespace, ref, d, t.guides(ref, d), t.outbox(ref, d))
		if err != nil {
			o.fault(d, err)
			return started, fmt.Errorf("%s %s: %w", d.kind.Name, d.name(), err)
		}
		if err := o.addDeviceFinalizer(ctx, report, d); err != nil {
			return started, err
		}
		if err := o.addServedFinalizers(ctx, report, t, d); err != nil {
			return started, err
		}
		created, err := o.ensure(ctx, report, carried, built, svc, template)
		if created {
			started = append(started, d)
		}
		switch {
		case errors.Is(err, errReplaceLater):
			// The other devices' pods do not wait for this one.
			later = errors.Join(later, err)
		case err != nil:
			return started, err
		}
	}
	return started, later
}

// waitReady waits until the pod of each device on ref is Ready. A pod
// with a claim for real hardware stays Pending until its device
// appears, so activation waits here until a person or a Switch powers
// the device on. keeper creates a pod again that is gone or ended.
func (o *operator) waitReady(ctx context.Context, report func(string), keeper *podKeeper, ref serverRef, devices []*device) error {
	return o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		for _, d := range driverDevices(devices) {
			name, err := objectName(d.kind, d.name())
			if err != nil {
				return false, "", err
			}
			if acted, waiting, err := keeper.keepDevice(ctx, report, t, ref, d, name); acted || err != nil {
				return false, waiting, err
			}
			p, ok := t.pods[name]
			if !ok || !p.ready() {
				phase := "not created"
				if ok {
					phase = firstNonEmpty(p.Status.Phase, "Pending") + unschedulable(p)
				}
				return false, fmt.Sprintf("waiting for pod %s of %s %s (%s)", name, d.kind.Name, d.name(), phase), nil
			}
		}
		return true, "", nil
	})
}

// waitServer waits until the operator's INDI connection to a server is
// open. keeper creates the server's pod again when it is gone or ended.
func (o *operator) waitServer(ctx context.Context, report func(string), keeper *podKeeper, ref serverRef) (*indiServer, error) {
	var server *indiServer
	err := o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		if acted, waiting, err := keeper.keepServer(ctx, report, t, ref); acted || err != nil {
			return false, waiting, err
		}
		s, ok := o.servers.get(ref.String())
		if !ok || !s.client.Connected() {
			return false, "waiting for the INDI connection to " + ref.String(), nil
		}
		server = s
		return true, "", nil
	})
	return server, err
}

// waitDefined waits until the driver of each device defines its INDI
// device on the server, and returns a handle for each, keyed by device
// key. keeper creates the server's pod again while the wait for the
// connection runs (waitServer).
func (o *operator) waitDefined(ctx context.Context, report func(string), keeper *podKeeper, ref serverRef, devices []*device) (map[string]handle, error) {
	server, err := o.waitServer(ctx, report, keeper, ref)
	if err != nil {
		return nil, err
	}
	handles := map[string]handle{}
	err = o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		waiting := ""
		for _, d := range devices {
			if _, found := handles[d.key()]; found {
				continue
			}
			runner, ok := t.runnerOf(d)
			if !ok {
				err := fmt.Errorf("%s %s: %s", d.kind.Name, d.name(), firstNonEmpty(t.ownershipOf(d).refusal, "spec.device names no resource that runs a driver"))
				o.fault(d, err)
				return false, "", err
			}
			if holder, taken := t.driverHolder(runner); taken {
				err := fmt.Errorf("%s %s: %s", d.kind.Name, d.name(), sharedDriver(holder, ref))
				o.fault(d, err)
				return false, "", err
			}
			name := t.indiName(server.client, d)
			if name == "" {
				waiting = firstNonEmpty(waiting, fmt.Sprintf("waiting for driver %s of %s %s on %s", runner.object.Spec.Driver.Name, runner.kind.Name, runner.name(), ref))
				continue
			}
			handles[d.key()] = handle{d: d, server: server, name: name}
		}
		return waiting == "", waiting, nil
	})
	return handles, err
}

// handlesOf returns a handle for each device whose driver has already
// defined its INDI device on the server, without waiting, for a step
// that ends a reservation.
//
// A restarting driver can define its device while handlesOf reads the
// INDI client's store. So handlesOf looks up the INDI name once for each
// driver, and a served device gets the name of its owner's lookup. With
// a lookup for each device, a served filter wheel listed before its
// camera could find no INDI device while the camera, a moment later,
// found one, and the camera's configuration would have no filter wheel
// in ACTIVE_FILTER.
func (o *operator) handlesOf(t *tree, ref serverRef, devices []*device) []handle {
	server, ok := o.servers.get(ref.String())
	if !ok || !server.client.Connected() {
		return nil
	}
	names := map[*device]string{}
	var out []handle
	for _, d := range devices {
		runner, ok := t.runnerOf(d)
		if !ok {
			continue
		}
		if _, taken := t.driverHolder(runner); taken {
			continue
		}
		name, read := names[runner]
		if !read {
			name = t.indiName(server.client, d)
			names[runner] = name
		}
		if name != "" {
			out = append(out, handle{d: d, server: server, name: name})
		}
	}
	return out
}

// stopPods deletes the pods, Services, ConfigMaps, and claim templates
// whose labels selected accepts, and waits until the pods are gone. It
// returns the names of the pods that it stopped.
func (o *operator) stopPods(ctx context.Context, report func(string), selected func(labels map[string]string) bool) ([]string, error) {
	stopped := o.snapshot().podNames(selected)
	err := o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		if err := o.deleteSelected(ctx, report, t, selected); err != nil {
			return false, "", err
		}
		left := t.podNames(selected)
		return len(left) == 0, "stopping pods " + strings.Join(left, ", "), nil
	})
	return stopped, err
}

// podNames returns the sorted names of the pods whose labels selected
// accepts.
func (t *tree) podNames(selected func(labels map[string]string) bool) []string {
	var names []string
	for name, p := range t.pods {
		if selected(p.Metadata.Labels) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// deleteSelected sends the deletes for the objects that selectedObjects
// finds, and does not wait for them to finish.
func (o *operator) deleteSelected(ctx context.Context, report func(string), t *tree, selected func(labels map[string]string) bool) error {
	for _, object := range t.selectedObjects(o.namespace, selected) {
		if err := o.send(ctx, report, "deleting "+object.what, func() error { return o.deleteObject(object.path) }); err != nil {
			return err
		}
	}
	return nil
}

// selectedObject is an object to delete. what is its kind and name for
// a message, such as "pod east-mount".
type selectedObject struct{ what, path string }

// selectedObjects returns the objects whose labels selected accepts:
// each pod followed by its claim template, then the Services, each
// device Service preceded by its claim template, then the ConfigMaps.
//
// A device's pod, Service, and claim template share one name, and the
// claim that the pod made from the template is deleted with the pod.
// The operator does not watch claim templates, so it finds a template
// through the pod or the Service of the same name. The Service matters
// when an eviction deleted the pod before a release.
func (t *tree) selectedObjects(namespace string, selected func(labels map[string]string) bool) []selectedObject {
	var out []selectedObject
	claimed := map[string]bool{}
	claim := func(name string) {
		if !claimed[name] {
			claimed[name] = true
			out = append(out, selectedObject{"ResourceClaimTemplate " + name, templatePath(namespace, name)})
		}
	}
	for _, name := range t.podNames(selected) {
		if t.pods[name].Metadata.DeletionTimestamp != nil {
			continue
		}
		out = append(out, selectedObject{"pod " + name, podPath(namespace, name)})
		claim(name)
	}
	for _, name := range sortedKeys(t.services) {
		l := t.services[name].Metadata.Labels
		if !selected(l) {
			continue
		}
		if l[labelRole] == roleDevice {
			claim(name)
		}
		out = append(out, selectedObject{"Service " + name, servicePath(namespace, name)})
	}
	for _, name := range sortedKeys(t.configMaps) {
		if selected(t.configMaps[name].Metadata.Labels) {
			out = append(out, selectedObject{"ConfigMap " + name, configMapPath(namespace, name)})
		}
	}
	return out
}

// stopServer deletes the pods and other objects of a server's devices,
// then the server's pod, and waits until every pod is gone. It returns
// the names of the pods that it stopped. Every caller disconnects the
// server's devices first.
//
// The device pods go first because a device pod runs socat with the
// restart policy Always, and socat exits when the shim's connection
// ends. If the server stopped first, the kubelet would restart socat in
// a device pod that is not yet terminating. The kubelet restarts no
// container in a terminating pod. When a device pod stops first,
// indiserver restarts its shim, the shim finds no ready endpoint behind
// the device's Service, and it ends with indiserver, so no driver
// starts.
func (o *operator) stopServer(ctx context.Context, report func(string), ref serverRef) ([]string, error) {
	devices := withoutServers(func(l map[string]string) bool { return l[labelServer] == ref.String() })
	t := o.snapshot()
	first := t.podNames(devices)
	if err := o.deleteSelected(ctx, report, t, devices); err != nil {
		return first, err
	}
	rest, err := o.stopPods(ctx, report, onServer(ref, func(string) bool { return true }))
	stopped := append(first, rest...)
	sort.Strings(stopped)
	return slices.Compact(stopped), err
}

// withoutServers narrows a selection to the objects of the devices and
// the guider, which stopServer and the supervisor's sweep delete before
// the server's pod.
func withoutServers(selected func(labels map[string]string) bool) func(map[string]string) bool {
	return func(l map[string]string) bool { return selected(l) && l[labelRole] != roleServer }
}

// onServer selects the objects of one server whose kind label keep
// accepts.
func onServer(ref serverRef, keep func(kind string) bool) func(map[string]string) bool {
	return func(l map[string]string) bool {
		return l[labelServer] == ref.String() && keep(l[labelKind])
	}
}

func names(devices []*device) string {
	var out []string
	for _, d := range devices {
		out = append(out, d.kind.Name+" "+d.name())
	}
	return strings.Join(out, ", ")
}

// partition splits devices into switches, whose INDI device serves a
// Switch, and others. That includes the owner of a served Switch, such
// as a Focuser with a power box, and every other device that owner
// serves, because they are all one INDI device.
func (t *tree) partition(devices []*device) (switches, others []*device) {
	for _, d := range devices {
		owner, _ := t.ownerOf(d)
		if t.powers(owner) {
			switches = append(switches, d)
		} else {
			others = append(others, d)
		}
	}
	return switches, others
}

// powers reports whether the INDI device of an owner serves a Switch.
func (t *tree) powers(owner *device) bool {
	if owner == nil {
		return false
	}
	if owner.kind == observatory.SwitchKind {
		return true
	}
	for _, d := range t.servedBy(owner) {
		if d.kind == observatory.SwitchKind {
			return true
		}
	}
	return false
}
