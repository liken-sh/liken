package main

// The agent publishes one media.liken.sh device for each render node
// that `liken` publishes on this node. It queries each render node's
// media driver the first time it sees the render node and caches the
// report for the life of the pod. The driver is part of the image, so
// it cannot change while the pod runs, and no timer queries again.
//
// The agent opens a render node through its own claim. The scheduler
// allocates every render node that exists when it places the pod, and
// the allocation does not change afterward. When `liken` publishes a
// render node that the claim does not hold, the container has no
// device file for it, so the agent deletes its own pod, and the
// DaemonSet creates a pod whose claim holds every render node.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// claimName is the name of the claim in the pod spec of
// deploy/capabilities.yaml.
const claimName = "render-nodes"

type agent struct {
	client    *apiclient.Client
	node      string
	owner     OwnerReference
	pod       string
	namespace string

	// held is the set of `liken.sh` devices that the pod's claim
	// allocated.
	held map[string]bool
	// reports caches each render node's report by device name. A
	// failed query is cached as an empty report, so it is not retried.
	reports map[string]report
	// replacing is true once the agent has deleted its own pod.
	replacing bool

	query      func(ctx context.Context, path string) (report, error)
	renderNode func(address string) (string, error)
}

// pass publishes a device for each render node in this node's
// `liken.sh` slice, which is nil when `liken` publishes no slice here.
// When the slice has a render node that the claim does not hold, pass
// deletes the pod and publishes nothing.
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
		facts, queried := a.reports[gpu.Name]
		if !queried {
			facts = a.queryGPU(ctx, gpu)
			a.reports[gpu.Name] = facts
		}
		devices = append(devices, mediaDevice(gpu, facts))
	}
	return ensureResourceSlice(a.client, a.node, a.owner, devices)
}

// queryGPU queries the media driver of one GPU's render node and logs
// the result. When the query fails, it logs the error and returns an
// empty report, so every capability of the GPU publishes as false.
func (a *agent) queryGPU(ctx context.Context, gpu SliceDevice) report {
	path, err := a.renderNode(gpu.stringAttribute("address"))
	if err == nil {
		var facts report
		facts, err = a.query(ctx, path)
		if err == nil {
			fmt.Fprintf(os.Stderr, "capabilities: %s (%s, %s): %s\n", gpu.Name, path, facts.Vendor, capabilityList(facts))
			return facts
		}
	}
	fmt.Fprintf(os.Stderr, "capabilities: querying GPU %s: %v. Every capability of this GPU publishes as false\n", gpu.Name, err)
	return report{}
}

// capabilityList formats a report's capabilities for the log in the
// order of capabilityRules, such as "decodeH264=true decodeHEVCMain=true ...".
func capabilityList(facts report) string {
	values := capabilitiesOf(facts)
	var out []string
	for _, rule := range capabilityRules {
		out = append(out, fmt.Sprintf("%s=%t", rule.name, values[rule.name]))
	}
	return strings.Join(out, " ")
}

// replace deletes the agent's own pod once. A pod that is already gone
// counts as deleted, and any other failure is returned, so the next
// pass tries again.
func (a *agent) replace(unheld []string) error {
	if a.replacing {
		return nil
	}
	fmt.Fprintf(os.Stderr, "capabilities: the node has the GPUs %s, which the claim of this pod does not hold."+
		" Deleting pod %s/%s so the DaemonSet creates a pod whose claim holds every render node\n",
		strings.Join(unheld, ", "), a.namespace, a.pod)
	err := a.client.RequestJSON(http.MethodDelete, "/api/v1/namespaces/"+a.namespace+"/pods/"+a.pod, nil, nil)
	if err != nil && err != apiclient.ErrNotFound {
		return fmt.Errorf("deleting pod %s/%s: %w", a.namespace, a.pod, err)
	}
	a.replacing = true
	return nil
}

// renderNodes returns the devices of a `liken.sh` slice that have a
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

// podObject is the part of a pod that maps each claim in its spec to
// the ResourceClaim created for it.
type podObject struct {
	Status struct {
		ResourceClaimStatuses []struct {
			Name              string `json:"name"`
			ResourceClaimName string `json:"resourceClaimName"`
		} `json:"resourceClaimStatuses"`
	} `json:"status"`
}

// claimObject is the part of a ResourceClaim that lists its allocated
// devices.
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

// heldDevices returns the `liken.sh` devices allocated to the pod's
// claim. status.resourceClaimStatuses of the pod names the
// ResourceClaim created from the template. The kubelet starts a
// container only after that claim is allocated, so a missing status or
// allocation is an error.
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
		return nil, fmt.Errorf("pod %s/%s has no status.resourceClaimStatuses entry named %s", namespace, pod, claimName)
	}
	claim, err := apiclient.Get[claimObject](c, "/apis/resource.k8s.io/v1/namespaces/"+namespace+"/resourceclaims/"+name)
	if err != nil {
		return nil, fmt.Errorf("reading claim %s/%s: %w", namespace, name, err)
	}
	if claim.Status.Allocation == nil {
		return nil, fmt.Errorf("claim %s/%s has no status.allocation", namespace, name)
	}
	out := map[string]bool{}
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Driver == likenDriver {
			out[result.Device] = true
		}
	}
	return out, nil
}
