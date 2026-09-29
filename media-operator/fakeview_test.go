package main

// The fake cluster's view: each collection a pass reads, served from
// the fake cluster's maps the way a watch's store holds it, as an
// *unstructured.Unstructured. A test that changes a map between two
// passes changes what the next pass reads, the way a watch delivers a
// change to the store.
//
// Two fields of the fake cluster shape what the view answers. unseen
// names objects the view does not hold yet, the way a watch can still
// be carrying an object the API server already has. fails names paths
// whose objects do not convert, the way a list that the API server
// answered with a bad object failed.

import (
	"encoding/json"
	"strings"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/tools/cache"
)

// fakeSource serves one map of the fake cluster. collection is the
// collection's path and object the path of one object in it, the paths
// fails is keyed by.
type fakeSource[T any] struct {
	cluster    *fakeCluster
	objects    func() []*T
	collection string
	object     func(namespace, name string) string
}

func (s fakeSource[T]) List() ([]any, error) {
	var items []any
	for _, object := range s.objects() {
		item := asUnstructured(object)
		if s.cluster.unseen[item.GetName()] {
			continue
		}
		if s.cluster.fails[s.collection] {
			item = malformed(item)
		}
		items = append(items, item)
	}
	return items, nil
}

// fakeStore is a fakeSource in the shape of client-go's cache.Store, for
// the reads of objectcache.go. A method it does not define is one a
// read never calls.
type fakeStore[T any] struct {
	cache.Store
	source fakeSource[T]
}

func (s fakeStore[T]) List() []any {
	items, _ := s.source.List()
	return items
}

func (s fakeStore[T]) GetByKey(key string) (any, bool, error) { return s.source.GetByKey(key) }

func (s fakeStore[T]) ListKeys() []string {
	var keys []string
	items, _ := s.source.List()
	for _, item := range items {
		key, _ := cache.MetaNamespaceKeyFunc(item)
		keys = append(keys, key)
	}
	return keys
}

func (s fakeSource[T]) GetByKey(key string) (any, bool, error) {
	namespace, name, namespaced := strings.Cut(key, "/")
	if !namespaced {
		namespace, name = "", key
	}
	if s.cluster.unseen[name] {
		return nil, false, nil
	}
	for _, object := range s.objects() {
		item := asUnstructured(object)
		if item.GetNamespace() != namespace || item.GetName() != name {
			continue
		}
		if s.cluster.fails[s.collection] || s.cluster.fails[s.object(namespace, name)] {
			return malformed(item), true, nil
		}
		return item, true, nil
	}
	return nil, false, nil
}

// asUnstructured encodes an object and decodes it the way client-go's
// dynamic client decodes one from the API server, whole numbers as
// int64.
func asUnstructured(object any) *unstructured.Unstructured {
	encoded, err := json.Marshal(object)
	if err != nil {
		panic(err)
	}
	fields := map[string]any{}
	if err := utiljson.Unmarshal(encoded, &fields); err != nil {
		panic(err)
	}
	return &unstructured.Unstructured{Object: fields}
}

// malformed keeps the object's name and gives it a spec and a status of
// the wrong type, so it does not convert.
func malformed(item *unstructured.Unstructured) *unstructured.Unstructured {
	bad := &unstructured.Unstructured{Object: map[string]any{"spec": "garbled", "status": "garbled"}}
	bad.SetKind("Garbled")
	bad.SetName(item.GetName())
	bad.SetNamespace(item.GetNamespace())
	return bad
}

func valuesOf[T any](objects map[string]*T) func() []*T {
	return func() []*T {
		items := make([]*T, 0, len(objects))
		for _, name := range sortedNames(objects) {
			items = append(items, objects[name])
		}
		return items
	}
}

func clusterPath(collection string) func(string, string) string {
	return func(_, name string) string { return collection + "/" + name }
}

// view answers the fake cluster's view. It reads the maps on every
// call, so it follows every change a test or a write makes.
func (f *fakeCluster) view() *clusterView {
	return &clusterView{
		plays: informer.Held{
			View: informer.View{
				Store:  fakeStore[Play]{source: fakeSource[Play]{f, valuesOf(f.plays), playsPath, playPath}},
				Synced: func() bool { return true },
				Whole:  true,
			},
			Versions: memo.New(),
		},
		players: informer.Held{
			View: informer.View{
				Store:  fakeStore[Player]{source: fakeSource[Player]{f, valuesOf(f.players), playersPath, playerPath}},
				Synced: func() bool { return true },
				Whole:  true,
			},
			Versions: memo.New(),
		},
		remotes:     fakeSource[Remote]{f, valuesOf(f.remotes), remotesAllPath, remotePath},
		keymaps:     fakeSource[Keymap]{f, valuesOf(f.keymaps), keymapsPath, clusterPath(keymapsPath)},
		preferences: fakeSource[MediaPreferences]{f, valuesOf(f.mediaprefs), mediaPrefsPath, clusterPath(mediaPrefsPath)},
		peripherals: fakeSource[Peripheral]{f, valuesOf(f.peripherals), peripheralsPath, clusterPath(peripheralsPath)},
		pods: fakeSource[Pod]{f, func() []*Pod {
			// The pods watch selects the operator's own pods by their
			// component label, so a pod without one of its three values
			// is not in the view.
			var own []*Pod
			for _, pod := range valuesOf(f.pods)() {
				switch pod.Metadata.Labels[playbackLabelKey] {
				case playbackLabelValue, idleLabelValue, remoteLabelValue:
					own = append(own, pod)
				}
			}
			return own
		}, "/api/v1/pods", func(namespace, name string) string { return podsPath(namespace) + "/" + name }},
		claims: fakeSource[ResourceClaim]{f, valuesOf(f.claims), "/apis/resource.k8s.io/v1/resourceclaims",
			func(namespace, name string) string { return claimsPath(namespace) + "/" + name }},
		displays: fakeSource[Display]{f, valuesOf(f.displays), displaysPath, clusterPath(displaysPath)},
		slices: fakeSource[ResourceSlice]{f, func() []*ResourceSlice {
			slices := make([]*ResourceSlice, len(f.slices))
			for index := range f.slices {
				slices[index] = &f.slices[index]
			}
			return slices
		}, slicesPath, clusterPath(slicesPath)},
		receivers: fakeSource[Receiver]{f, func() []*Receiver {
			if f.receiversAbsent {
				return nil
			}
			return valuesOf(f.receivers)()
		}, receiversPath, clusterPath(receiversPath)},
	}
}
