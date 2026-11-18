package main

// clusterView is how a reconcile pass reads the cluster. Each collection
// comes from the store of one informer (clusterwatch.go), so a pass that
// finds nothing to change sends the API server no request. While the API
// server forbids a collection's watch, the view lists that collection
// from the API server instead (watchedsource.go).
//
// A store can lag the API server. That is fine for deciding what to do,
// because a change that has not arrived yet triggers another pass when
// it arrives. It is not fine for acting: a create or delete that the
// previous pass sent may not be in the store yet, and a pass that acted
// on the store could repeat it. So reconcile, ensurePlayback, and
// reconcilePersistent read the objects again from the API server before
// they create, delete, or recreate anything.
//
// Plays and Players are read through the object cache (objectcache.go),
// which also records the versions this operator wrote and read, because
// a pass acts on their status.
//
// An object that does not convert into the operator's struct fails the
// read of its whole collection, with an error that names the object.

import (
	"fmt"
	"sort"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// objectSource is what the view reads of one collection: every object,
// and one object by its key (namespace/name, or name for a
// cluster-scoped kind). While the watch is ready, both read the
// watch's store. Otherwise they read the API server (watchedsource.go).
// Stored always reads the store alone, for a caller that must not send
// a request.
type objectSource interface {
	List() ([]any, error)
	GetByKey(key string) (item any, exists bool, err error)
	Stored(key string) (item any, exists bool)
}

type clusterView struct {
	plays       informer.Held
	players     informer.Held
	remotes     objectSource
	keymaps     objectSource
	preferences objectSource
	peripherals objectSource
	// pods has only this operator's pods: the playback pods, the idle
	// pods, and the Remotes' reader pods.
	pods      objectSource
	claims    objectSource
	displays  objectSource
	slices    objectSource
	receivers objectSource
	// Sink is a kind of audio-operator and Television a kind of
	// equipment-operator. The volume code reads the Sinks that a Player
	// plays through, and relayScreenAsks reads the screen requests in each
	// Television's status.
	sinks       objectSource
	televisions objectSource
}

// listOf converts every object in a source and sorts them by namespace,
// then name, which is the order the API server lists them in. A store
// returns objects in no fixed order, and a decision that depends on the
// order, such as which Play runs on a Player, must come out the same on
// every pass.
func listOf[T any](source objectSource) ([]T, error) {
	items, err := source.List()
	if err != nil {
		return nil, err
	}
	objects := make([]*unstructured.Unstructured, 0, len(items))
	for _, item := range items {
		object, ok := item.(*unstructured.Unstructured)
		if !ok {
			return nil, fmt.Errorf("the cache holds a %T, want an *unstructured.Unstructured", item)
		}
		objects = append(objects, object)
	}
	sort.Slice(objects, func(i, j int) bool {
		if objects[i].GetNamespace() != objects[j].GetNamespace() {
			return objects[i].GetNamespace() < objects[j].GetNamespace()
		}
		return objects[i].GetName() < objects[j].GetName()
	})
	out := make([]T, 0, len(objects))
	for _, object := range objects {
		converted, err := informer.Convert[T](object)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

// oneOf returns apiclient.ErrNotFound when the source has no object at
// key, the same error the API client returns, so a caller handles both
// sources the same way.
func oneOf[T any](source objectSource, key string) (*T, error) {
	item, exists, err := source.GetByKey(key)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, apiclient.ErrNotFound
	}
	converted, err := informer.Convert[T](item)
	if err != nil {
		return nil, err
	}
	return &converted, nil
}

func namespacedKey(namespace, name string) string { return namespace + "/" + name }

func (v *clusterView) Remotes() ([]Remote, error) { return listOf[Remote](v.remotes) }
func (v *clusterView) Keymaps() ([]Keymap, error) { return listOf[Keymap](v.keymaps) }

func (v *clusterView) Peripherals() ([]Peripheral, error) {
	return listOf[Peripheral](v.peripherals)
}

func (v *clusterView) ResourceSlices() ([]ResourceSlice, error) {
	return listOf[ResourceSlice](v.slices)
}

func (v *clusterView) Receivers() ([]Receiver, error) { return listOf[Receiver](v.receivers) }

func (v *clusterView) Televisions() ([]Television, error) {
	return listOf[Television](v.televisions)
}

// Sink reads one cluster-scoped Sink by name.
func (v *clusterView) Sink(name string) (*Sink, error) { return oneOf[Sink](v.sinks, name) }

func (v *clusterView) Remote(namespace, name string) (*Remote, error) {
	return oneOf[Remote](v.remotes, namespacedKey(namespace, name))
}

// MediaPreferences reads one cluster-scoped MediaPreferences by name.
func (v *clusterView) MediaPreferences(name string) (*MediaPreferences, error) {
	return oneOf[MediaPreferences](v.preferences, name)
}

func (v *clusterView) Pod(namespace, name string) (*Pod, error) {
	return oneOf[Pod](v.pods, namespacedKey(namespace, name))
}

// StoredPod reads one pod from the pod store alone, and returns nil when
// the store does not have it or it does not convert. It never sends a
// request, even while the API server forbids the pod watch.
func (v *clusterView) StoredPod(namespace, name string) *Pod {
	item, stored := v.pods.Stored(namespacedKey(namespace, name))
	if !stored {
		return nil
	}
	pod, err := informer.Convert[Pod](item)
	if err != nil {
		return nil
	}
	return &pod
}

func (v *clusterView) ResourceClaim(namespace, name string) (*ResourceClaim, error) {
	return oneOf[ResourceClaim](v.claims, namespacedKey(namespace, name))
}

// Display reads one cluster-scoped Display by its monitor id.
func (v *clusterView) Display(name string) (*Display, error) {
	return oneOf[Display](v.displays, name)
}
