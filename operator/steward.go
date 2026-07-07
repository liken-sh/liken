package main

// The pod steward keeps the OS's own pods in step with the operating
// systems under them.
//
// Two container images are part of the OS: the operator's and the
// log relays'. image/build.sh bakes both into the initramfs, k3s
// imports them into containerd at boot, and imagePullPolicy: Never
// means a node can only ever run the builds its own OS carries (or
// ones left in containerd's persistent store by an OS it ran
// before). That breaks the usual Kubernetes assumption that any node
// can pull any image, and every OS DaemonSet is arranged around it:
//
//   - Each template pins a *stable* tag (liken.sh/operator:installed,
//     liken.sh/logs:installed) that every release tags its own build
//     with, so the same pod spec resolves to each node's own baked
//     image, and applying a new release's manifests doesn't change
//     the image field at all.
//   - updateStrategy: OnDelete, so applying manifests never deletes
//     a running pod. A rolling update would recreate pods on nodes
//     whose OS doesn't carry the new image yet; for the operator that
//     would kill the very pod each machine needs to drive its own
//     upgrade and leave it unable to ever take one.
//
// What's left is freshness: after a machine reboots into a new
// release, its existing pod objects predate the new manifests, and
// the sweep leader, right here, deletes them so each DaemonSet can
// recreate its pod from the current template. Every stewarded
// DaemonSet carries a liken.sh/os-version annotation naming the
// release that shipped it, stamped onto its pods through the
// template; the steward refreshes a pod exactly when its machine
// reports that version in its facts but the pod predates it. Both
// halves of that condition matter. A machine still on the old OS
// keeps its old pod, because evicting it would leave the machine
// without that pod's function at all. A machine ahead of the applied
// manifests keeps its old pod too, because a refresh would recreate
// another stale one, thrashing every sweep until a new-release
// leader applies the manifests that can actually satisfy it.

import (
	"fmt"
	"net/http"

	"github.com/chrisguidry/liken/machine"
)

// osVersionAnnotation names the liken release a manifest (and the
// pods created from it) shipped with. image/build.sh substitutes the
// real version into each DaemonSet at bake time.
const osVersionAnnotation = "liken.sh/os-version"

// stewardedDaemonSets are the OS's own DaemonSets, the ones whose
// images are baked into the initramfs and whose pods therefore need
// the steward's refresh after an upgrade. Each is expected to label
// its pods app: <name>, the selector its manifest declares.
var stewardedDaemonSets = []string{
	"liken-operator",
	"kernel-logs",
	"liken-logs",
	"k3s-logs",
	"containerd-logs",
}

func daemonSetPath(name string) string {
	return "/apis/apps/v1/namespaces/liken-system/daemonsets/" + name
}

func daemonSetPodsPath(name string) string {
	return "/api/v1/namespaces/liken-system/pods?labelSelector=app%3D" + name
}

// decideRefresh is the steward's whole judgment, pure over the
// sweep's inputs: which of one DaemonSet's pods to evict so it
// recreates them from the current template. dsVersion is the
// os-version annotation on the DaemonSet itself, naming the release
// whose manifests are actually applied; "" (no annotation, or no
// DaemonSet) means there is no authority to refresh toward.
func decideRefresh(dsVersion string, machines []machine.Machine, pods []podObject) []podObject {
	if dsVersion == "" {
		return nil
	}
	running := make(map[string]string, len(machines))
	for i := range machines {
		running[machines[i].Metadata.Name] = machines[i].Status.Version.Liken
	}
	var refresh []podObject
	for _, p := range pods {
		osVersion, known := running[p.Spec.NodeName]
		if !known || osVersion != dsVersion {
			continue // the machine isn't (yet) running what the manifests shipped
		}
		if p.Metadata.Annotations[osVersionAnnotation] == dsVersion {
			continue // the pod is already from this release's template
		}
		refresh = append(refresh, p)
	}
	return refresh
}

// stewardOSPods is the acting half, run by the sweep leader over
// every stewarded DaemonSet in turn.
func stewardOSPods(c *apiClient, machines []machine.Machine) {
	for _, name := range stewardedDaemonSets {
		stewardDaemonSet(c, machines, name)
	}
}

// stewardDaemonSet reads one DaemonSet's shipped version, lists its
// pods, and evicts the stale ones. Eviction rather than delete,
// deliberately: it is the verb the operator already holds for
// drains, and the DaemonSet recreates the pod either way. The
// eviction may take the sweep leader's own operator pod (an upgraded
// leader's old operator is exactly a stale pod); the lease passes
// and the recreated pod picks the sweep back up. For a relay pod the
// eviction also discards its emptyDir resume cursor, so each OS
// upgrade re-ships the tail of that machine's streams once, with the
// envelopes' seq field there to deduplicate.
func stewardDaemonSet(c *apiClient, machines []machine.Machine, name string) {
	var ds struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := c.requestJSON(http.MethodGet, daemonSetPath(name), nil, &ds); err != nil {
		return // no DaemonSet to steward; nothing to do
	}
	dsVersion := ds.Metadata.Annotations[osVersionAnnotation]
	var pods struct {
		Items []podObject `json:"items"`
	}
	if err := c.requestJSON(http.MethodGet, daemonSetPodsPath(name), nil, &pods); err != nil {
		fmt.Printf("listing %s pods for the steward: %v\n", name, err)
		return
	}
	for _, p := range decideRefresh(dsVersion, machines, pods.Items) {
		if err := evictPod(c, p); err != nil {
			fmt.Printf("refreshing pod %s: %v\n", p.Metadata.Name, err)
		} else {
			fmt.Printf("pod %s on %s predates release %s; evicted for the DaemonSet to recreate\n",
				p.Metadata.Name, p.Spec.NodeName, dsVersion)
		}
	}
}
