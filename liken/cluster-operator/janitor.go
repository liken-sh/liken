package main

// This file deletes the workloads of features that the cluster
// document no longer declares.
//
// A feature's workload manifests ship in the image. init copies them
// into k3s's auto-deploy directory while the cluster document declares
// the feature, and deletes them when the document drops it
// (init/features.go and init/restart.go). Deleting the file does not
// delete the workloads, because k3s's deploy controller applies the
// files that exist and does not reconcile an Addon whose file is gone.
// k3s does delete an addon's objects when its component is on the
// disable list, but a liken feature is not a k3s component.
//
// So every feature's workloads have a liken.sh/feature annotation with
// the feature's name, and each pass deletes every liken-system
// workload whose annotation names a feature that the document does
// not declare. It does not wait for the machines to apply the new
// document, because no machine needs the workload once the document
// drops the feature. Workloads with no annotation, such as the
// operator and log-relay DaemonSets, are never deleted.

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
)

// featureAnnotation is set to the name of the feature that a workload
// belongs to. Only feature manifests set it, for example
// open-iscsi/manifests/iscsid.yaml.
const featureAnnotation = "liken.sh/feature"

// featureWorkloadKinds lists the kinds that deleteRetractedWorkloads
// reads, each with its list endpoint in liken-system. The workloads of
// a kind not listed here are not deleted when their feature is
// retracted.
var featureWorkloadKinds = []struct {
	kind     string
	listPath string
}{
	{"daemonset", daemonSetsPath},
}

// annotatedObject decodes the name and annotations of any object: a
// DaemonSet, the flux-system Namespace, or a Flux controller pod.
type annotatedObject struct {
	Metadata struct {
		Name        string            `json:"name"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
}

// decideRetractions returns the workloads whose feature is not in
// spec.features. A feature is declared when its key is present, the
// same test that init uses to copy the manifests.
func decideRetractions(features map[string]*cluster.FeatureConfig, workloads []annotatedObject) []annotatedObject {
	var retracted []annotatedObject
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

// deleteRetractedWorkloads lists each kind in featureWorkloadKinds and
// deletes the retracted workloads by name. Background propagation
// deletes their pods too.
func deleteRetractedWorkloads(r *fleetReader, clusterDoc *cluster.Cluster) {
	for _, k := range featureWorkloadKinds {
		workloads, err := r.workloads(k.listPath)
		if err != nil {
			fmt.Printf("listing %ss to find retracted feature workloads: %v\n", k.kind, err)
			continue
		}
		for _, w := range decideRetractions(clusterDoc.Spec.Features, workloads) {
			name := w.Metadata.Name
			slug := w.Metadata.Annotations[featureAnnotation]
			path := k.listPath + "/" + name + "?propagationPolicy=Background"
			if err := r.client.RequestJSON(http.MethodDelete, path, nil, nil); err != nil {
				fmt.Printf("deleting %s %s for the retracted %s feature: %v\n", k.kind, name, slug, err)
			} else {
				fmt.Printf("deleted %s %s, because the Cluster no longer declares the %s feature\n", k.kind, name, slug)
			}
		}
	}
}

// fluxNamespace is the Namespace of the Flux installation, and
// fluxNamespacePath is its API path. The Namespace's liken.sh/feature
// annotation records that liken created the installation.
const (
	fluxNamespace     = "flux-system"
	fluxNamespacePath = "/api/v1/namespaces/" + fluxNamespace
)

// fluxTeardownCondition is the type of the Cluster condition that
// reports a refused teardown. The publishClusterStatus function in
// fleet.go removes it from the Cluster when tearDownFlux returns nil.
const fluxTeardownCondition = "FluxTeardown"

// fluxTeardownPaths are the delete targets of stage 3, in order. The
// sync objects come first, then the Namespace, whose deletion removes
// everything namespaced that remains, including the deploy key
// `Secret`. The cluster-scoped objects come last. The CRD and RBAC
// names must match the Flux manifests that flux.go applies, and
// TestTheTeardownAndItsRightsCoverTheFluxManifest in flux_test.go checks them.
var fluxTeardownPaths = []string{
	"/apis/kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system",
	"/apis/source.toolkit.fluxcd.io/v1/namespaces/flux-system/gitrepositories/flux-system",
	fluxNamespacePath,
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

// fluxControllerPodsPath lists the pods of the Flux controllers,
// running or terminating.
var fluxControllerPodsPath = "/api/v1/namespaces/flux-system/pods?labelSelector=" +
	url.QueryEscape("app in (source-controller,kustomize-controller)")

// declinedFluxTeardown is the condition that reports a refusal to
// tear down a Flux that liken did not create. Its message gives the
// kubectl command that lets tearDownFlux delete the installation.
func declinedFluxTeardown() *api.Condition {
	mark := featureAnnotation + "=" + cluster.FeatureFlux
	return &api.Condition{
		Type:   fluxTeardownCondition,
		Status: api.ConditionFalse,
		Reason: "NotPlantedByLiken",
		Message: "The Cluster no longer declares the flux feature, but the " + fluxNamespace +
			" Namespace has no " + mark + " annotation, so liken did not create this" +
			" Flux installation and deleted nothing. To let liken delete it, run:" +
			" kubectl annotate namespace " + fluxNamespace + " " + mark,
	}
}

// tearDownFlux deletes the Flux installation that liken created when
// the document no longer declares the flux feature.
//
// It deletes only a Flux that liken created. An adopted cluster can
// already run a Flux that someone else installed, and a document
// written during adoption can leave flux out of spec.features without
// meaning to remove it. flux/manifests/flux-system.yaml sets the
// liken.sh/feature annotation on the flux-system Namespace, and
// tearDownFlux deletes nothing unless the annotation is there.
//
// The teardown cannot be a plain delete either. When the
// Kustomization is deleted while kustomize-controller runs, the
// controller's finalizer prunes everything the repository applied,
// including the Machine and Cluster documents. So tearDownFlux deletes
// the controller Deployments, waits for their pods to exit, removes
// the finalizers itself, and only then deletes the objects.
//
// Each call does one stage and returns, so a stage starts only after
// a new read shows that the stage before it finished:
//
//  0. The flux-system Namespace has no liken.sh/feature annotation:
//     delete nothing and report the refusal on the Cluster.
//  1. A controller Deployment exists: delete it.
//  2. A controller pod exists: wait, because a pod that is still
//     terminating can still process a finalizer.
//  3. No controller pod exists: remove the finalizers of the sync
//     objects and delete them, then delete the Namespace, the CRDs,
//     and the RBAC objects, including the ClusterRole and
//     ClusterRoleBinding liken-engine-planter.
//
// Deleting the Namespace deletes the deploy key, so enabling the
// feature again creates a new key to register. The workloads that the
// repository deployed keep running, because stopping the sync must
// not undeploy them.
//
// The RBAC rules for the teardown are in manifests/cluster-operator.yaml,
// because the teardown deletes the grants that the feature's own
// manifests create.
//
// It returns the FluxTeardown condition when it refuses, and nil
// otherwise.
func tearDownFlux(c *apiclient.Client, clusterDoc *cluster.Cluster) *api.Condition {
	if clusterDoc.FeatureEnabled(cluster.FeatureFlux) {
		return nil
	}

	// Stage 0: a missing Namespace means there is nothing to tear
	// down. A failed read does not show who created the installation,
	// so it ends the pass with no condition.
	//
	// A flux-system Namespace that liken created before its manifest
	// set the annotation has none, so tearDownFlux refuses and deletes
	// nothing until a person annotates it. Refusing is the safer
	// mistake, because the other mistake deletes someone else's Flux.
	namespace := &annotatedObject{}
	err := c.RequestJSON(http.MethodGet, fluxNamespacePath, nil, namespace)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	if err != nil {
		fmt.Printf("reading the %s Namespace to decide on the Flux teardown: %v\n", fluxNamespace, err)
		return nil
	}
	if namespace.Metadata.Annotations[featureAnnotation] != cluster.FeatureFlux {
		return declinedFluxTeardown()
	}

	// Stage 1: any delete, failed or not, ends the pass. After a
	// failed delete the Deployment still exists, and during its rollout
	// the pods can be missing for a moment. Stage 2 would then find no
	// pod, and stage 3 could remove the finalizers just before a
	// controller starts again.
	stop := false
	for _, name := range []string{"source-controller", "kustomize-controller"} {
		path := "/apis/apps/v1/namespaces/flux-system/deployments/" + name
		if err := c.RequestJSON(http.MethodGet, path, nil, nil); errors.Is(err, apiclient.ErrNotFound) {
			continue
		}
		err := c.RequestJSON(http.MethodDelete, path+"?propagationPolicy=Background", nil, nil)
		switch {
		case err == nil:
			fmt.Printf("deleted the %s Deployment, because liken installed Flux and the Cluster no longer declares the flux feature\n", name)
			stop = true
		case !errors.Is(err, apiclient.ErrNotFound):
			fmt.Printf("deleting the %s Deployment for the Flux teardown: %v\n", name, err)
			stop = true
		}
	}
	if stop {
		return nil
	}

	// Stage 2: wait until no controller pod exists, terminating pods
	// included.
	pods, err := kubernetes.List[annotatedObject](c, fluxControllerPodsPath)
	if err != nil || len(pods) > 0 {
		return nil
	}

	// Stage 3: remove the sync objects' finalizers, because no
	// controller is left to remove them, and their deletion and the
	// Namespace's would wait forever. Each pass runs this stage again
	// until the Namespace is gone. A 404 is not logged, because those
	// passes find most objects already deleted. A 409 is not logged,
	// because the API server returns one for a delete of a Namespace
	// that is already terminating.
	for _, path := range fluxTeardownPaths[:2] {
		_ = kubernetes.PatchJSON(c, path, []byte(`{"metadata": {"finalizers": null}}`))
	}
	for _, path := range fluxTeardownPaths {
		err := c.RequestJSON(http.MethodDelete, path, nil, nil)
		if err == nil {
			fmt.Printf("deleted %s, because liken installed Flux and the Cluster no longer declares the flux feature. The controllers are gone, so Flux cannot prune it\n", path)
		} else if !errors.Is(err, apiclient.ErrNotFound) && !errors.Is(err, apiclient.ErrConflict) {
			fmt.Printf("deleting %s for the Flux teardown: %v\n", path, err)
		}
	}
	return nil
}
