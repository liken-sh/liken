package main

// The fake cluster's view serves its maps as *unstructured.Unstructured
// objects, the way an informer's store does, and reads them on each
// call. So a test that changes a map between two passes changes what the
// next pass reads, as a watch event would.
//
// The view leaves out each object named in unseen, as when the watch has
// not yet received an object that the API server has. It returns
// malformed each object whose collection or object path is in fails, so
// that the object does not convert to its Go type.

import (
	"encoding/json"
	"strings"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/tools/cache"
)

// fakeSource serves one map of the fake cluster. collection and object
// return the paths that fails is keyed by.
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

// fakeStore adapts a fakeSource to the cache.Store methods that
// objectcache.go calls. The other methods are not implemented.
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

func (s fakeSource[T]) Stored(key string) (any, bool) {
	item, held, _ := s.GetByKey(key)
	return item, held
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

// asUnstructured decodes the object's JSON the way client-go's dynamic
// client does, with whole numbers as int64.
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

// malformed keeps the object's name and namespace and makes its spec and
// status strings, so converting it to its Go type fails.
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
			// The operator's pod watch selects these label values
			// only.
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
		sinks:       fakeSource[Sink]{f, valuesOf(f.sinks), sinksPath, clusterPath(sinksPath)},
		televisions: fakeSource[Television]{f, valuesOf(f.televisions), televisionsPath, clusterPath(televisionsPath)},
	}
}

const televisionsPath = "/apis/equipment.liken.sh/v1alpha1/televisions"
