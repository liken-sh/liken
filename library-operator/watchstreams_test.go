package main

// The watch half of the fake cluster. A watch request holds a stream
// open, the way the API server does, and the stream carries every change
// to its collection: the changes a pass writes, and the changes a test
// makes by hand and then announces. A watch that asks for the initial
// events first answers the collection as ADDED events and the bookmark
// that ends them, which is the streaming list client-go's reflector
// opens with.

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
)

// The collections the operator watches, with the apiVersion and the
// kind the API server writes on each object of one. The fake holds its
// objects as the operator's structs, which leave both empty.
var watchedKinds = map[string]struct{ apiVersion, kind string }{
	librariesPath:         {libraryAPIVersion, "Library"},
	catalogsPath:          {libraryAPIVersion, "Catalog"},
	metadataProvidersPath: {metadataProviderAPIVersion, "MetadataProvider"},
	playersPath:           {playerAPIVersion, "Player"},
	mediaPreferencesPath:  {playerAPIVersion, "MediaPreferences"},
	playsAllPath:          {playerAPIVersion, "Play"},
	peoplePath:            {personAPIVersion, "Person"},
	podsAllPath:           {podAPIVersion, "Pod"},
	claimsAllPath:         {"v1", "PersistentVolumeClaim"},
	volumesPath:           {"v1", "PersistentVolume"},
	jobsAllPath:           {batchAPIVersion, "Job"},
	nodesPath:             {"v1", "Node"},
	servicesAllPath:       {"v1", "Service"},
	endpointSlicesAllPath: {endpointSliceAPIVersion, "EndpointSlice"},
	configMapsAllPath:     {"v1", "ConfigMap"},
	claimTemplatesAllPath: {deviceAPIVersion, "ResourceClaimTemplate"},
}

// The cluster-wide lists of the three kinds the operator stands one of by
// name in each namespace, which only its watches read.
const (
	servicesAllPath       = "/api/v1/services"
	endpointSlicesAllPath = "/apis/" + endpointSliceAPIVersion + "/endpointslices"
	configMapsAllPath     = "/api/v1/configmaps"
	claimTemplatesAllPath = "/apis/" + deviceAPIVersion + "/resourceclaimtemplates"
)

// One open watch: the collection and the selector it asked for, and the
// objects it has been sent, by namespace and name.
type watchStream struct {
	collection string
	selector   string
	seen       map[string]string
	events     chan string
	done       chan struct{}
	// cut closes when a test ends the stream, the way the API server
	// ends a watch at its timeout.
	cut chan struct{}
}

func (f *fakeCluster) serveWatch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	f.mutex.Lock()
	if status := f.brokenStatus(r); status != 0 {
		f.mutex.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte("the API server is unwell"))
		return
	}
	kind, watched := watchedKinds[r.URL.Path]
	if !watched {
		f.mutex.Unlock()
		w.WriteHeader(http.StatusNotFound)
		return
	}
	stream := &watchStream{
		collection: r.URL.Path,
		selector:   query.Get("labelSelector"),
		events:     make(chan string, 256),
		done:       make(chan struct{}),
		cut:        make(chan struct{}),
	}
	// A watch that does not ask for the initial events starts from the
	// list the reflector read just before it.
	stream.seen = f.collectionLocked(stream.collection, stream.selector)
	var lines []string
	if query.Get("sendInitialEvents") == "true" {
		for _, key := range slices.Sorted(maps.Keys(stream.seen)) {
			lines = append(lines, watchEventLine("ADDED", stream.seen[key]))
		}
		lines = append(lines, fmt.Sprintf(`{"type":"BOOKMARK","object":{"apiVersion":%q,"kind":%q,`+
			`"metadata":{"resourceVersion":"1","annotations":{"k8s.io/initial-events-end":"true"}}}}`,
			kind.apiVersion, kind.kind))
	}
	f.streams = append(f.streams, stream)
	f.mutex.Unlock()
	defer f.dropStream(stream)

	w.Header().Set("Content-Type", "application/json")
	for _, line := range lines {
		_, _ = io.WriteString(w, line+"\n")
	}
	w.(http.Flusher).Flush()
	for {
		select {
		case line := <-stream.events:
			_, _ = io.WriteString(w, line+"\n")
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		case <-stream.cut:
			return
		}
	}
}

// cutStreams ends every open stream, the way the API server ends a
// watch at its timeout.
func (f *fakeCluster) cutStreams() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	for _, stream := range f.streams {
		close(stream.cut)
	}
	f.streams = nil
}

func (f *fakeCluster) dropStream(stream *watchStream) {
	close(stream.done)
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.streams = slices.DeleteFunc(f.streams, func(held *watchStream) bool { return held == stream })
}

// collectionLocked answers every object one list of the collection
// holds, by namespace and name, each as the JSON the API server sends.
// The caller holds the mutex.
func (f *fakeCluster) collectionLocked(collection, selector string) map[string]string {
	recorder := httptest.NewRecorder()
	f.route(recorder, httptest.NewRequest(http.MethodGet,
		collection+"?labelSelector="+url.QueryEscape(selector), nil))
	var list struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &list)
	kind := watchedKinds[collection]
	objects := map[string]string{}
	for _, item := range list.Items {
		item["apiVersion"], item["kind"] = kind.apiVersion, kind.kind
		metadata, _ := item["metadata"].(map[string]any)
		namespace, _ := metadata["namespace"].(string)
		name, _ := metadata["name"].(string)
		encoded, _ := json.Marshal(item)
		objects[namespace+"/"+name] = string(encoded)
	}
	return objects
}

// announceLocked sends each open stream the changes to its collection
// since the stream last looked. The caller holds the mutex.
func (f *fakeCluster) announceLocked() {
	for _, stream := range f.streams {
		now := f.collectionLocked(stream.collection, stream.selector)
		var lines []string
		for _, key := range slices.Sorted(maps.Keys(now)) {
			was, held := stream.seen[key]
			switch {
			case !held:
				lines = append(lines, watchEventLine("ADDED", now[key]))
			case was != now[key]:
				lines = append(lines, watchEventLine("MODIFIED", now[key]))
			}
		}
		for _, key := range slices.Sorted(maps.Keys(stream.seen)) {
			if _, held := now[key]; !held {
				lines = append(lines, watchEventLine("DELETED", stream.seen[key]))
			}
		}
		stream.seen = now
		for _, line := range lines {
			select {
			case stream.events <- line:
			case <-stream.done:
			}
		}
	}
}

// announce sends the open streams a change a test made by hand.
func (f *fakeCluster) announce() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.announceLocked()
}

// watching answers how many watch streams the cluster holds open on one
// collection.
func (f *fakeCluster) watching(collection string) int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	count := 0
	for _, stream := range f.streams {
		if stream.collection == collection {
			count++
		}
	}
	return count
}

func watchEventLine(kind, object string) string {
	return `{"type":"` + kind + `","object":` + strings.TrimSpace(object) + `}`
}
