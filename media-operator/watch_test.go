package main

// These tests run the operator's watches on client-go's reflector
// against a scripted API server, inside a synctest bubble.
// synctest.Wait returns once the reflector has processed every event
// the server sent, so a test reads the result without waiting on the
// real clock. The reflector's own retries are upstream's to test.
// optionalwatch_test.go covers a collection that the cluster does not
// have.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/informer"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

// closeStream, sent as a line, ends the open watch the way the API
// server does when the watch times out.
const closeStream = "close"

// servedCollection is one collection on the scripted server: the
// objects that list requests return, a status code that replaces the
// response when status is not zero, and the lines the test sends to the
// open watch.
type servedCollection struct {
	apiVersion string
	kind       string
	items      []json.RawMessage
	status     int
	live       chan string
	// missed holds the lines of changes made while no watch was open. A
	// watch that resumes from a resourceVersion receives them first. A
	// watch with no resourceVersion does not, as on the API server.
	missed []string
	// forbidWatch returns 403 for every watch and still serves list and
	// get, as the API server does under RBAC that grants list and get but
	// not watch.
	forbidWatch bool
}

// collectionServer serves list and watch requests for each of its
// collections as the API server does. A streaming list sends one ADDED
// event per object and then the bookmark that ends the initial events.
// A plain list serves the reflector when it falls back to one. Then the
// test's lines go out on the open watch.
type collectionServer struct {
	mu          sync.Mutex
	collections map[string]*servedCollection
	requests    []string
	// objectReadCount counts get requests for a single object, whose
	// paths match no collection.
	objectReadCount int
}

// objectReads returns how many get requests for a single object the
// server received.
func (s *collectionServer) objectReads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.objectReadCount
}

func newCollectionServer() *collectionServer {
	return &collectionServer{collections: map[string]*servedCollection{}}
}

// serve adds one collection with its objects.
func (s *collectionServer) serve(t *testing.T, resource schema.GroupVersionResource, kind string, objects ...any) *servedCollection {
	t.Helper()
	collection := &servedCollection{
		apiVersion: resource.GroupVersion().String(),
		kind:       kind,
		live:       make(chan string, 16),
	}
	for _, object := range objects {
		collection.items = append(collection.items, stamped(t, collection, object))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.collections[collectionPath(resource)] = collection
	return collection
}

// stamped encodes an object with the collection's apiVersion and kind,
// because client-go's decoder requires them.
func stamped(t *testing.T, collection *servedCollection, object any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(object)
	mustSucceed(t, err)
	fields := map[string]any{}
	mustSucceed(t, json.Unmarshal(encoded, &fields))
	fields["apiVersion"] = collection.apiVersion
	fields["kind"] = collection.kind
	encoded, err = json.Marshal(fields)
	mustSucceed(t, err)
	return encoded
}

// send writes one event to the collection's open watch.
func (c *servedCollection) send(t *testing.T, eventType string, object any) {
	t.Helper()
	c.live <- watchLine(eventType, stamped(t, c, object))
}

func (s *collectionServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.URL.Path+"?"+r.URL.RawQuery)
		collection, held := s.collections[r.URL.Path]
		s.mu.Unlock()
		if !held {
			s.serveObject(w, r)
			return
		}
		if collection.status != 0 {
			w.WriteHeader(collection.status)
			return
		}
		if collection.forbidWatch && r.URL.Query().Get("watch") == "true" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		query := r.URL.Query()
		if query.Get("watch") != "true" {
			list, _ := json.Marshal(map[string]any{
				"apiVersion": collection.apiVersion,
				"kind":       collection.kind + "List",
				"metadata":   map[string]string{"resourceVersion": "100"},
				"items":      collection.items,
			})
			_, _ = w.Write(list)
			return
		}
		// A streaming list sends every object and then the bookmark. A
		// watch with no resourceVersion also sends every object first,
		// because the API server starts it at the most recent version.
		if query.Get("sendInitialEvents") == "true" || query.Get("resourceVersion") == "" {
			for _, item := range collection.items {
				_, _ = w.Write([]byte(watchLine("ADDED", item) + "\n"))
			}
		}
		if query.Get("sendInitialEvents") == "true" {
			_, _ = w.Write([]byte(initialEventsEnd(collection.apiVersion, collection.kind, "100") + "\n"))
		}
		// A watch from a resourceVersion sends every change after that
		// version. A watch with no resourceVersion, and a streaming list,
		// start at the present and send none of the missed changes.
		s.mu.Lock()
		missed := collection.missed
		collection.missed = nil
		s.mu.Unlock()
		if query.Get("resourceVersion") != "" && query.Get("sendInitialEvents") != "true" {
			for _, line := range missed {
				_, _ = w.Write([]byte(line + "\n"))
			}
		}
		w.(http.Flusher).Flush()
		for {
			select {
			case line := <-collection.live:
				if line == closeStream {
					return
				}
				_, _ = w.Write([]byte(line + "\n"))
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	})
}

// serveObject serves a get of one object at its namespaced or
// cluster-scoped path, from the collection that holds it.
func (s *collectionServer) serveObject(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objectReadCount++
	for path, collection := range s.collections {
		if collection.status != 0 {
			continue
		}
		for _, item := range collection.items {
			var object unstructured.Unstructured
			if object.UnmarshalJSON(item) != nil {
				continue
			}
			resource := resourceOf(collection.apiVersion, strings.TrimPrefix(path[strings.LastIndex(path, "/"):], "/"))
			key := object.GetName()
			if object.GetNamespace() != "" {
				key = object.GetNamespace() + "/" + key
			}
			if objectPath(resource, key) == r.URL.Path {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(item)
				return
			}
		}
	}
	http.NotFound(w, r)
}

// requested returns every request the server received for one
// collection.
func (s *collectionServer) requested(resource schema.GroupVersionResource) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found []string
	for _, request := range s.requests {
		if strings.HasPrefix(request, collectionPath(resource)+"?") {
			found = append(found, request)
		}
	}
	return found
}

// runWatch runs one collection watch until the test ends. The watch
// must stop before the server closes, because a reflector that finds
// the server closed sleeps in a backoff that ignores its context.
func runWatch(t *testing.T, server *collectionServer, watch collectionWatch) {
	t.Helper()
	client := testWatcher(t, server.handler())
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() {
		stop()
		<-done
	})
	go func() {
		defer close(done)
		watchCollection(ctx, client, watch)
	}()
}

// received reports whether the channel has a value or is closed, and
// consumes the value. Call it after synctest.Wait, when every goroutine
// that could send has either sent or is blocked on something else.
func received(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

// asObject returns an object in the form the informer passes to a
// handler.
func asObject(t *testing.T, object any) *unstructured.Unstructured {
	t.Helper()
	encoded, err := json.Marshal(object)
	mustSucceed(t, err)
	fields := map[string]any{}
	mustSucceed(t, json.Unmarshal(encoded, &fields))
	return &unstructured.Unstructured{Object: fields}
}

func labeledPod(name, component, phase string) *Pod {
	return &Pod{
		Metadata: ObjectMeta{Name: name, Namespace: "house", Labels: map[string]string{playbackLabelKey: component}},
		Status:   PodStatus{Phase: phase},
	}
}

// The first list and every change to a Play wake the pass, including
// the operator's own status writes. So the pass acts on a Play that
// has just Finished without waiting for another change.
func TestAChangeToAPlayWakesThePass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newCollectionServer()
		plays := server.serve(t, playResource, "Play", housePlay("https://nas/film.mkv"))
		wake := make(chan struct{}, 1)
		synced := make(chan struct{})

		runWatch(t, server, collectionWatch{resource: playResource, handler: wakeOnChange(wake),
			synced: func(informer.View) { close(synced) }})
		synctest.Wait()
		mustMatch(t, received(synced), true)
		mustMatch(t, received(wake), true)

		written := housePlay("https://nas/film.mkv")
		written.Metadata.ResourceVersion = "101"
		written.Status.Phase = phaseRunning
		plays.send(t, "MODIFIED", written)
		synctest.Wait()
		mustMatch(t, received(wake), true)

		plays.send(t, "DELETED", written)
		synctest.Wait()
		mustMatch(t, received(wake), true)
	})
}

// Deleting any of the operator's pods wakes the pass. A change to a
// playback pod's status wakes it too, and so does an idle or reader pod
// that the kubelet ended. No other pod change does.
func TestAPodWakesThePassWhenItEndsOrAPlaybackPodMoves(t *testing.T) {
	restarted := labeledPod("movie-playback", playbackLabelValue, podRunning)
	restarted.Status.ContainerStatuses = []ContainerStatus{{Name: displayContainer, RestartCount: 3}}
	ending := labeledPod("movie-playback", playbackLabelValue, podRunning)
	ending.Metadata.Labels[endingLabelKey] = endingLabelValue

	cases := []struct {
		name   string
		before *Pod
		after  *Pod
		want   bool
	}{
		{name: "a playback pod starts running", before: labeledPod("movie-playback", playbackLabelValue, podPending),
			after: labeledPod("movie-playback", playbackLabelValue, podRunning), want: true},
		{name: "a playback pod succeeds", before: labeledPod("movie-playback", playbackLabelValue, podRunning),
			after: labeledPod("movie-playback", playbackLabelValue, podSucceeded), want: true},
		{name: "a playback pod fails", before: labeledPod("movie-playback", playbackLabelValue, podRunning),
			after: labeledPod("movie-playback", playbackLabelValue, podFailed), want: true},
		{name: "a playback pod's display restarts", before: labeledPod("movie-playback", playbackLabelValue, podRunning),
			after: restarted, want: true},
		{name: "this operator labels the ending", before: labeledPod("movie-playback", playbackLabelValue, podRunning),
			after: ending, want: false},
		{name: "a failed playback pod the watch had not seen",
			after: labeledPod("movie-playback", playbackLabelValue, podFailed), want: true},
		{name: "a finished playback pod the watch had not seen",
			after: labeledPod("movie-playback", playbackLabelValue, podSucceeded), want: true},
		{name: "this operator creates a playback pod",
			after: labeledPod("movie-playback", playbackLabelValue, podPending), want: false},
		{name: "an idle pod starts running", before: labeledPod("theater-idle", idleLabelValue, podPending),
			after: labeledPod("theater-idle", idleLabelValue, podRunning), want: false},
		{name: "an idle pod fails", before: labeledPod("theater-idle", idleLabelValue, podRunning),
			after: labeledPod("theater-idle", idleLabelValue, podFailed), want: true},
		{name: "a reader pod fails", before: labeledPod("sofa-remote", remoteLabelValue, podRunning),
			after: labeledPod("sofa-remote", remoteLabelValue, podFailed), want: true},
		{name: "an idle pod succeeds", before: labeledPod("theater-idle", idleLabelValue, podRunning),
			after: labeledPod("theater-idle", idleLabelValue, podSucceeded), want: true},
		{name: "a succeeded reader pod the watch had not seen",
			after: labeledPod("sofa-remote", remoteLabelValue, podSucceeded), want: true},
		{name: "a failed reader pod the watch had not seen",
			after: labeledPod("sofa-remote", remoteLabelValue, podFailed), want: true},
		{name: "this operator creates a reader pod",
			after: labeledPod("sofa-remote", remoteLabelValue, podPending), want: false},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			wake := make(chan struct{}, 1)

			handChange(t, podRule.handler(wake), each.before, each.after)

			mustMatch(t, len(wake) == 1, each.want)
		})
	}
}

// The pods watch selects only this operator's pods, so deleting any of
// them wakes the pass.
func TestAPodRemovalWakesThePass(t *testing.T) {
	for _, component := range []string{playbackLabelValue, idleLabelValue, remoteLabelValue} {
		t.Run(component, func(t *testing.T) {
			wake := make(chan struct{}, 1)

			podRule.handler(wake).OnDelete(asObject(t, labeledPod("gone", component, podRunning)))

			mustMatch(t, len(wake), 1)
		})
	}
}

// A delete event whose tombstone has no copy of the pod wakes the pass,
// because every pod the watch selects belongs to this operator.
func TestAPodRemovedWithNoCopyWakesThePass(t *testing.T) {
	wake := make(chan struct{}, 1)

	podRule.handler(wake).OnDelete(cache.DeletedFinalStateUnknown{Key: "house/movie-playback"})

	mustMatch(t, len(wake), 1)
}

// Through the reflector, the pods watch selects the operator's pods by
// their component label, and a playback pod that fails wakes the pass.
func TestThePodsWatchSelectsTheOperatorsOwnPods(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newCollectionServer()
		pods := server.serve(t, podResource, "Pod", labeledPod("movie-playback", playbackLabelValue, podRunning))
		wake := make(chan struct{}, 1)
		synced := make(chan struct{})

		runWatch(t, server, collectionWatch{resource: podResource, labels: ownPodsSelector,
			handler: podRule.handler(wake), synced: func(informer.View) { close(synced) }})
		synctest.Wait()
		mustMatch(t, received(synced), true)
		// The running playback pod in the first list wakes the pass once.
		mustMatch(t, received(wake), true)
		pods.send(t, "MODIFIED", labeledPod("movie-playback", playbackLabelValue, podFailed))
		synctest.Wait()

		mustMatch(t, received(wake), true)
		for _, request := range server.requested(podResource) {
			mustMatch(t, strings.Contains(request, "labelSelector=media.liken.sh%2Fcomponent+in+%28playback%2Cidle%2Cremote%29"), true)
		}
	})
}

// Each watch the API server accepts after the first calls reopened
// once.
func TestReopenedRunsOnceForEachLaterWatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newCollectionServer()
		plays := server.serve(t, playResource, "Play")
		var reopened atomic.Int32
		synced := make(chan struct{})

		runWatch(t, server, collectionWatch{resource: playResource, reopened: func() { reopened.Add(1) },
			synced: func(informer.View) { close(synced) }})
		synctest.Wait()
		mustMatch(t, received(synced), true)
		plays.send(t, "ADDED", housePlay("https://nas/film.mkv"))
		plays.live <- closeStream
		// The reflector's backoff before the next watch is under a minute.
		time.Sleep(time.Minute)
		synctest.Wait()

		mustMatch(t, reopened.Load(), int32(1))
	})
}

// servedCluster serves every collection the view reads, with the
// objects of the fake cluster.
func servedCluster(t *testing.T, cluster *fakeCluster) *collectionServer {
	t.Helper()
	server := newCollectionServer()
	view := cluster.view()
	listed := func(source objectSource) []any {
		items, err := source.List()
		mustSucceed(t, err)
		return items
	}
	collections := []struct {
		resource schema.GroupVersionResource
		kind     string
		items    []any
	}{
		{playResource, "Play", view.plays.View.Store.List()},
		{playerResource, "Player", view.players.View.Store.List()},
		{remoteResource, "Remote", listed(view.remotes)},
		{keymapResource, "Keymap", listed(view.keymaps)},
		{preferencesResource, "MediaPreferences", listed(view.preferences)},
		{peripheralResource, "Peripheral", listed(view.peripherals)},
		{podResource, "Pod", listed(view.pods)},
		{claimResource, "ResourceClaim", listed(view.claims)},
		{sliceResource, "ResourceSlice", listed(view.slices)},
		{displayResource, "Display", listed(view.displays)},
		{receiverResource, "Receiver", listed(view.receivers)},
	}
	for _, each := range collections {
		server.serve(t, each.resource, each.kind, each.items...)
	}
	return server
}

// watchedView runs watchCluster against the server until the test ends
// and returns its view. The caller must run in a synctest bubble. The
// watches stop before the server closes, for the reason runWatch gives.
func watchedView(t *testing.T, server *collectionServer, wake chan struct{}) *clusterView {
	t.Helper()
	watcher, reader := testWatcher(t, server.handler()), viewClient(t, server)
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(func() {
		stop()
		synctest.Wait()
	})
	view, err := watchCluster(ctx, ctx, watcher, reader, wake, nil)
	mustSucceed(t, err)
	return view
}

// viewClient returns the operator's own client, pointed at the scripted
// server, for the reads that the view sends directly.
func viewClient(t *testing.T, server *collectionServer) *apiclient.Client {
	t.Helper()
	return apiclient.New(apiservertest.Host, apiservertest.Start(t, server.handler()).Client(), "")
}

// settledHouse returns a cluster that the operator has already
// reconciled, and that operator. The cluster has a Player with a screen
// wired through a Receiver, a Remote with its controller bonded, and a
// Play running on the Player. Each has the claims and pods that the
// passes created, with the claims allocated.
func settledHouse(t *testing.T, wake chan struct{}) (*fakeCluster, *operator) {
	t.Helper()
	cluster := receiverCluster()
	running := runningCluster(housePlayerWithRemote())
	bonded := bondedRemote(t)
	for name, play := range running.plays {
		cluster.plays[name] = play
	}
	for name, pod := range running.pods {
		cluster.pods[name] = pod
	}
	for _, from := range []*fakeCluster{running, bonded} {
		for name, claim := range from.claims {
			cluster.claims[name] = claim
		}
	}
	for name, remote := range bonded.remotes {
		cluster.remotes[name] = remote
	}
	for name, keymap := range bonded.keymaps {
		cluster.keymaps[name] = keymap
	}
	for name, peripheral := range bonded.peripherals {
		cluster.peripherals[name] = peripheral
	}
	cluster.players["theater"] = housePlayerWithRemote()
	media := testOperator(t, cluster, wake)
	media.idleDisplayClass = "display-draw"
	for range 4 {
		media.pass()
	}
	// The fake cluster has no scheduler, so the test allocates the
	// claims that the passes created.
	cluster.claims[idleClaimName("theater")].Status = allocatedIdleClaim().Status
	cluster.claims[remoteClaimName("sofa")].Status = bonded.claims[remoteClaimName("sofa")].Status
	for range 2 {
		media.pass()
	}
	return cluster, media
}

// A pass on a settled cluster reads every collection from the watches
// and sends the API server nothing: no read and no write.
func TestASettledPassSendsTheAPIServerNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wake := make(chan struct{}, 8)
		cluster, media := settledHouse(t, wake)
		media.view = watchedView(t, servedCluster(t, cluster), wake)
		cluster.requests = nil

		media.pass()

		mustMatchAll(t, cluster.requests, nil)
	})
}

// When the operator cannot read a collection, the first read fails with
// an error that names the collection, so the log shows which RBAC grant
// or resource is missing.
func TestTheFirstReadNamesACollectionItCannotRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := servedCluster(t, newFakeCluster())
		server.collections[collectionPath(peripheralResource)].status = http.StatusForbidden
		watcher, reader := testWatcher(t, server.handler()), viewClient(t, server)
		ctx, stop := context.WithCancel(context.Background())
		t.Cleanup(func() {
			stop()
			synctest.Wait()
		})
		wait, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		_, err := watchCluster(ctx, wait, watcher, reader, make(chan struct{}, 1), nil)

		mustFail(t, err)
		mustMatch(t, strings.Contains(err.Error(), "not read: "+kindPeripheral+":"), true)
	})
}

// An object that cannot be converted to its type is an error that names
// the object, so the log shows which object the operator could not
// read.
func TestAnObjectThatDoesNotConvertIsAnErrorThatNamesIt(t *testing.T) {
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": mediaAPIVersion, "kind": "Play",
		"metadata": map[string]any{"name": "movie", "namespace": "house"},
		"spec":     "garbled",
	}}

	_, err := informer.Convert[Play](object)

	mustFail(t, err)
	mustMatch(t, strings.Contains(err.Error(), "Play house/movie does not convert"), true)
}
