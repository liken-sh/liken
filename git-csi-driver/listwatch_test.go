package main

// These tests do not cover the reflector's loop, which upstream tests.
// They run the driver's handlers through the real reflector against a
// fake clientset, and cover what the driver adds around it.

import (
	"io"
	"sync"
	"testing"
	"testing/synctest"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	k8stesting "k8s.io/client-go/testing"
)

// gone is the error event an API server sends when the watch's
// resourceVersion is older than the oldest change it still keeps.
func gone() watch.Event {
	status := apierrors.NewResourceExpired("too old resource version").Status()
	return watch.Event{Type: watch.Error, Object: &status}
}

func TestTheInformerStoresNoManagedFields(t *testing.T) {
	pv := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{
		Name:          "franchises",
		ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
	}}

	stored, err := dropManagedFields(pv)

	if err != nil {
		t.Fatalf("running the transform: %v", err)
	}
	if fields := stored.(*corev1.PersistentVolume).ManagedFields; fields != nil {
		t.Errorf("the informer stores managedFields %v, want none", fields)
	}
}

func TestADemandWrittenBetweenTheListAndTheWatchPullsTheTree(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		vol := demandedVolume(t, n, "franchises", fileURL(source), "on-demand")
		want := commitFiles(t, source, map[string]string{"a.txt": "two"})

		// The list returns the state from before the demand, and the
		// demand is written before the watch opens. Only a watch that
		// starts at the list's resourceVersion delivers it.
		client := cluster(t, n)
		resource := schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}
		kind := schema.GroupVersionKind{Version: "v1", Kind: "PersistentVolume"}
		var once sync.Once
		client.PrependReactor("list", "persistentvolumes",
			func(k8stesting.Action) (bool, runtime.Object, error) {
				handled := false
				var listed runtime.Object
				var err error
				once.Do(func() {
					handled = true
					listed, err = client.Tracker().List(resource, kind, "")
					if err != nil {
						return
					}
					stored, _ := client.Tracker().Get(resource, "", "franchises")
					demanded := annotated(stored.(*corev1.PersistentVolume).DeepCopy(), demandAt(0))
					err = client.Tracker().Update(resource, demanded, "")
				})
				return handled, listed, err
			})
		watchDemands(t, n)

		waitForCommit(t, vol, want)
	})
}
