package main

// objectcache.go explains when a read comes from a watch store and when
// it goes to the API server.

import "github.com/liken-sh/liken/kubernetes/informer"

// readSinks returns the Sinks whose status.node is this machine, from
// the watch store once the watch has finished its first list, and from
// the API server with the watch's selector before that.
func (e *endpointControl) readSinks() ([]Sink, error) {
	return informer.List[Sink](e.client, e.cache.sinks, byMachine(SinksPath, e.machine), sinkPath)
}

func (e *endpointControl) readSources() ([]Source, error) {
	return informer.List[Source](e.client, e.cache.sources, byMachine(SourcesPath, e.machine), sourcePath)
}

// createSink records the resourceVersion of the created Sink in the
// memo, so a later read does not take an older copy from the store.
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

// compose returns the status to write, given the status on the
// operator's copy of the resource. It returns false when another
// machine owns the resource.
type compose func(published EndpointStatus) (EndpointStatus, bool)

// settleSinkStatus patches the Sink's status to the one that want
// composes, and skips the write when nothing changed. The merge patch
// names only this operator's fields, so it leaves status.session, which
// media-operator writes, in place (statuspatch.go). After a 409 or a
// 404, it reads the Sink from the API server and calls want again on
// that copy, so a Sink that another machine took in the meantime is not
// written. It returns apiclient.ErrNotFound when the Sink has been
// deleted.
//
// A successful write posts an Event for each condition that changed
// (endpointevents.go). want can run more than once, so the Events come
// from the last status it composed, which is the one the API server
// accepted.
func (e *endpointControl) settleSinkStatus(sink *Sink, want compose) error {
	var published, written EndpointStatus
	wrote, err := patchSinkStatus(e.client, e.cache.sinks.Versions, sink, func(current EndpointStatus) (EndpointStatus, bool) {
		status, ours := want(current)
		published, written = current, status
		return status, ours && !sameStatus(current, status)
	})
	if wrote && err == nil {
		e.postTransitions(endpointReference(SinkKind, sink.Metadata.Name, sink.Metadata.UID), published, written)
	}
	return err
}

// settleSourceStatus does for a Source what settleSinkStatus does for a
// Sink, except that it replaces the whole status, because this operator
// is the only writer of a Source's status.
func (e *endpointControl) settleSourceStatus(source *Source, want compose) error {
	var published, written EndpointStatus
	wrote, err := informer.SettleStatus(e.client, e.cache.sources.Versions, sourcePath(source.Metadata.Name), source, func(current *Source) bool {
		status, ours := want(current.Status)
		if !ours || sameStatus(current.Status, status) {
			return false
		}
		published, written = current.Status, status
		current.APIVersion, current.Kind, current.Status = EndpointAPIVersion, SourceKind, status
		return true
	})
	if wrote && err == nil {
		e.postTransitions(endpointReference(SourceKind, source.Metadata.Name, source.Metadata.UID), published, written)
	}
	return err
}
