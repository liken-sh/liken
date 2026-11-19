package main

// Some passes wait for objects that other programs change. The image
// proof waits until every OS container on the node is Ready
// (imports.go). The drain waits until the node's pods leave, and until
// a PodDisruptionBudget allows an eviction that it refused (drain.go).
// A retraction waits until the cluster's HelmCharts or LoadBalancer
// Services are deleted (retraction.go). Each wait gets a watch that
// wakes the pass loop when those objects change.
//
// A watch runs only while its wait does. A pass that reads a kind
// starts its watch, and endPass stops every watch that the pass did
// not read. Each pass decides from what it reads whether the wait
// continues, so an operator that restarts during a drain starts the
// watches it needs on its first pass. Outside a wait, writes to a busy
// node's pods wake no pass and use no memory.
//
// The reader goes to the API server whenever a watch's copy is not
// ready (watches.go). So the first pass of a wait reads the API server
// and starts the watch, and the watch's first sync wakes the next
// pass, which reads the copy.

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

// The kinds that a wait can watch.
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

// waits starts and stops the watches for the waits. A nil *waits
// starts no watch, so every read goes to the API server.
type waits struct {
	ctx     context.Context
	watcher dynamic.Interface
	wake    func()
	node    string

	mu      sync.Mutex
	running map[string]*waitWatch
	used    map[string]bool
}

type waitWatch struct {
	copy   *informer.Collection
	cancel context.CancelFunc
}

func newWaits(ctx context.Context, watcher dynamic.Interface, wake func(), node string) *waits {
	return &waits{ctx: ctx, watcher: watcher, wake: wake, node: node,
		running: map[string]*waitWatch{}, used: map[string]bool{}}
}

// use returns the copy of one kind and starts its watch if it is not
// running. The watch keeps running until a pass ends without calling
// use for the kind.
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

// endPass stops every watch that the pass did not use, and clears the
// record of what the pass used.
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

// watchOf returns what to watch for one kind and the options that
// decide which events wake the loop.
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
			// The HelmChart kind exists only while the Helm controller
			// runs, and a missing kind means there are no charts.
			Absent: apierrors.IsNotFound,
		}
	default:
		return informer.Source{Resource: serviceResource}, informer.Options{
			Handler:   serviceHandler(w.wake),
			Transform: trimService,
		}
	}
}

// budgetHandler wakes the loop when a budget may now allow an eviction
// that it refused earlier: when status.disruptionsAllowed rises, and
// when the budget is deleted. A new budget only restricts more, so it
// wakes nothing.
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

// presenceHandler wakes the loop when an object is added or deleted.
// A retraction waits only for objects to be deleted, and the Helm
// controller updates each chart's status while it works, so updates
// wake nothing.
func presenceHandler(wake func()) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { wake() },
		DeleteFunc: func(any) { wake() },
	}
}

// serviceHandler wakes the loop when a Service is added or deleted, or
// when spec.type changes, because that can make it a LoadBalancer or
// stop it being one.
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

// trimPod keeps only the pod fields that the image proof and the drain
// read through kubernetes.Pod, so the copy of a node's pods stays
// small. A dropped field reads as its zero value, so every field that
// the code reads must stay. For example, without ownerReferences the
// drain evicts DaemonSet pods. Without the mirror annotation it evicts
// mirror pods, which the kubelet creates again. Without the
// tolerations it evicts a pod that tolerates the cordon, and the pod is
// scheduled onto the same node again.
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
	if tolerations, ok, _ := unstructured.NestedSlice(object.Object, "spec", "tolerations"); ok {
		spec["tolerations"] = tolerations
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

// trimService keeps a Service's name, namespace, and type, which are
// the fields that a retraction reads, so the copy of every Service in
// the cluster stays small.
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

// nodePods returns every pod on the node, from the copy when it is
// ready and from the API server otherwise.
func (r *reader) nodePods(node string) ([]kubernetes.Pod, error) {
	copy := r.waits.use(waitPods)
	if pods, ok := watch.List[kubernetes.Pod](r.view(copy)); ok {
		// The copy returns pods in no fixed order, but the API server
		// lists them by namespace and name. Sorting makes the drain
		// evict pods in the same order from either source.
		slices.SortFunc(pods, func(a, b kubernetes.Pod) int {
			return strings.Compare(a.Metadata.Namespace+"/"+a.Metadata.Name, b.Metadata.Namespace+"/"+b.Metadata.Name)
		})
		return pods, nil
	}
	return kubernetes.ListPodsOnNode(r.client, node)
}

// watchBudgets keeps the budget watch running, so that a budget that
// allows a refused eviction wakes the drain.
func (r *reader) watchBudgets() {
	r.waits.use(waitBudgets)
}

// helmCharts returns every HelmChart in the cluster.
func (r *reader) helmCharts() ([]kubernetes.HelmChart, error) {
	copy := r.waits.use(waitCharts)
	if charts, ok := watch.List[kubernetes.HelmChart](r.view(copy)); ok {
		return charts, nil
	}
	return kubernetes.ListHelmCharts(r.client)
}

// loadBalancerServices returns every Service of type LoadBalancer in
// the cluster.
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
