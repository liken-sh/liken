package main

// The operator's passes read Displays, Layouts, and this node's pods
// from the watches' stores, not from the API server. Each store holds
// the same selection the passes read: every Display, every Layout, and
// the pods whose spec.nodeName is this node. So a settled pass sends
// the API server no read of these three kinds.
//
// An object that a store does not hold is read from the API server:
// an object that does not exist yet, a pod on another node, and any
// object while its watch has not finished its first read. A list comes
// from a store only after the store holds the whole first read,
// because a store that holds part of it would leave objects out.
//
// A Display's copy in the store can be older than this operator's own
// last write, because the watch delivers the write a moment after the
// API server answers it, and later while the watch is down. The
// Display controller acts on the hardware from what the status holds:
// a captured value, a mode the compositor declined, a write it already
// made. A pass that read an older copy would act again on a change it
// already made, such as restarting the compositor for a mode it
// already recorded as declined. So the operator remembers the
// resourceVersion of each Display's newest copy that it wrote or read
// from the API server, and reads a Display from the API server when
// the store's copy has another version. Once the watch delivers the
// write, the versions match and the store answers again. The operator
// writes no Layout and no pod, so their stores need no memo.

import (
	"errors"
	"reflect"
	"sync"

	"k8s.io/client-go/tools/cache"
)

// storeView is one watch's store, and whether the store holds the
// whole first read. The zero value holds nothing, and every read goes
// to the API server.
type storeView struct {
	store  cache.Store
	synced func() bool
}

// ready reports whether a list can come from the store.
func (v storeView) ready() bool {
	return v.store != nil && v.synced != nil && v.synced()
}

// cachedCopy answers the store's copy of one object, by its key: the
// name of a cluster-scoped object, and namespace/name for a namespaced
// one. A copy that does not convert is logged and not answered, so the
// caller reads the object from the API server.
func cachedCopy[T any](view storeView, key string) (*T, bool) {
	if view.store == nil {
		return nil, false
	}
	object, held, err := view.store.GetByKey(key)
	if err != nil || !held {
		return nil, false
	}
	item, err := convert[T](object)
	if err != nil {
		reportUnconverted("the cached "+key, err)
		return nil, false
	}
	return &item, true
}

// cachedList answers every copy in the store. A copy that does not
// convert is logged and left out, the same as a watch event that does
// not convert.
func cachedList[T any](view storeView) []T {
	var items []T
	for _, object := range view.store.List() {
		item, err := convert[T](object)
		if err != nil {
			reportUnconverted("the cached objects", err)
			continue
		}
		items = append(items, item)
	}
	return items
}

// displayStore reads and writes the Displays for both passes. The
// Display controller and the placement pass run on their own
// goroutines and both write status, so the store sends one Display
// request to the API server at a time. Then the order in which it
// notes each answered version is the order in which the API server
// answered, and the memo always holds the newest version this process
// saw. A read the store answers takes only the memo's lock, so it
// never waits on the other pass's request.
type displayStore struct {
	client *Client
	view   storeView

	// requests holds one Display request at a time, with the note of
	// its answer.
	requests sync.Mutex

	mu sync.Mutex
	// versions is the resourceVersion of each Display's newest copy this
	// process wrote or read from the API server. An empty version is a
	// Display the API server no longer holds. A version is compared only
	// for equality, because the API server gives it no order.
	versions map[string]string
}

// newDisplayStore builds a store with no watch behind it, which reads
// every Display from the API server. main gives it the watch's store.
func newDisplayStore(client *Client, view storeView) *displayStore {
	return &displayStore{client: client, view: view, versions: map[string]string{}}
}

// current reports whether a store's copy is at least as new as every
// copy this process wrote or read.
func (s *displayStore) current(display *Display) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen, noted := s.versions[display.Metadata.Name]
	return !noted || seen == display.Metadata.ResourceVersion
}

// get answers one Display: the store's copy when it holds a current
// one, and the API server's copy when it does not.
func (s *displayStore) get(name string) (*Display, error) {
	if held, ok := cachedCopy[Display](s.view, name); ok && s.current(held) {
		return held, nil
	}
	return s.fetch(name)
}

// note records the version of a Display's newest copy. An empty
// version is a Display the API server no longer holds, or a copy
// another writer changed, and no copy in the store matches it.
func (s *displayStore) note(name, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions[name] = version
}

// fetch reads one Display from the API server.
func (s *displayStore) fetch(name string) (*Display, error) {
	s.requests.Lock()
	defer s.requests.Unlock()
	display, err := getDisplay(s.client, name)
	switch {
	case err == nil:
		s.note(name, display.Metadata.ResourceVersion)
	case errors.Is(err, ErrNotFound):
		s.note(name, "")
	}
	return display, err
}

// list answers every Display, from the store once it holds its first
// read. A copy older than this process's own write is read again from
// the API server, and a Display the API server no longer holds is left
// out.
func (s *displayStore) list() ([]Display, error) {
	if !s.view.ready() {
		return listDisplays(s.client)
	}
	displays := cachedList[Display](s.view)
	current := displays[:0]
	for index := range displays {
		if s.current(&displays[index]) {
			current = append(current, displays[index])
			continue
		}
		fresh, err := s.fetch(displays[index].Metadata.Name)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		current = append(current, *fresh)
	}
	return current, nil
}

// create creates one Display with an empty spec.
func (s *displayStore) create(name string) (*Display, error) {
	s.requests.Lock()
	defer s.requests.Unlock()
	created, err := createDisplay(s.client, name)
	if err == nil {
		s.note(name, created.Metadata.ResourceVersion)
	}
	return created, err
}

// writeStatus writes one Display's status. A write the API server
// refuses with 409 Conflict came from a copy that another writer
// changed since, and the memo then holds no version any copy has, so
// the next read goes to the API server.
func (s *displayStore) writeStatus(display *Display, status DisplayStatus) (*Display, error) {
	s.requests.Lock()
	defer s.requests.Unlock()
	name := display.Metadata.Name
	updated, err := writeDisplayStatus(s.client, display, status)
	switch {
	case err == nil:
		s.note(name, updated.Metadata.ResourceVersion)
	case errors.Is(err, ErrConflict), errors.Is(err, ErrNotFound):
		s.note(name, "")
	}
	return updated, err
}

// settleStatus writes the status that compose makes from a Display's
// published status, when it differs. compose reports false when this
// node must leave the Display alone. A write refused because the copy
// is older than the API server's, or because the Display is gone,
// reads the Display again, composes again from the fresh copy, and
// writes once more. A Display that is gone answers ErrNotFound.
func (s *displayStore) settleStatus(display *Display, compose func(published DisplayStatus) (DisplayStatus, bool)) error {
	if err := s.settleOnce(display, compose); !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
		return err
	}
	fresh, err := s.fetch(display.Metadata.Name)
	if err != nil {
		return err
	}
	*display = *fresh
	return s.settleOnce(display, compose)
}

func (s *displayStore) settleOnce(display *Display, compose func(published DisplayStatus) (DisplayStatus, bool)) error {
	status, ours := compose(display.Status)
	if !ours || reflect.DeepEqual(display.Status, status) {
		return nil
	}
	updated, err := s.writeStatus(display, status)
	if err != nil {
		return err
	}
	*display = *updated
	return nil
}

// clusterStores is the Layout store and the store of this node's pods.
// The zero value holds nothing, and every read goes to the API server.
type clusterStores struct {
	layouts storeView
	pods    storeView
}

// layout answers one Layout. Once the store holds its first read, it
// holds every Layout, so a name it does not hold is a Layout that does
// not exist, and the read costs the API server nothing.
func (c clusterStores) layout(client *Client, name string) (*Layout, error) {
	if held, ok := cachedCopy[Layout](c.layouts, name); ok {
		return held, nil
	}
	if c.layouts.ready() {
		return nil, ErrNotFound
	}
	return getLayout(client, name)
}

// pod answers one pod, from the store when it holds it. The store
// holds this node's pods, so a pod on another node is read from the API
// server.
func (c clusterStores) pod(client *Client, namespace, name string) (*Pod, error) {
	if held, ok := cachedCopy[Pod](c.pods, namespace+"/"+name); ok {
		return held, nil
	}
	return getPod(client, namespace, name)
}

// podsOn answers the pods on this node, from the store once it holds
// its first read.
func (c clusterStores) podsOn(client *Client, node string) ([]Pod, error) {
	if c.pods.ready() {
		return cachedList[Pod](c.pods), nil
	}
	return listPods(client, node)
}
