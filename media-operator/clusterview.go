package main

// The view is what a pass reads the cluster through. Each collection
// in it is the store of one informer (clusterwatch.go), which the watch
// keeps current, so a pass that finds everything as it wants it sends
// the API server no request at all.
//
// A read here can be a moment behind the API server: the informer
// takes a change a few milliseconds after the API server writes it.
// That is the right answer for a pass that only compares, because a
// change that has not arrived yet wakes or reaches the next pass. It is
// not the right answer for a pass that is about to act, because a
// create or a delete this operator sent on the last pass can still be
// on its way. So a pass decides from the view, and before it creates,
// deletes, or recreates anything it reads the objects again from the
// API server (reconcile, ensurePlayback, and reconcileStanding), and
// acts on that read.
//
// The accessors answer the operator's own structs. An object that does
// not convert fails the read that holds it, with an error that names
// it, the way a list fails when the API server answers it with an
// object that does not decode.

import (
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// objectSource is the part of an informer's store the view reads: every
// object, and one object by its key, namespace/name or name.
// cache.Store has both methods.
type objectSource interface {
	List() []any
	GetByKey(key string) (item any, exists bool, err error)
}

// clusterView holds one source per collection a pass reads.
type clusterView struct {
	plays       objectSource
	players     objectSource
	remotes     objectSource
	keymaps     objectSource
	preferences objectSource
	peripherals objectSource
	// pods holds the operator's own pods: the playback pods, the idle
	// pods, and the Remotes' reader pods.
	pods      objectSource
	claims    objectSource
	displays  objectSource
	slices    objectSource
	receivers objectSource
}

// listOf converts every object in a source, in the order the API server
// lists them: by namespace, then by name. A pass reads the collection
// in the same order every time, so what it builds from the order, such
// as which Play on a unit runs, does not change from one pass to the
// next.
func listOf[T any](source objectSource) ([]T, error) {
	items := source.List()
	objects := make([]*unstructured.Unstructured, 0, len(items))
	for _, item := range items {
		object, ok := item.(*unstructured.Unstructured)
		if !ok {
			return nil, fmt.Errorf("the store holds a %T, not an object", item)
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
		converted, err := convert[T](object)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

// oneOf converts one object by its key, and answers ErrNotFound for an
// object the source does not hold, the answer the API client gives, so
// a caller treats the two the same.
func oneOf[T any](source objectSource, key string) (*T, error) {
	item, exists, err := source.GetByKey(key)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	converted, err := convert[T](item)
	if err != nil {
		return nil, err
	}
	return &converted, nil
}

func namespacedKey(namespace, name string) string { return namespace + "/" + name }

func (v *clusterView) Plays() ([]Play, error)     { return listOf[Play](v.plays) }
func (v *clusterView) Players() ([]Player, error) { return listOf[Player](v.players) }
func (v *clusterView) Remotes() ([]Remote, error) { return listOf[Remote](v.remotes) }
func (v *clusterView) Keymaps() ([]Keymap, error) { return listOf[Keymap](v.keymaps) }

func (v *clusterView) Peripherals() ([]Peripheral, error) {
	return listOf[Peripheral](v.peripherals)
}

func (v *clusterView) ResourceSlices() ([]ResourceSlice, error) {
	return listOf[ResourceSlice](v.slices)
}

func (v *clusterView) Receivers() ([]Receiver, error) { return listOf[Receiver](v.receivers) }

func (v *clusterView) Player(namespace, name string) (*Player, error) {
	return oneOf[Player](v.players, namespacedKey(namespace, name))
}

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

func (v *clusterView) ResourceClaim(namespace, name string) (*ResourceClaim, error) {
	return oneOf[ResourceClaim](v.claims, namespacedKey(namespace, name))
}

// Display reads one cluster-scoped Display by its monitor id.
func (v *clusterView) Display(name string) (*Display, error) {
	return oneOf[Display](v.displays, name)
}
