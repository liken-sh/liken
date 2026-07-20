package main

// The feature janitor deletes the workloads that belong to retracted
// features.
//
// A feature's workload manifests ride in the image. init seeds them
// into k3s's auto-deploy directory only while the cluster document
// declares the feature (see init/features.go). Retraction, though,
// leaves a gap that k3s cannot close by itself. k3s deletes an
// addon's resources when it detects that the manifest file was
// removed while k3s is running. But retraction removes the file at
// boot, before k3s starts, so k3s never detects a deletion. The
// retracted feature stops functioning, because init stops writing
// its boot files and the workload cannot run. But the feature's
// objects would stay in the cluster, with their pods failing.
//
// This janitor closes the gap declaratively. Every feature-seeded
// workload carries a liken.sh/feature annotation that names the
// feature it belongs to. Each sweep deletes any liken-system workload
// whose annotation names a feature that the cluster document no
// longer declares. Whether the feature is declared is the only
// question the janitor asks. The janitor does not wait for the
// retraction to roll through the fleet, because k3s's own live
// behavior, deleting when the manifest file disappears while k3s
// runs, also deletes immediately. Once the document no longer claims
// a workload's feature, the fleet does not need that workload to keep
// running anywhere. The janitor does not act on objects that carry no
// annotation. The operator and log-relay DaemonSets live in the same
// namespace, and no feature owns them.

import (
	"fmt"
	"net/http"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
)

// featureAnnotation names the feature a workload belongs to. It is
// the janitor's whole contract with the manifests: a feature's
// manifests carry this annotation (for example,
// open-iscsi/manifests/iscsid.yaml), and everything else omits it.
const featureAnnotation = "liken.sh/feature"

// featureWorkloadKinds lists the workload kinds the janitor sweeps.
// Each kind is a list endpoint in liken-system. Features seed
// DaemonSets today. When a feature ships its first Deployment or
// Service, add that kind here, in the same change.
var featureWorkloadKinds = []struct {
	kind     string
	listPath string
}{
	{"daemonset", "/apis/apps/v1/namespaces/liken-system/daemonsets"},
}

// featureWorkload holds the small amount of information the janitor
// needs about an object: its name, and which feature, if any, claims
// it.
type featureWorkload struct {
	Metadata struct {
		Name        string            `json:"name"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
}

// decideRetractions computes which workloads belong to features that
// the document no longer declares. Presence in spec.features is the
// same opt-in test that init uses, so the two gates always agree.
func decideRetractions(features map[string]*cluster.FeatureConfig, workloads []featureWorkload) []featureWorkload {
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

// janitorFeatureWorkloads carries out the janitor's work, once per
// sweep: it lists each swept kind, decides which workloads to
// retract, and deletes them. Deletion is by name with background
// propagation, so the workload's pods are deleted along with it.
func janitorFeatureWorkloads(c *kubernetes.Client, clusterDoc *cluster.Cluster) {
	for _, k := range featureWorkloadKinds {
		workloads, err := kubernetes.List[featureWorkload](c, k.listPath)
		if err != nil {
			fmt.Printf("listing %ss for the feature janitor: %v\n", k.kind, err)
			continue
		}
		for _, w := range decideRetractions(clusterDoc.Spec.Features, workloads) {
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
