package main

// The pod steward: keeping the operator's own pods in step with the
// operating systems under them.
//
// The operator's container image is part of the OS: image/build.sh
// bakes it into the initramfs, k3s imports it into containerd at
// boot, and imagePullPolicy: Never means a node can only ever run
// the operator its own OS carries (or one left in containerd's
// persistent store by an OS it ran before). That breaks the usual
// Kubernetes assumption that any node can pull any image, and the
// DaemonSet has to be arranged around it:
//
//   - The template pins a *stable* tag (liken.sh/operator:installed)
//     that every release tags its own build with, so the same pod
//     spec resolves to each node's own baked operator, and applying
//     a new release's manifests doesn't change the image field at
//     all.
//   - updateStrategy: OnDelete, so applying manifests never deletes
//     a running pod. A rolling update would recreate pods on nodes
//     whose OS doesn't carry the new image yet — killing the very
//     operator that machine needs to drive its own upgrade, and
//     leaving it unable to ever take one (a deadlock this fleet has
//     lived through).
//
// What's left is freshness: after a machine reboots into a new
// release, its existing pod object predates the new manifests, and
// somebody has to delete it so the DaemonSet can recreate it from
// the current template. That somebody is the sweep leader, right
// here. The DaemonSet carries a liken.sh/os-version annotation
// naming the release that shipped it, stamped onto its pods through
// the template; the steward refreshes a pod exactly when its machine
// reports that version in its facts but the pod predates it. Both
// halves of that condition are load-bearing: a machine still on the
// old OS keeps its old pod (evicting it would orphan the machine),
// and a machine *ahead* of the applied manifests keeps its old pod
// too (a refresh would recreate another stale one, thrashing every
// sweep until a new-release leader applies the manifests that can
// actually satisfy it).

import (
	"fmt"
	"net/http"

	"github.com/chrisguidry/liken/machine"
)

// osVersionAnnotation names the liken release a manifest (and the
// pods created from it) shipped with. image/build.sh substitutes the
// real version into the DaemonSet at bake time.
const osVersionAnnotation = "liken.sh/os-version"

const operatorDaemonSetPath = "/apis/apps/v1/namespaces/liken-system/daemonsets/liken-operator"
const operatorPodsPath = "/api/v1/namespaces/liken-system/pods?labelSelector=app%3Dliken-operator"

// decideOperatorRefresh is the steward's whole judgment, pure over
// the sweep's inputs: which operator pods to evict so the DaemonSet
// recreates them from the current template. dsVersion is the
// os-version annotation on the DaemonSet itself — the release whose
// manifests are actually applied — and "" (no annotation, or no
// DaemonSet) means there is no authority to refresh toward.
func decideOperatorRefresh(dsVersion string, machines []machine.Machine, pods []podObject) []podObject {
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

// stewardOperatorPods is the acting half, run by the sweep leader:
// read the DaemonSet's shipped version, list its pods, and evict the
// stale ones. Eviction rather than delete on purpose — it's the verb
// the operator already holds for drains, and the DaemonSet recreates
// the pod either way. The eviction may take the sweep leader's own
// pod (an upgraded leader's old operator is exactly a stale pod);
// the lease passes and the recreated pod picks the sweep back up.
func stewardOperatorPods(c *apiClient, machines []machine.Machine) {
	var ds struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := c.requestJSON(http.MethodGet, operatorDaemonSetPath, nil, &ds); err != nil {
		return // no DaemonSet to steward; nothing to do
	}
	dsVersion := ds.Metadata.Annotations[osVersionAnnotation]
	var pods struct {
		Items []podObject `json:"items"`
	}
	if err := c.requestJSON(http.MethodGet, operatorPodsPath, nil, &pods); err != nil {
		fmt.Printf("listing operator pods for the steward: %v\n", err)
		return
	}
	for _, p := range decideOperatorRefresh(dsVersion, machines, pods.Items) {
		if err := evictPod(c, p); err != nil {
			fmt.Printf("refreshing operator pod %s: %v\n", p.Metadata.Name, err)
		} else {
			fmt.Printf("operator pod %s on %s predates release %s; evicted for the DaemonSet to recreate\n",
				p.Metadata.Name, p.Spec.NodeName, dsVersion)
		}
	}
}
