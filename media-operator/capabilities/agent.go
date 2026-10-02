package main

// The agent's pass: for each render node that `liken` publishes on this
// node, query its media driver once, and publish one media.liken.sh
// device with what the driver states.
//
// The agent measures when its pod starts and when a render node
// appears. A new pod covers a new image, and so a new driver. A GPU's
// media driver does not change while the pod runs, so the agent holds
// each report for the life of the pod and no timer queries again.
//
// The agent reaches each render node through its own claim, which
// allocates every render node of the node at the moment the scheduler
// places the pod. A render node that `liken` publishes after that
// moment is not in the claim, and this container has no device node
// for it. The allocation of a pod's claim does not change while the pod
// exists, so the agent deletes its own pod, and the DaemonSet makes a
// new pod whose claim holds every render node.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// claimName is the name of the agent's claim in its pod spec, which
// deploy/capabilities.yaml states.
const claimName = "render-nodes"

// agent holds what the passes share.
type agent struct {
	client    *apiclient.Client
	node      string
	owner     OwnerReference
	pod       string
	namespace string

	// Held names the `liken.sh` devices that the pod's claim
	// allocated. It does not change for the life of the pod.
	held map[string]bool
	// Reports holds each render node's report by device name, and an
	// empty report for a query that failed.
	reports map[string]report
	// Replacing is set once the agent has deleted its own pod.
	replacing bool

	query      func(ctx context.Context, path string) (report, error)
	renderNode func(address string) (string, error)
}

// pass publishes the devices for the render nodes in the `liken.sh`
// slice of this node. A nil slice means `liken` publishes nothing here.
func (a *agent) pass(ctx context.Context, liken *ResourceSlice) error {
	gpus := renderNodes(liken)
	var unheld []string
	for _, gpu := range gpus {
		if !a.held[gpu.Name] {
			unheld = append(unheld, gpu.Name)
		}
	}
	if len(unheld) > 0 {
		return a.replace(unheld)
	}
	var devices []SliceDevice
	for _, gpu := range gpus {
		facts, measured := a.reports[gpu.Name]
		if !measured {
			facts = a.measure(ctx, gpu)
			a.reports[gpu.Name] = facts
		}
		devices = append(devices, mediaDevice(gpu, facts))
	}
	return ensureResourceSlice(a.client, a.node, a.owner, devices)
}

// measure queries the driver of one GPU's render node, and writes one
// line that states the result. A query that fails gives an empty
// report, so every capability of the GPU publishes as false, and the
// line holds the failure word for word.
func (a *agent) measure(ctx context.Context, gpu SliceDevice) report {
	path, err := a.renderNode(gpu.stringAttribute("address"))
	if err == nil {
		var facts report
		facts, err = a.query(ctx, path)
		if err == nil {
			fmt.Fprintf(os.Stderr, "capabilities: %s (%s, %s): %s\n", gpu.Name, path, facts.Vendor, stated(facts))
			return facts
		}
	}
	fmt.Fprintf(os.Stderr, "capabilities: %s: %v; every capability of this GPU publishes as false\n", gpu.Name, err)
	return report{}
}

// stated lists the capabilities of a report in the order of the rules,
// such as "decodeH264=true decodeHEVCMain=true ...".
func stated(facts report) string {
	values := capabilitiesOf(facts)
	var out []string
	for _, rule := range capabilityRules {
		out = append(out, fmt.Sprintf("%s=%t", rule.name, values[rule.name]))
	}
	return strings.Join(out, " ")
}

// replace deletes the agent's own pod, once, so the DaemonSet makes a
// pod whose claim holds the render nodes this one's does not.
func (a *agent) replace(unheld []string) error {
	if a.replacing {
		return nil
	}
	fmt.Fprintf(os.Stderr, "capabilities: liken publishes %s, which this pod's claim does not hold;"+
		" deleting pod %s/%s so the DaemonSet makes one whose claim holds every render node\n",
		strings.Join(unheld, ", "), a.namespace, a.pod)
	err := a.client.RequestJSON(http.MethodDelete, "/api/v1/namespaces/"+a.namespace+"/pods/"+a.pod, nil, nil)
	if err != nil && err != apiclient.ErrNotFound {
		return fmt.Errorf("deleting pod %s/%s: %w", a.namespace, a.pod, err)
	}
	a.replacing = true
	return nil
}

// renderNodes lists the devices of a `liken.sh` slice that deliver a
// render node, sorted by name, so the same hardware always publishes
// the same slice.
func renderNodes(liken *ResourceSlice) []SliceDevice {
	if liken == nil {
		return nil
	}
	var out []SliceDevice
	for _, device := range liken.Spec.Devices {
		if value := device.Attributes["renderNode"].Bool; value != nil && *value {
			out = append(out, device)
		}
	}
	slices.SortFunc(out, func(a, b SliceDevice) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// podObject holds the part of the agent's pod that names its claim.
type podObject struct {
	Status struct {
		ResourceClaimStatuses []struct {
			Name              string `json:"name"`
			ResourceClaimName string `json:"resourceClaimName"`
		} `json:"resourceClaimStatuses"`
	} `json:"status"`
}

// claimObject holds the part of a claim that names what it allocated.
type claimObject struct {
	Status struct {
		Allocation *struct {
			Devices struct {
				Results []struct {
					Driver string `json:"driver"`
					Device string `json:"device"`
				} `json:"results"`
			} `json:"devices"`
		} `json:"allocation"`
	} `json:"status"`
}

// heldDevices reads which `liken.sh` devices the pod's claim allocated.
// The pod's status names the claim that the kubelet made from the
// template, and the claim's allocation names the devices. The kubelet
// starts a container only after the claim is allocated, so both are
// there when the agent reads them.
func heldDevices(c *apiclient.Client, namespace, pod string) (map[string]bool, error) {
	held, err := apiclient.Get[podObject](c, "/api/v1/namespaces/"+namespace+"/pods/"+pod)
	if err != nil {
		return nil, fmt.Errorf("reading pod %s/%s: %w", namespace, pod, err)
	}
	name := ""
	for _, status := range held.Status.ResourceClaimStatuses {
		if status.Name == claimName {
			name = status.ResourceClaimName
		}
	}
	if name == "" {
		return nil, fmt.Errorf("pod %s/%s states no claim for %s", namespace, pod, claimName)
	}
	claim, err := apiclient.Get[claimObject](c, "/apis/resource.k8s.io/v1/namespaces/"+namespace+"/resourceclaims/"+name)
	if err != nil {
		return nil, fmt.Errorf("reading claim %s/%s: %w", namespace, name, err)
	}
	if claim.Status.Allocation == nil {
		return nil, fmt.Errorf("claim %s/%s has no allocation", namespace, name)
	}
	out := map[string]bool{}
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Driver == likenDriver {
			out[result.Device] = true
		}
	}
	return out, nil
}
