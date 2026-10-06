package main

// The Kubernetes side of the steps: the pods and Services that a step
// creates, the waits for them to be Ready, the wait for each driver to
// define its device on its server, and the deletes at the end.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// ensure creates a pod and its Service and claim, or replaces a
// running pod whose spec differs from the one built now. A pod that
// stops must be gone before the new one of the same name can be
// created. A write that the API server refuses is sent again (send),
// and the step's deadline bounds the tries. created reports whether
// the API server created the pod, so a Ready reservation's runner can
// record an Event for each pod that it creates.
func (o *operator) ensure(ctx context.Context, report func(string), built *pod, svc *service, claim *resourceClaim) (created bool, err error) {
	name := built.Metadata.Name
	err = o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		if claim != nil {
			// A claim's spec cannot change, and one that exists is the
			// one the operator created. The create answers 409 then.
			if err := o.send(ctx, report, fmt.Sprintf("creating ResourceClaim %s", name), func() error {
				return o.create("/apis/resource.k8s.io/v1/namespaces/"+o.namespace+"/resourceclaims", claim)
			}); err != nil {
				return false, "", err
			}
		}
		// A Service's spec follows from its name alone, so a Service
		// that exists is current.
		if _, ok := t.services[svc.Metadata.Name]; !ok {
			if err := o.send(ctx, report, fmt.Sprintf("creating Service %s", name), func() error { return o.create("/api/v1/namespaces/"+o.namespace+"/services", svc) }); err != nil {
				return false, "", err
			}
		}
		running, ok := t.pods[name]
		switch {
		case !ok:
			if err := o.send(ctx, report, fmt.Sprintf("creating pod %s", name), func() (err error) {
				created, err = o.post("/api/v1/namespaces/"+o.namespace+"/pods", built)
				return err
			}); err != nil {
				return false, "", err
			}
			return true, "", nil
		case running.Metadata.DeletionTimestamp != nil:
			return false, "waiting for the earlier pod " + name + " to stop", nil
		case !current(running, built):
			if err := o.send(ctx, report, fmt.Sprintf("replacing pod %s: its spec changed", name), func() error { return o.deleteObject(podPath(o.namespace, name)) }); err != nil {
				return false, "", err
			}
			return false, "replacing the pod " + name + ", whose spec changed", nil
		}
		return true, "", nil
	})
	return created, err
}

// startServer creates the pod and the Service of one INDI server, with
// a link for each of the devices. created reports whether the API
// server created the pod.
func (o *operator) startServer(ctx context.Context, report func(string), ref serverRef, ownerUID string, devices []*device) (created bool, err error) {
	built, svc, err := serverPod(o.namespace, ref, ownerUID, devices)
	if err != nil {
		return false, err
	}
	return o.ensure(ctx, report, built, svc, nil)
}

// startDevices creates the pod, the Service, and the claim of each
// device, and answers the devices whose pod it created. The tree of
// now decides which camera a Guider names, so a Guider created during
// a reservation places its camera at the next start of the camera's
// pod.
func (o *operator) startDevices(ctx context.Context, report func(string), ref serverRef, devices []*device) ([]*device, error) {
	t := o.snapshot()
	var started []*device
	for _, d := range devices {
		built, svc, claim, err := devicePod(o.namespace, ref, d, t.guides(ref, d))
		if err != nil {
			o.fault(d, err)
			return started, fmt.Errorf("%s %s: %w", d.kind.Name, d.name(), err)
		}
		created, err := o.ensure(ctx, report, built, svc, claim)
		if created {
			started = append(started, d)
		}
		if err != nil {
			return started, err
		}
	}
	return started, nil
}

// waitReady waits until the pod of each device is Ready. For real
// hardware, a pod with a claim stays Pending until its device appears,
// so this is where activation waits for a person or a Switch to power
// the device on.
func (o *operator) waitReady(ctx context.Context, report func(string), devices []*device) error {
	return o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		for _, d := range devices {
			name, err := objectName(d.kind, d.name())
			if err != nil {
				return false, "", err
			}
			p, ok := t.pods[name]
			if !ok || !p.ready() {
				phase := "not created"
				if ok {
					phase = firstNonEmpty(p.Status.Phase, "Pending")
				}
				return false, fmt.Sprintf("waiting for pod %s of %s %s (%s)", name, d.kind.Name, d.name(), phase), nil
			}
		}
		return true, "", nil
	})
}

// waitServer waits until the operator's INDI connection to a server is
// open, and answers the server.
func (o *operator) waitServer(ctx context.Context, report func(string), ref serverRef) (*indiServer, error) {
	var server *indiServer
	err := o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		s, ok := o.servers.get(ref.String())
		if !ok || !s.client.Connected() {
			return false, "waiting for the INDI connection to " + ref.String(), nil
		}
		server = s
		return true, "", nil
	})
	return server, err
}

// waitDefined waits until the driver of each device defines its device
// on the server, and answers a handle for each, by the device's key.
func (o *operator) waitDefined(ctx context.Context, report func(string), ref serverRef, devices []*device) (map[string]handle, error) {
	server, err := o.waitServer(ctx, report, ref)
	if err != nil {
		return nil, err
	}
	handles := map[string]handle{}
	err = o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		onServer := t.devicesOn(ref)
		waiting := ""
		for _, d := range devices {
			if _, found := handles[d.key()]; found {
				continue
			}
			name, err := indiName(server.client, onServer, d)
			if err != nil {
				o.fault(d, err)
				return false, "", err
			}
			if name == "" {
				waiting = firstNonEmpty(waiting, fmt.Sprintf("waiting for driver %s of %s %s on %s", d.object.Spec.Driver.Name, d.kind.Name, d.name(), ref))
				continue
			}
			handles[d.key()] = handle{d: d, server: server, name: name}
		}
		return waiting == "", waiting, nil
	})
	return handles, err
}

// handlesOf answers a handle for each device whose driver has defined
// its device on the server now, with no wait. A step that ends a
// reservation acts on the devices that are there.
func (o *operator) handlesOf(t *tree, ref serverRef, devices []*device) []handle {
	server, ok := o.servers.get(ref.String())
	if !ok || !server.client.Connected() {
		return nil
	}
	var out []handle
	onServer := t.devicesOn(ref)
	for _, d := range devices {
		if name, err := indiName(server.client, onServer, d); err == nil && name != "" {
			out = append(out, handle{d: d, server: server, name: name})
		}
	}
	return out
}

// stopPods deletes the pods, the Services, the ConfigMaps, and the
// claims that select returns true for, and waits until the pods are gone. A delete that
// the API server refuses is sent again (send).
func (o *operator) stopPods(ctx context.Context, report func(string), selected func(labels map[string]string) bool) ([]string, error) {
	var stopped []string
	for name, p := range o.snapshot().pods {
		if selected(p.Metadata.Labels) {
			stopped = append(stopped, name)
		}
	}
	sort.Strings(stopped)
	err := o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		var left []string
		for name, p := range t.pods {
			if !selected(p.Metadata.Labels) {
				continue
			}
			left = append(left, name)
			if p.Metadata.DeletionTimestamp != nil {
				continue
			}
			if err := o.send(ctx, report, fmt.Sprintf("deleting pod %s", name), func() error { return o.deleteObject(podPath(o.namespace, name)) }); err != nil {
				return false, "", err
			}
			if err := o.send(ctx, report, fmt.Sprintf("deleting ResourceClaim %s", name), func() error { return o.deleteObject(claimPath(o.namespace, name)) }); err != nil {
				return false, "", err
			}
		}
		for name, svc := range t.services {
			if selected(svc.Metadata.Labels) {
				if err := o.send(ctx, report, fmt.Sprintf("deleting Service %s", name), func() error { return o.deleteObject(servicePath(o.namespace, name)) }); err != nil {
					return false, "", err
				}
			}
		}
		for name, files := range t.configMaps {
			if selected(files.Metadata.Labels) {
				if err := o.send(ctx, report, fmt.Sprintf("deleting ConfigMap %s", name), func() error { return o.deleteObject(configMapPath(o.namespace, name)) }); err != nil {
					return false, "", err
				}
			}
		}
		sort.Strings(left)
		return len(left) == 0, "stopping pods " + strings.Join(left, ", "), nil
	})
	return stopped, err
}

// onServer selects the objects of one server whose kind passes keep.
func onServer(ref serverRef, keep func(kind string) bool) func(map[string]string) bool {
	return func(l map[string]string) bool {
		return l[labelServer] == ref.String() && keep(l[labelKind])
	}
}

func isSwitch(kind string) bool { return kind == observatory.SwitchKind.Name }

func names(devices []*device) string {
	var out []string
	for _, d := range devices {
		out = append(out, d.kind.Name+" "+d.name())
	}
	return strings.Join(out, ", ")
}

// partition splits devices into the Switch devices and the others.
func partition(devices []*device) (switches, others []*device) {
	for _, d := range devices {
		if d.kind == observatory.SwitchKind {
			switches = append(switches, d)
		} else {
			others = append(others, d)
		}
	}
	return switches, others
}
