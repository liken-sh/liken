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
	"errors"
	"fmt"
	"net/http"
	"net/url"

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

// The flux janitor: the teardown half of the seed-once engine.
//
// The generic janitor above deletes annotated workloads, but the
// flux feature cannot use that shape, because its objects are not
// inert. Deleting the Kustomization while its controller still runs
// triggers the engine's own deletion finalizer, and with prune on,
// that finalizer garbage-collects everything the repository ever
// applied: the workloads, the Machine documents, and the Cluster
// document itself. So flux's retraction is ordered, and the order
// is the whole design: kill the controllers first, so the finalizer
// can never fire, then remove the finalizers by hand, and only then
// delete the objects.
//
// Each sweep advances one stage and returns, so the stages are
// separated by real observations, never by in-process waits:
//
//  1. Engine Deployments still exist: delete them.
//  2. Controller pods still exist: wait. A Deployment's deletion is
//     asynchronous, and a controller that is still terminating could
//     still process a finalizer.
//  3. Controllers provably gone: strip the sync objects' finalizers,
//     delete them, and delete the engine's cluster-scoped remains:
//     the CRDs, the engine's RBAC, the planter's grant, and the
//     namespace.
//
// What survives is deliberate: nothing. Retraction burns the deploy
// key with the namespace, and a re-enabled feature mints a fresh
// key to register. Keeping the key was considered and rejected: it
// would mean k3s's addon machinery must never touch the namespace,
// and the ground would outlive the feature as unowned state. Off
// means off. The repository's own workloads also survive, as
// orphans: stopping the sync must not undeploy what the sync
// deployed.
//
// The janitor's rights are standing, in the operator's own manifest,
// unlike the planter's, which arrive with the feature. This is
// deliberate too: the janitor's job begins exactly when the
// feature's delivered grants disappear, so delivered rights could
// never clean up after the feature that delivered them. The
// standing rights are deletes on exact names, powerless to create
// or read anything.

// fluxTeardownPaths are the delete targets of the final stage, in
// order. The sync objects come first, finalizers already stripped;
// the namespace's own deletion then sweeps everything namespaced
// that remains, the deploy key Secret included; the cluster-scoped
// remains close it out. The CRD and RBAC names must match the
// engine seed; the parity test in flux_test.go holds them to it.
var fluxTeardownPaths = []string{
	"/apis/kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system",
	"/apis/source.toolkit.fluxcd.io/v1/namespaces/flux-system/gitrepositories/flux-system",
	"/api/v1/namespaces/flux-system",
	"/apis/apiextensions.k8s.io/v1/customresourcedefinitions/buckets.source.toolkit.fluxcd.io",
	"/apis/apiextensions.k8s.io/v1/customresourcedefinitions/externalartifacts.source.toolkit.fluxcd.io",
	"/apis/apiextensions.k8s.io/v1/customresourcedefinitions/gitrepositories.source.toolkit.fluxcd.io",
	"/apis/apiextensions.k8s.io/v1/customresourcedefinitions/helmcharts.source.toolkit.fluxcd.io",
	"/apis/apiextensions.k8s.io/v1/customresourcedefinitions/helmrepositories.source.toolkit.fluxcd.io",
	"/apis/apiextensions.k8s.io/v1/customresourcedefinitions/ocirepositories.source.toolkit.fluxcd.io",
	"/apis/apiextensions.k8s.io/v1/customresourcedefinitions/kustomizations.kustomize.toolkit.fluxcd.io",
	"/apis/rbac.authorization.k8s.io/v1/clusterroles/crd-controller-flux-system",
	"/apis/rbac.authorization.k8s.io/v1/clusterroles/flux-edit-flux-system",
	"/apis/rbac.authorization.k8s.io/v1/clusterroles/flux-view-flux-system",
	"/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/cluster-reconciler-flux-system",
	"/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/crd-controller-flux-system",
	"/apis/rbac.authorization.k8s.io/v1/clusterroles/liken-engine-planter",
	"/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/liken-engine-planter",
}

// fluxControllerPodsPath lists the engine's controller pods, alive
// or terminating. The engine's own labels select them.
var fluxControllerPodsPath = "/api/v1/namespaces/flux-system/pods?labelSelector=" +
	url.QueryEscape("app in (source-controller,kustomize-controller)")

// janitorFlux tears the flux feature down when the cluster document
// no longer declares it. Every call is one stage at most; the sweep
// calls it again ten seconds later, and silence is the converged
// state.
func janitorFlux(c *kubernetes.Client, clusterDoc *cluster.Cluster) {
	if clusterDoc.FeatureEnabled(cluster.FeatureFlux) {
		return
	}

	// Stage 1: the controllers must die before anything else is
	// touched. A successful delete means this pass's work is done;
	// the pod check below needs a fresh observation anyway.
	deleted := false
	for _, name := range []string{"source-controller", "kustomize-controller"} {
		path := "/apis/apps/v1/namespaces/flux-system/deployments/" + name
		if err := c.RequestJSON(http.MethodGet, path, nil, nil); errors.Is(err, kubernetes.ErrNotFound) {
			continue
		}
		if err := c.RequestJSON(http.MethodDelete, path+"?propagationPolicy=Background", nil, nil); err == nil {
			fmt.Printf("the cluster no longer declares flux; deleted the %s Deployment\n", name)
			deleted = true
		}
	}
	if deleted {
		return
	}

	// Stage 2: wait out terminating controller pods. This gate is
	// what makes the finalizer unreachable: no controller process
	// exists past it.
	pods, err := kubernetes.List[featureWorkload](c, fluxControllerPodsPath)
	if err != nil || len(pods) > 0 {
		return
	}

	// Stage 3: nothing can react anymore. Strip the sync objects'
	// finalizers so their deletion, and the namespace's, completes
	// instead of waiting forever for a controller that no longer
	// exists. Then delete what remains, and let 404s stay silent:
	// this function runs on every sweep, and the converged state is
	// nothing but 404s.
	for _, path := range fluxTeardownPaths[:2] {
		_ = c.PatchJSON(path, []byte(`{"metadata": {"finalizers": null}}`))
	}
	for _, path := range fluxTeardownPaths {
		err := c.RequestJSON(http.MethodDelete, path, nil, nil)
		if err == nil {
			fmt.Printf("the cluster no longer declares flux; deleted %s\n", path)
		} else if !errors.Is(err, kubernetes.ErrNotFound) && !errors.Is(err, kubernetes.ErrConflict) {
			fmt.Printf("flux teardown, deleting %s: %v\n", path, err)
		}
	}
}
