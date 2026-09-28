package main

// These tests run the operator's watches through client-go's real
// reflector, against a scripted API server. The reflector's own loop is
// upstream's to test; what these prove is which changes wake the pass,
// that every collection reaches the view before the first pass, that
// an optional collection a cluster lacks reads as empty, and that a
// settled pass reads nothing from the API server.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

// closeStream, sent as a line, ends the watch the way the API server
// ends one at its timeout.
const closeStream = "close"

// servedCollection is one collection the scripted server holds: the
// objects its reads answer, the status it answers instead when status
// is not zero, and the lines the test sends to the open watch.
type servedCollection struct {
	apiVersion string
	kind       string
	items      []json.RawMessage
	status     int
	live       chan string
}

// collectionServer answers client-go's reads and watches of each
// collection it holds, the way the API server does: a streaming list
// with one ADDED event for each object and the bookmark that ends the
// initial events, a plain list when the reflector falls back to one,
// and the test's lines on the open watch after that.
type collectionServer struct {
	mu          sync.Mutex
	collections map[string]*servedCollection
	requests    []string
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
	s.collections[collectionPathOf(resource)] = collection
	return collection
}

// collectionPathOf is the path the dynamic client reads a collection
// in every namespace from.
func collectionPathOf(resource schema.GroupVersionResource) string {
	if resource.Group == "" {
		return "/api/" + resource.Version + "/" + resource.Resource
	}
	return "/apis/" + resource.Group + "/" + resource.Version + "/" + resource.Resource
}

// stamped encodes an object with its collection's apiVersion and kind,
// which client-go's decoder needs.
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

// send puts one event on the collection's open watch.
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
			http.NotFound(w, r)
			return
		}
		if collection.status != 0 {
			w.WriteHeader(collection.status)
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
		// watch with no version sends every object too, the way the API
		// server starts one at the most recent version.
		if query.Get("sendInitialEvents") == "true" || query.Get("resourceVersion") == "" {
			for _, item := range collection.items {
				_, _ = w.Write([]byte(watchLine("ADDED", item) + "\n"))
			}
		}
		if query.Get("sendInitialEvents") == "true" {
			_, _ = w.Write([]byte(initialEventsEnd(collection.apiVersion, collection.kind, "100") + "\n"))
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

// requested answers each request the server took for one collection.
func (s *collectionServer) requested(resource schema.GroupVersionResource) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found []string
	for _, request := range s.requests {
		if strings.HasPrefix(request, collectionPathOf(resource)+"?") {
			found = append(found, request)
		}
	}
	return found
}

// runWatch runs one collection watch until the test ends.
func runWatch(t *testing.T, server *collectionServer, watch collectionWatch) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() {
		stop()
		<-done
	})
	client := testWatcher(t, server.handler())
	go func() {
		defer close(done)
		watchCollection(ctx, client, watch)
	}()
}

// wokeWithin reports whether the wake channel received a wake within the
// quiet spell, and empties it.
func wokeWithin(wake chan struct{}) bool {
	select {
	case <-wake:
		return true
	case <-time.After(watchQuietSpell):
		return false
	}
}

// asObject is an object the way the informer hands it to a handler.
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

// Every change to a Play wakes the pass through the real reflector,
// this operator's own status write included, and so does the first
// read: the pass after a status write is how a Finished Play retires at
// once.
func TestAChangeToAPlayWakesThePass(t *testing.T) {
	server := newCollectionServer()
	plays := server.serve(t, playResource, "Play", housePlay("https://nas/film.mkv"))
	wake := make(chan struct{}, 1)
	synced := make(chan struct{})

	runWatch(t, server, collectionWatch{resource: playResource, handler: wakeOnChange(wake),
		synced: func(cache.Store) { close(synced) }})
	mustMatch(t, closedWithin(synced, watchTimeout), true)
	<-wake

	written := housePlay("https://nas/film.mkv")
	written.Metadata.ResourceVersion = "101"
	written.Status.Phase = phaseRunning
	plays.send(t, "MODIFIED", written)
	mustMatch(t, wokeWithin(wake), true)

	plays.send(t, "DELETED", written)
	mustMatch(t, wokeWithin(wake), true)
}

// Any of this operator's pods wakes the pass when it goes away, and a
// playback pod also when its status changes. Nothing else about a pod
// wakes it.
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
		{name: "an idle pod fails", before: labeledPod("theater-idle", idleLabelValue, podRunning),
			after: labeledPod("theater-idle", idleLabelValue, podFailed), want: false},
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

// Every pod the watch selects is this operator's, so the removal of
// any of them wakes the pass.
func TestAPodRemovalWakesThePass(t *testing.T) {
	for _, component := range []string{playbackLabelValue, idleLabelValue, remoteLabelValue} {
		t.Run(component, func(t *testing.T) {
			wake := make(chan struct{}, 1)

			podRule.handler(wake).OnDelete(asObject(t, labeledPod("gone", component, podRunning)))

			mustMatch(t, len(wake), 1)
		})
	}
}

// A removal whose tombstone holds no copy of the pod wakes the pass,
// because every pod the watch selects is this operator's.
func TestAPodRemovedWithNoCopyWakesThePass(t *testing.T) {
	wake := make(chan struct{}, 1)

	podRule.handler(wake).OnDelete(cache.DeletedFinalStateUnknown{Key: "house/movie-playback"})

	mustMatch(t, len(wake), 1)
}

// Through the reflector: the pods watch selects the operator's own pods
// by their component label, and a playback pod that fails wakes the
// pass.
func TestThePodsWatchSelectsTheOperatorsOwnPods(t *testing.T) {
	server := newCollectionServer()
	pods := server.serve(t, podResource, "Pod", labeledPod("movie-playback", playbackLabelValue, podRunning))
	wake := make(chan struct{}, 1)
	synced := make(chan struct{})

	runWatch(t, server, collectionWatch{resource: podResource, labels: ownPodsSelector,
		handler: podRule.handler(wake), synced: func(cache.Store) { close(synced) }})
	mustMatch(t, closedWithin(synced, watchTimeout), true)
	// The first read holds a running playback pod, which wakes the pass
	// once.
	mustMatch(t, wokeWithin(wake), true)
	pods.send(t, "MODIFIED", labeledPod("movie-playback", playbackLabelValue, podFailed))

	mustMatch(t, wokeWithin(wake), true)
	for _, request := range server.requested(podResource) {
		mustMatch(t, strings.Contains(request, "labelSelector=media.liken.sh%2Fcomponent+in+%28playback%2Cidle%2Cremote%29"), true)
	}
}

// Each watch the API server accepts after the first counts as one
// restart, and the refusals while the API server is away do not.
func TestAWatchCountsEachReopen(t *testing.T) {
	server := newCollectionServer()
	plays := server.serve(t, playResource, "Play")
	var reopened sync.WaitGroup
	reopened.Add(1)
	synced := make(chan struct{})

	runWatch(t, server, collectionWatch{resource: playResource, reopened: reopened.Done,
		synced: func(cache.Store) { close(synced) }})
	mustMatch(t, closedWithin(synced, watchTimeout), true)
	plays.send(t, "ADDED", housePlay("https://nas/film.mkv"))
	plays.live <- closeStream

	done := make(chan struct{})
	go func() {
		reopened.Wait()
		close(done)
	}()
	mustMatch(t, closedWithin(done, watchTimeout), true)
}

// servedCluster serves every collection the view reads, from what the
// fake cluster holds.
func servedCluster(t *testing.T, cluster *fakeCluster) *collectionServer {
	t.Helper()
	server := newCollectionServer()
	view := cluster.view()
	collections := []struct {
		resource schema.GroupVersionResource
		kind     string
		source   objectSource
	}{
		{playResource, "Play", view.plays.view.store},
		{playerResource, "Player", view.players},
		{remoteResource, "Remote", view.remotes},
		{keymapResource, "Keymap", view.keymaps},
		{preferencesResource, "MediaPreferences", view.preferences},
		{peripheralResource, "Peripheral", view.peripherals},
		{podResource, "Pod", view.pods},
		{claimResource, "ResourceClaim", view.claims},
		{sliceResource, "ResourceSlice", view.slices},
		{displayResource, "Display", view.displays},
		{receiverResource, "Receiver", view.receivers},
	}
	for _, each := range collections {
		server.serve(t, each.resource, each.kind, each.source.List()...)
	}
	return server
}

// watchedView runs watchCluster against a server until the test ends,
// and answers its view.
func watchedView(t *testing.T, server *collectionServer, wake chan struct{}) *clusterView {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	wait, cancel := context.WithTimeout(ctx, watchTimeout)
	defer cancel()
	view, err := watchCluster(ctx, wait, testWatcher(t, server.handler()), wake, nil)
	mustSucceed(t, err)
	return view
}

// settledHouse is a cluster the operator has already settled: a Player
// with a screen wired through a Receiver, a Remote with its controller
// bonded, and a Play running on the Player, each with the claims and
// the pods the passes built, and the claims allocated. The operator it
// answers has run a pass over that cluster, so it holds what a running
// operator holds.
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
	// The scheduler allocates the claims the passes created, which the
	// fake cluster does not do on its own.
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
	wake := make(chan struct{}, 8)
	cluster, media := settledHouse(t, wake)
	media.view = watchedView(t, servedCluster(t, cluster), wake)
	cluster.requests = nil

	media.pass()

	mustMatchAll(t, cluster.requests, nil)
}

// A collection that a cluster has not installed reads as empty, so the
// operator starts on a cluster with no equipment-operator or
// display-operator. When the resource arrives, the watch finds it.
func TestAnAbsentOptionalCollectionReadsAsEmptyUntilItArrives(t *testing.T) {
	recheckWas := optionalRecheck
	optionalRecheck = 50 * time.Millisecond
	t.Cleanup(func() { optionalRecheck = recheckWas })
	server := servedCluster(t, newFakeCluster())
	server.collections[collectionPathOf(receiverResource)].status = http.StatusNotFound

	view := watchedView(t, server, make(chan struct{}, 1))
	receivers, err := view.Receivers()
	mustSucceed(t, err)
	mustMatch(t, len(receivers), 0)

	server.serve(t, receiverResource, "Receiver", houseReceiver())
	until(t, "the view never held the Receiver that arrived", func() bool {
		receivers, err := view.Receivers()
		return err == nil && len(receivers) == 1
	})
}

// A collection the operator cannot read ends the wait with an error
// that names it, so the process ends and says which grant or which
// resource is missing.
func TestTheFirstReadNamesACollectionItCannotRead(t *testing.T) {
	server := servedCluster(t, newFakeCluster())
	server.collections[collectionPathOf(peripheralResource)].status = http.StatusForbidden
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	wait, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	_, err := watchCluster(ctx, wait, testWatcher(t, server.handler()), make(chan struct{}, 1), nil)

	mustFail(t, err)
	mustMatch(t, strings.Contains(err.Error(), "not read: "+kindPeripheral+":"), true)
}

// An object that does not convert is an error that names it, so a log
// line says which object the operator could not read.
func TestAnObjectThatDoesNotConvertIsAnErrorThatNamesIt(t *testing.T) {
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": mediaAPIVersion, "kind": "Play",
		"metadata": map[string]any{"name": "movie", "namespace": "house"},
		"spec":     "garbled",
	}}

	_, err := convert[Play](object)

	mustFail(t, err)
	mustMatch(t, strings.Contains(err.Error(), "Play house/movie does not convert"), true)
}
