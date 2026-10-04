package main

// The reads and writes the pass makes of this machine's Sinks and
// Sources, through the watches' stores and memos (objectcache.go).

import "github.com/liken-sh/liken/kubernetes/informer"

// readSinks answers this machine's Sinks, from the store once it holds
// its first read, and from the API server by the watch's field selector
// before then (informer.List).
func (e *endpointControl) readSinks() ([]Sink, error) {
	return informer.List[Sink](e.client, e.cache.sinks, byMachine(SinksPath, e.machine), sinkPath)
}

func (e *endpointControl) readSources() ([]Source, error) {
	return informer.List[Source](e.client, e.cache.sources, byMachine(SourcesPath, e.machine), sourcePath)
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
// published one, when it differs. The write is a merge patch of this
// operator's fields alone, so it leaves the media operator's
// status.session in place (statuspatch.go). A conflict or a missing
// resource reads the Sink from the API server and composes the status
// again from what it holds, so a resource that another machine took in
// the meantime is left alone. A Sink the API server no longer holds
// answers apiclient.ErrNotFound.
func (e *endpointControl) settleSinkStatus(sink *Sink, want compose) error {
	_, err := patchSinkStatus(e.client, e.cache.sinks.Versions, sink, func(published EndpointStatus) (EndpointStatus, bool) {
		status, ours := want(published)
		return status, ours && !sameStatus(published, status)
	})
	return err
}

// settleSourceStatus writes a Source's status the way settleSinkStatus
// writes a Sink's. A Source has one writer, so the write replaces the
// whole status.
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
