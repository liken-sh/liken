package main

// The reads and writes the pass makes of this machine's Sinks and
// Sources, through the watches' stores and memos (objectcache.go).

import "github.com/liken-sh/liken/kubernetes/informer"

// readSinks answers this machine's Sinks, from the store once it holds
// its first read.
func (e *endpointControl) readSinks() ([]Sink, error) {
	if e.cache.sinks.View.Ready() {
		return informer.CurrentList[Sink](e.client, e.cache.sinks, sinkPath)
	}
	return listSinks(e.client, e.machine)
}

func (e *endpointControl) readSources() ([]Source, error) {
	if e.cache.sources.View.Ready() {
		return informer.CurrentList[Source](e.client, e.cache.sources, sourcePath)
	}
	return listSources(e.client, e.machine)
}

// createSink creates one Sink and notes the version the API server
// stored.
func (e *endpointControl) createSink(name string) (*Sink, error) {
	var created *Sink
	err := e.cache.sinks.Versions.Send(name, func() (string, error) {
		var err error
		if created, err = createSink(e.client, name); err != nil {
			return "", err
		}
		return created.Metadata.ResourceVersion, nil
	})
	return created, err
}

func (e *endpointControl) createSource(name string) (*Source, error) {
	var created *Source
	err := e.cache.sources.Versions.Send(name, func() (string, error) {
		var err error
		if created, err = createSource(e.client, name); err != nil {
			return "", err
		}
		return created.Metadata.ResourceVersion, nil
	})
	return created, err
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
// answers apiclient.ErrNotFound.
func (e *endpointControl) settleSinkStatus(sink *Sink, want compose) error {
	_, err := informer.SettleStatus(e.client, e.cache.sinks.Versions, sinkPath(sink.Metadata.Name), sink, func(held *Sink) bool {
		status, ours := want(held.Status)
		if !ours || sameStatus(held.Status, status) {
			return false
		}
		held.APIVersion, held.Kind, held.Status = EndpointAPIVersion, SinkKind, status
		return true
	})
	return err
}

func (e *endpointControl) settleSourceStatus(source *Source, want compose) error {
	_, err := informer.SettleStatus(e.client, e.cache.sources.Versions, sourcePath(source.Metadata.Name), source, func(held *Source) bool {
		status, ours := want(held.Status)
		if !ours || sameStatus(held.Status, status) {
			return false
		}
		held.APIVersion, held.Kind, held.Status = EndpointAPIVersion, SourceKind, status
		return true
	})
	return err
}
