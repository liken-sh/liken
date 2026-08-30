package main

// The fake cluster every pass runs against: an API server that answers
// the way Kubernetes does, and the objects one seeded house starts
// from. A test reads what a pass did from the requests it recorded and
// what a pass left from the objects it holds.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeCluster holds the objects an API server would, and records every
// request, so a test reads what a pass did as well as what it left
// behind.
type fakeCluster struct {
	mutex     sync.Mutex
	libraries map[string]*Library
	claims    map[string]*PersistentVolumeClaim
	pods      map[string]*Pod

	// A PersistentVolume is held as the body the API server serves,
	// because a volume names its storage with a key on the spec and
	// not with a field beside it, and reading that key back is the
	// operator's own work.
	volumes  map[string]string
	requests []string

	// broken maps a path to the status the server answers it with,
	// which is how a test drives the failure a pass reports and
	// carries on from.
	broken map[string]int

	// refuseCreate answers every pod creation with a conflict, the
	// state a second writer leaves behind.
	refuseCreate bool

	// parked holds every watch request open, because a watcher has no
	// stop and a watch that ended would set it reconnecting.
	parked chan struct{}
}

func newFakeCluster() *fakeCluster {
	return &fakeCluster{
		libraries: map[string]*Library{},
		claims:    map[string]*PersistentVolumeClaim{},
		volumes:   map[string]string{},
		pods:      map[string]*Pod{},
		broken:    map[string]int{},
		parked:    make(chan struct{}),
	}
}

func (f *fakeCluster) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			<-f.parked
			return
		}
		f.mutex.Lock()
		defer f.mutex.Unlock()
		f.serve(w, r)
	})
}

func (f *fakeCluster) serve(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if status := f.broken[r.URL.Path]; status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("the API server is unwell"))
		return
	}
	name := path.Base(r.URL.Path)
	switch {
	case r.URL.Path == versionPath:
		_ = json.NewEncoder(w).Encode(Version{GitVersion: "v1.34.1+k3s1"})
	case r.URL.Path == librariesPath:
		// The list is sorted, so one pass reads the collection in the
		// same order every time.
		list := LibraryList{Metadata: ListMeta{ResourceVersion: "1"}}
		for _, key := range sortedNames(f.libraries) {
			list.Items = append(list.Items, *f.libraries[key])
		}
		_ = json.NewEncoder(w).Encode(list)
	case strings.HasSuffix(r.URL.Path, "/status"):
		var written Library
		_ = json.NewDecoder(r.Body).Decode(&written)
		f.libraries[written.Metadata.Name] = &written
		_ = json.NewEncoder(w).Encode(written)
	case r.URL.Path == podsAllPath:
		list := PodList{Metadata: ListMeta{ResourceVersion: "1"}}
		for _, key := range sortedNames(f.pods) {
			list.Items = append(list.Items, *f.pods[key])
		}
		_ = json.NewEncoder(w).Encode(list)
	case strings.Contains(r.URL.Path, "/persistentvolumeclaims/"):
		answer(w, f.claims[name])
	case strings.Contains(r.URL.Path, "/persistentvolumes/"):
		body, held := f.volumes[name]
		if !held {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, body)
	case r.Method == http.MethodPost:
		if f.refuseCreate {
			w.WriteHeader(http.StatusConflict)
			return
		}
		var created Pod
		_ = json.NewDecoder(r.Body).Decode(&created)
		f.pods[created.Metadata.Name] = &created
		_ = json.NewEncoder(w).Encode(created)
	case r.Method == http.MethodDelete:
		delete(f.pods, name)
	default:
		answer(w, f.pods[name])
	}
}

// held reads one object out of the cluster under the lock, so a test
// that inspects the cluster while a watch request is in flight reads
// no torn map.
func (f *fakeCluster) heldPod(name string) *Pod {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.pods[name]
}

func (f *fakeCluster) heldLibrary(name string) *Library {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.libraries[name]
}

// countRequests counts the requests of one method against one kind of
// object, so a test reads what a pass did rather than only what it
// left.
func (f *fakeCluster) countRequests(method, kind string) int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	count := 0
	for _, request := range f.requests {
		if strings.HasPrefix(request, method) && strings.Contains(request, "/"+kind) {
			count++
		}
	}
	return count
}

func answer[T any](w http.ResponseWriter, held *T) {
	if held == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(held)
}

func sortedNames[T any](objects map[string]*T) []string {
	names := make([]string, 0, len(objects))
	for name := range objects {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// testOperator builds the operator around one fake cluster. Its bus is
// never run by the tests that only take a pass, so a publish finds no
// write queue and drops, which is what a pass wants with no broker
// under the test.
//
// The server outlives the test on purpose. The operator's watchers
// have no stop, so a test that runs the loop ends with both held in a
// watch request, and a server that closed would wait on them.
func testOperator(t *testing.T, cluster *fakeCluster) *operator {
	t.Helper()
	server := httptest.NewServer(cluster.handler())
	return newOperator(NewClient(server.URL, server.Client(), ""),
		testScannerImage, testCorrosionImage, testBusAddress, defaultTopicBase)
}

// testRunContext ends when the test does, so the bus the loop starts
// stops dialing a broker that is not there.
func testRunContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// boundHouse seeds the cluster with a films Library over a claim bound
// to an NFS volume, which is the ordinary state every other state is
// read against.
func boundHouse(cluster *fakeCluster) *Library {
	library := houseFilms()
	cluster.libraries["films"] = library
	cluster.claims["films"] = &PersistentVolumeClaim{
		Metadata: ObjectMeta{Name: "films", Namespace: "house"},
		Spec:     PersistentVolumeClaimSpec{VolumeName: "pv-films"},
		Status:   PersistentVolumeClaimStatus{Phase: claimBound},
	}
	cluster.volumes["pv-films"] = `{"metadata":{"name":"pv-films"},"spec":` +
		`{"capacity":{"storage":"4Ti"},"accessModes":["ReadOnlyMany"],` +
		`"nfs":{"server":"syn.example","path":"/volume1/films"}}}`
	return library
}
