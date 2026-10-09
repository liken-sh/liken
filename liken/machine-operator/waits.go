package main

// The watches that run only while a pass waits on objects that other
// programs change.
//
// Three features wait that way. The image proof waits for every OS
// container on the node to become Ready (imports.go). The drain waits
// for the node's pods to leave, and for a PodDisruptionBudget to allow
// an eviction it refused (drain.go). A feature removal waits for the
// cluster's HelmCharts or LoadBalancer Services to be deleted
// (retraction.go). Each of those reads lists objects that change while
// the wait lasts, so each wait gets a watch, and the watch wakes the
// pass that reads it.
//
// A watch runs only while its wait does. A pass that reads a kind
// starts its watch, and the loop stops every watch that the last pass
// did not read (endPass). A wait begins and ends on a pass, and each
// pass works out from the state it reads whether the wait goes on, so
// an operator that restarts in the middle of a drain starts the
// watches it needs on its first pass. Outside a wait, a busy node's pod
// writes wake no pass, and no copy of them costs memory.
//
// A copy that is not ready yet answers nothing, and the read goes to
// the API server, as every read of the reader does (watches.go). The
// first pass of a wait reads the API server and starts the watch, and
// the watch's first sync wakes the next pass, which reads the copy.

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

// The kinds a wait can watch.
const (
	waitPods     = "node pods"
	waitBudgets  = "PodDisruptionBudgets"
	waitCharts   = "HelmCharts"
	waitServices = "Services"
)

var (
	budgetResource  = schema.GroupVersionResource{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"}
	chartResource   = schema.GroupVersionResource{Group: "helm.cattle.io", Version: "v1", Resource: "helmcharts"}
	serviceResource = schema.GroupVersionResource{Version: "v1", Resource: "services"}
)

// waits starts and stops the watches of the waits. A nil *waits starts
// nothing, and every read goes to the API server.
type waits struct {
	ctx     context.Context
	watcher dynamic.Interface
	wake    func()
	node    string

	mu      sync.Mutex
	running map[string]*waitWatch
	used    map[string]bool
}

// waitWatch is one running watch and the cancel that stops it.
type waitWatch struct {
	copy   *informer.Collection
	cancel context.CancelFunc
}

func newWaits(ctx context.Context, watcher dynamic.Interface, wake func(), node string) *waits {
	return &waits{ctx: ctx, watcher: watcher, wake: wake, node: node,
		running: map[string]*waitWatch{}, used: map[string]bool{}}
}

// use answers the copy of one kind, and starts its watch when it is not
// running. The pass that calls it keeps the watch running for one more
// pass.
func (w *waits) use(kind string) *informer.Collection {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.used[kind] = true
	if running, ok := w.running[kind]; ok {
		return running.copy
	}
	ctx, cancel := context.WithCancel(w.ctx)
	source, options := w.watchOf(kind)
	options.Synced = w.wake
	options.UnreadyOnWatchError = true
	running := &waitWatch{copy: informer.Start(ctx, w.watcher, source, options), cancel: cancel}
	w.running[kind] = running
	return running.copy
}

// endPass stops every watch that the pass did not use, and starts the
// count again for the next pass.
func (w *waits) endPass() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for kind, running := range w.running {
		if !w.used[kind] {
			running.cancel()
			delete(w.running, kind)
		}
	}
	clear(w.used)
}

// watchOf names the source of one kind and what wakes the loop.
func (w *waits) watchOf(kind string) (informer.Source, informer.Options) {
	switch kind {
	case waitPods:
		pods := informer.Source{Resource: podResource, FieldSelector: "spec.nodeName=" + w.node}
		return pods, informer.Options{
			Handler:   watch.WakeOnContent[kubernetes.Pod](pods, w.wake),
			Transform: trimPod,
		}
	case waitBudgets:
		return informer.Source{Resource: budgetResource}, informer.Options{
			Handler: budgetHandler(w.wake),
		}
	case waitCharts:
		return informer.Source{Resource: chartResource}, informer.Options{
			Handler: presenceHandler(w.wake),
			// The HelmChart kind exists only while k3s's Helm controller
			// runs. An absent kind holds no chart.
			Absent: apierrors.IsNotFound,
		}
	default:
		return informer.Source{Resource: serviceResource}, informer.Options{
			Handler:   serviceHandler(w.wake),
			Transform: trimService,
		}
	}
}

// budgetHandler wakes the loop when a budget could allow an eviction it
// refused: a budget whose status.disruptionsAllowed rose, and a budget
// that was deleted, which guards nothing any more. A new budget
// guards more, so it wakes nothing.
func budgetHandler(wake func()) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(before, after any) {
			old, okOld := before.(*unstructured.Unstructured)
			now, okNew := after.(*unstructured.Unstructured)
			if !okOld || !okNew {
				return
			}
			was, _, _ := unstructured.NestedInt64(old.Object, "status", "disruptionsAllowed")
			is, _, _ := unstructured.NestedInt64(now.Object, "status", "disruptionsAllowed")
			if is > was {
				wake()
			}
		},
		DeleteFunc: func(any) { wake() },
	}
}

// presenceHandler wakes the loop when an object appears or leaves. A
// removal waits only on whether the objects exist, and the Helm
// controller writes each chart's status as it works, which changes
// nothing the wait reads.
func presenceHandler(wake func()) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { wake() },
		DeleteFunc: func(any) { wake() },
	}
}

// serviceHandler wakes the loop when a Service appears or leaves, or
// changes its type, which can make it a LoadBalancer or stop it being
// one.
func serviceHandler(wake func()) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(any) { wake() },
		UpdateFunc: func(before, after any) {
			old, okOld := before.(*unstructured.Unstructured)
			now, okNew := after.(*unstructured.Unstructured)
			if !okOld || !okNew {
				return
			}
			was, _, _ := unstructured.NestedString(old.Object, "spec", "type")
			is, _, _ := unstructured.NestedString(now.Object, "spec", "type")
			if was != is {
				wake()
			}
		},
		DeleteFunc: func(any) { wake() },
	}
}

// trimPod keeps only what the proof and the drain read from a pod
// (kubernetes.Pod), so a copy of a node's pods costs under a kilobyte
// for each pod rather than tens of kilobytes. A field the trim drops
// converts to its zero value, so the trim must keep every field the
// code reads. Without the owner references, every DaemonSet pod looks
// evictable, and without the mirror annotation, the drain evicts
// mirror pods that the kubelet creates again.
func trimPod(object *unstructured.Unstructured) {
	kept := map[string]any{
		"apiVersion": object.GetAPIVersion(),
		"kind":       object.GetKind(),
	}
	metadata := map[string]any{}
	for _, key := range []string{"name", "namespace", "uid", "resourceVersion", "ownerReferences", "deletionTimestamp"} {
		if value, ok := object.Object["metadata"].(map[string]any)[key]; ok {
			metadata[key] = value
		}
	}
	annotations := map[string]any{}
	for key, value := range object.GetAnnotations() {
		if key == mirrorPodAnnotation || key == osVersionAnnotation {
			annotations[key] = value
		}
	}
	if len(annotations) > 0 {
		metadata["annotations"] = annotations
	}
	kept["metadata"] = metadata

	spec := map[string]any{}
	if name, ok, _ := unstructured.NestedString(object.Object, "spec", "nodeName"); ok {
		spec["nodeName"] = name
	}
	if claims, ok, _ := unstructured.NestedSlice(object.Object, "spec", "resourceClaims"); ok {
		spec["resourceClaims"] = claims
	}
	if volumes, ok, _ := unstructured.NestedSlice(object.Object, "spec", "volumes"); ok {
		var kept []any
		for _, v := range volumes {
			volume, _ := v.(map[string]any)
			trimmed := map[string]any{"name": volume["name"]}
			if hostPath, ok := volume["hostPath"].(map[string]any); ok {
				trimmed["hostPath"] = map[string]any{"path": hostPath["path"]}
			}
			kept = append(kept, trimmed)
		}
		spec["volumes"] = kept
	}
	kept["spec"] = spec

	status := map[string]any{}
	if phase, ok, _ := unstructured.NestedString(object.Object, "status", "phase"); ok {
		status["phase"] = phase
	}
	if containers, ok, _ := unstructured.NestedSlice(object.Object, "status", "containerStatuses"); ok {
		var trimmed []any
		for _, c := range containers {
			container, _ := c.(map[string]any)
			trimmed = append(trimmed, map[string]any{
				"name": container["name"], "image": container["image"], "ready": container["ready"],
			})
		}
		status["containerStatuses"] = trimmed
	}
	kept["status"] = status
	object.Object = kept
}

// trimService keeps a Service's identity and its type, which is all a
// removal reads, so a copy of every Service in the cluster stays small.
func trimService(object *unstructured.Unstructured) {
	kind, _, _ := unstructured.NestedString(object.Object, "spec", "type")
	object.Object = map[string]any{
		"apiVersion": object.GetAPIVersion(),
		"kind":       object.GetKind(),
		"metadata": map[string]any{
			"name":            object.GetName(),
			"namespace":       object.GetNamespace(),
			"uid":             string(object.GetUID()),
			"resourceVersion": object.GetResourceVersion(),
		},
		"spec": map[string]any{"type": kind},
	}
}

// nodePods reads every pod on this node, from the wait's copy when it
// is ready, and from the API server otherwise.
func (r *reader) nodePods(node string) ([]kubernetes.Pod, error) {
	copy := r.waits.use(waitPods)
	if pods, ok := watch.List[kubernetes.Pod](r.view(copy)); ok {
		// The store answers in no order, and the API server lists by
		// namespace and name, so the drain asks pods to leave in the
		// same order from either.
		slices.SortFunc(pods, func(a, b kubernetes.Pod) int {
			return strings.Compare(a.Metadata.Namespace+"/"+a.Metadata.Name, b.Metadata.Namespace+"/"+b.Metadata.Name)
		})
		return pods, nil
	}
	return kubernetes.ListPodsOnNode(r.client, node)
}

// watchBudgets keeps the budget watch running for one more pass, so a
// budget that allows a refused eviction wakes the drain.
func (r *reader) watchBudgets() {
	r.waits.use(waitBudgets)
}

// helmCharts reads every HelmChart in the cluster.
func (r *reader) helmCharts() ([]kubernetes.HelmChart, error) {
	copy := r.waits.use(waitCharts)
	if charts, ok := watch.List[kubernetes.HelmChart](r.view(copy)); ok {
		return charts, nil
	}
	return kubernetes.ListHelmCharts(r.client)
}

// loadBalancerServices reads every Service of type LoadBalancer in the
// cluster.
func (r *reader) loadBalancerServices() ([]kubernetes.Service, error) {
	copy := r.waits.use(waitServices)
	services, ok := watch.List[kubernetes.Service](r.view(copy))
	if !ok {
		return kubernetes.ListLoadBalancerServices(r.client)
	}
	var balanced []kubernetes.Service
	for _, s := range services {
		if s.Spec.Type == "LoadBalancer" {
			balanced = append(balanced, s)
		}
	}
	return balanced, nil
}
