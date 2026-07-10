package main

// The feature janitor deletes the workloads of retracted features.
//
// A feature's workload manifests ride the image and are seeded into
// k3s's auto-deploy directory by init only while the cluster document
// declares the feature (init/features.go). Retraction, though, has a
// gap k3s cannot close by itself: k3s deletes an addon's resources
// when it sees the manifest file removed while it is running, but a
// retraction removes the file at boot, before k3s starts, so k3s
// never witnesses a deletion. The retracted feature is disarmed (init
// stops writing its boot files, so the workload cannot function), but
// its objects would survive in the cluster with their pods failing.
//
// This janitor closes the gap declaratively. Every feature-seeded
// workload carries a liken.sh/feature annotation naming the feature
// it belongs to, and each sweep deletes any liken-system workload
// whose annotation names a feature the cluster document no longer
// declares. Declared is the only question: the janitor doesn't wait
// for the retraction to roll through the fleet, because k3s's own
// live behavior (file removed while running) deletes immediately too,
// and a workload whose feature the document disowns has no reason to
// keep running anywhere. Objects without the annotation are not the
// janitor's to judge — the operator and log-relay DaemonSets live in
// the same namespace, and no feature owns them.

import (
	"fmt"
	"net/http"

	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// featureAnnotation names the feature a workload belongs to, the
// janitor's whole contract with the manifests: a feature's manifests
// carry it (open-iscsi/manifests/iscsid.yaml), everything else omits
// it.
const featureAnnotation = "liken.sh/feature"

// featureWorkloadKinds are the workload kinds the janitor sweeps,
// each a list endpoint in liken-system. Features seed DaemonSets
// today; a feature that ships its first Deployment or Service adds
// that kind here, in the same change.
var featureWorkloadKinds = []struct {
	kind     string
	listPath string
}{
	{"daemonset", "/apis/apps/v1/namespaces/liken-system/daemonsets"},
}

// featureWorkload is the little the janitor needs to know about an
// object: its name, and which feature (if any) claims it.
type featureWorkload struct {
	Metadata struct {
		Name        string            `json:"name"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
}

// decideRetractions is the pure half: which workloads belong to
// features the document no longer declares. Presence in spec.features
// is the same opt-in test init actuates by, so the two gates always
// agree.
func decideRetractions(features map[string]*machine.FeatureConfig, workloads []featureWorkload) []featureWorkload {
	var retracted []featureWorkload
	for _, w := range workloads {
		slug := w.Metadata.Annotations[featureAnnotation]
		if slug == "" {
			continue
		}
		if _, declared := features[slug]; !declared {
			retracted = append(retracted, w)
		}
	}
	return retracted
}

// janitorFeatureWorkloads is the acting half, run once per sweep:
// list each swept kind, decide, delete. Deletion is by name with
// background propagation, so the workload's pods go with it.
func janitorFeatureWorkloads(c *kubernetes.Client, cluster *machine.Cluster) {
	for _, k := range featureWorkloadKinds {
		workloads, err := kubernetes.List[featureWorkload](c, k.listPath)
		if err != nil {
			fmt.Printf("listing %ss for the feature janitor: %v\n", k.kind, err)
			continue
		}
		for _, w := range decideRetractions(cluster.Spec.Features, workloads) {
			name := w.Metadata.Name
			slug := w.Metadata.Annotations[featureAnnotation]
			path := k.listPath + "/" + name + "?propagationPolicy=Background"
			if err := c.RequestJSON(http.MethodDelete, path, nil, nil); err != nil {
				fmt.Printf("deleting %s %s for the retracted %s feature: %v\n", k.kind, name, slug, err)
			} else {
				fmt.Printf("the cluster no longer declares the %s feature; deleted %s %s\n", slug, k.kind, name)
			}
		}
	}
}
