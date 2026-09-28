package main

// The pass reads this machine's Sinks and Sources from the two watches'
// stores, not from the API server. The stores hold every resource whose
// status.node is this machine, the same selection the pass lists, so a
// settled pass sends the API server no read at all.
//
// A resource the store does not hold is read from the API server. That
// is a resource no machine has written status.node on yet, one whose
// status.node names another machine, such as a Bluetooth speaker that
// moved here, or any resource while the watch has not finished its
// first read. The sweep lists from the API server until both stores
// hold their first read, because a store that holds part of it would
// leave a resource out of the sweep.
//
// A copy in the store can be older than this operator's own last status
// write, because the watch delivers the write a moment after the API
// server answers it. A status write from that older copy carries an
// older resourceVersion, and the API server answers 409 Conflict. The
// write then reads the resource from the API server and writes once
// more, if the status still differs and the resource is still this
// machine's to write. A copy of a resource somebody deleted answers
// 404, and the pass creates the resource again.

import (
	"errors"

	"k8s.io/client-go/tools/cache"
)

// endpointCache is the two stores and whether both hold their first
// read. The zero value holds nothing, and every read goes to the API
// server.
type endpointCache struct {
	sinks, sources cache.Store
	synced         func() bool
}

func (c endpointCache) ready() bool { return c.synced != nil && c.synced() }

// cachedCopy answers the store's copy of one cluster-scoped resource.
// A copy that does not convert is logged and not answered, so the
// caller reads the resource from the API server.
func cachedCopy[T any](store cache.Store, name string) (*T, bool) {
	if store == nil {
		return nil, false
	}
	object, held, err := store.GetByKey(name)
	if err != nil || !held {
		return nil, false
	}
	item, err := convert[T](object)
	if err != nil {
		reportUnconverted("the cached "+name, err)
		return nil, false
	}
	return &item, true
}

// cachedList answers every copy in the store.
func cachedList[T any](store cache.Store) []T {
	var items []T
	for _, object := range store.List() {
		item, err := convert[T](object)
		if err != nil {
			reportUnconverted("the cached resources", err)
			continue
		}
		items = append(items, item)
	}
	return items
}

// readSink answers one Sink, from the store when it holds it.
func (e *endpointControl) readSink(name string) (*Sink, error) {
	if held, ok := cachedCopy[Sink](e.cache.sinks, name); ok {
		return held, nil
	}
	return getSink(e.client, name)
}

func (e *endpointControl) readSource(name string) (*Source, error) {
	if held, ok := cachedCopy[Source](e.cache.sources, name); ok {
		return held, nil
	}
	return getSource(e.client, name)
}

// readSinks answers this machine's Sinks, from the store once it holds
// its first read.
func (e *endpointControl) readSinks() ([]Sink, error) {
	if e.cache.ready() {
		return cachedList[Sink](e.cache.sinks), nil
	}
	return listSinks(e.client, e.machine)
}

func (e *endpointControl) readSources() ([]Source, error) {
	if e.cache.ready() {
		return cachedList[Source](e.cache.sources), nil
	}
	return listSources(e.client, e.machine)
}

// compose answers the status a write should publish, given the
// published one, and false when this machine must leave the resource
// alone.
type compose func(published EndpointStatus) (EndpointStatus, bool)

// settleSinkStatus writes the status that want composes from the
// published one, when it differs. A conflict or a missing resource
// reads the Sink from the API server and composes the status again
// from what it holds, so a resource that another machine took in the
// meantime is left alone. A Sink the API server no longer holds
// answers ErrNotFound.
func (e *endpointControl) settleSinkStatus(sink *Sink, want compose) error {
	status, ours := want(sink.Status)
	if !ours || sameStatus(sink.Status, status) {
		return nil
	}
	_, err := writeSinkStatus(e.client, sink, status)
	if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
		return err
	}
	current, err := getSink(e.client, sink.Metadata.Name)
	if err != nil {
		return err
	}
	status, ours = want(current.Status)
	if !ours || sameStatus(current.Status, status) {
		return nil
	}
	_, err = writeSinkStatus(e.client, current, status)
	return err
}

func (e *endpointControl) settleSourceStatus(source *Source, want compose) error {
	status, ours := want(source.Status)
	if !ours || sameStatus(source.Status, status) {
		return nil
	}
	_, err := writeSourceStatus(e.client, source, status)
	if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
		return err
	}
	current, err := getSource(e.client, source.Metadata.Name)
	if err != nil {
		return err
	}
	status, ours = want(current.Status)
	if !ours || sameStatus(current.Status, status) {
		return nil
	}
	_, err = writeSourceStatus(e.client, current, status)
	return err
}
