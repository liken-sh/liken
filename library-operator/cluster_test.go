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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// FakeCluster holds the objects an API server would, and records every
// request, so a test reads what a pass did as well as what it left
// behind.
type fakeCluster struct {
	mutex     sync.Mutex
	libraries map[string]*Library
	// The Catalogs the operator reads and writes, by name, one per
	// namespace in the ordinary case.
	catalogs map[string]*NamespaceCatalog
	// The Players media-operator would publish, which the operator reads
	// and never writes.
	players map[string]*Player
	claims  map[string]*PersistentVolumeClaim
	pods    map[string]*Pod
	// The catalog objects the operator writes, by namespace and name,
	// because the operator stands one of each in every namespace that
	// holds a Library.
	slices   map[string]*EndpointSlice
	services map[string]*Service
	// The Jobs and CronJobs the operator creates, keyed by namespace and
	// name, because a worker of one namespace and a worker of another
	// may take the same name.
	jobs     map[string]*Job
	cronJobs map[string]*CronJob
	// The Plays the operator creates, in the order it created them. They
	// are a list and not a map, because every Play takes a name the API
	// server mints.
	plays []Play

	// A PersistentVolume is held as the body the API server serves,
	// because a volume names its storage with a key on the spec and
	// not with a field beside it, and reading that key back is the
	// operator's own work.
	volumes  map[string]string
	requests []string

	// Broken maps a path to the status the server answers it with,
	// which is how a test drives the failure a pass reports and
	// carries on from.
	broken map[string]int

	// RefuseCreate answers every creation, of a pod or of a catalog
	// object, with a conflict: the state a second writer leaves behind.
	refuseCreate bool

	// Parked holds every watch request open, because a watcher has no
	// stop and a watch that ended would set it reconnecting.
	parked chan struct{}
}

func newFakeCluster() *fakeCluster {
	return &fakeCluster{
		libraries: map[string]*Library{},
		catalogs:  map[string]*NamespaceCatalog{},
		players:   map[string]*Player{},
		claims:    map[string]*PersistentVolumeClaim{},
		volumes:   map[string]string{},
		pods:      map[string]*Pod{},
		slices:    map[string]*EndpointSlice{},
		services:  map[string]*Service{},
		jobs:      map[string]*Job{},
		cronJobs:  map[string]*CronJob{},
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
	// A test breaks a path, one request against a path, or one method
	// against a path: the two pod lists differ by their selector alone,
	// so the whole request line is a key too.
	status := f.broken[r.URL.Path]
	if status == 0 {
		status = f.broken[r.URL.RequestURI()]
	}
	// A path is broken for one method alone where a test drives a
	// failure on the write and not on the read before it.
	if status == 0 {
		status = f.broken[r.Method+" "+r.URL.Path]
	}
	if status != 0 {
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
	case r.URL.Path == catalogsPath:
		list := CatalogList{Metadata: ListMeta{ResourceVersion: "1"}}
		for _, key := range sortedNames(f.catalogs) {
			list.Items = append(list.Items, *f.catalogs[key])
		}
		_ = json.NewEncoder(w).Encode(list)
	case r.URL.Path == playersPath:
		list := PlayerList{Metadata: ListMeta{ResourceVersion: "1"}}
		for _, key := range sortedNames(f.players) {
			list.Items = append(list.Items, *f.players[key])
		}
		_ = json.NewEncoder(w).Encode(list)
	case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/libraries/"):
		f.patchLibrary(w, r, name)
	case strings.Contains(r.URL.Path, "/catalogs/") && strings.HasSuffix(r.URL.Path, "/status"):
		var written NamespaceCatalog
		_ = json.NewDecoder(r.Body).Decode(&written)
		f.catalogs[written.Metadata.Name] = &written
		_ = json.NewEncoder(w).Encode(written)
	case strings.HasSuffix(r.URL.Path, "/status"):
		var written Library
		_ = json.NewDecoder(r.Body).Decode(&written)
		f.libraries[written.Metadata.Name] = &written
		_ = json.NewEncoder(w).Encode(written)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/persistentvolumeclaims"):
		if f.refuseCreate {
			w.WriteHeader(http.StatusConflict)
			return
		}
		var created PersistentVolumeClaim
		_ = json.NewDecoder(r.Body).Decode(&created)
		f.claims[created.Metadata.Name] = &created
		_ = json.NewEncoder(w).Encode(created)
	case r.URL.Path == jobsAllPath:
		list := JobList{Metadata: ListMeta{ResourceVersion: "1"}}
		for _, key := range sortedNames(f.jobs) {
			list.Items = append(list.Items, *f.jobs[key])
		}
		_ = json.NewEncoder(w).Encode(list)
	case strings.Contains(r.URL.Path, "/cronjobs"):
		f.serveCronJob(w, r, namespaceOf(r.URL.Path)+"/"+name)
	case strings.Contains(r.URL.Path, "/jobs"):
		f.serveJob(w, r, namespaceOf(r.URL.Path)+"/"+name)
	case r.URL.Path == podsAllPath:
		list := PodList{Metadata: ListMeta{ResourceVersion: "1"}}
		for _, key := range sortedNames(f.pods) {
			if !selects(r.URL.Query().Get("labelSelector"), f.pods[key]) {
				continue
			}
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
	case strings.Contains(r.URL.Path, "/endpointslices"):
		f.serveEndpointSlice(w, r, namespaceOf(r.URL.Path)+"/"+name)
	case strings.Contains(r.URL.Path, "/services"):
		f.serveService(w, r, namespaceOf(r.URL.Path)+"/"+name)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/plays"):
		f.createPlay(w, r)
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

// ServeJob answers a Job the way the API server does: a create stores
// what the body carries, a delete removes it, and anything else reads
// it by name.
func (f *fakeCluster) serveJob(w http.ResponseWriter, r *http.Request, key string) {
	switch r.Method {
	case http.MethodPost:
		if f.refuseCreate {
			w.WriteHeader(http.StatusConflict)
			return
		}
		var created Job
		_ = json.NewDecoder(r.Body).Decode(&created)
		f.jobs[created.Metadata.Namespace+"/"+created.Metadata.Name] = &created
		_ = json.NewEncoder(w).Encode(created)
	case http.MethodDelete:
		if _, held := f.jobs[key]; !held {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(f.jobs, key)
	default:
		answer(w, f.jobs[key])
	}
}

// ServeCronJob answers a CronJob the same way, with the update the
// operator sends when a schedule or an image changes.
func (f *fakeCluster) serveCronJob(w http.ResponseWriter, r *http.Request, key string) {
	switch r.Method {
	case http.MethodPost:
		if f.refuseCreate {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.writeCronJob(w, r, "1")
	case http.MethodPut:
		f.writeCronJob(w, r, "2")
	case http.MethodDelete:
		if _, held := f.cronJobs[key]; !held {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(f.cronJobs, key)
	default:
		answer(w, f.cronJobs[key])
	}
}

func (f *fakeCluster) writeCronJob(w http.ResponseWriter, r *http.Request, resourceVersion string) {
	var written CronJob
	_ = json.NewDecoder(r.Body).Decode(&written)
	written.Metadata.ResourceVersion = resourceVersion
	f.cronJobs[written.Metadata.Namespace+"/"+written.Metadata.Name] = &written
	_ = json.NewEncoder(w).Encode(written)
}

func (f *fakeCluster) heldJob(namespace, name string) *Job {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.jobs[namespace+"/"+name]
}

func (f *fakeCluster) heldCronJob(namespace, name string) *CronJob {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.cronJobs[namespace+"/"+name]
}

// HoldJob puts a Job into the cluster as the Job controller would have
// left it, so a test drives the state a pass reads.
func (f *fakeCluster) holdJob(job *Job) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.jobs[job.Metadata.Namespace+"/"+job.Metadata.Name] = job
}

// HeldJobs is every Job the cluster holds, in name order, so a test
// reads what one pass created.
func (f *fakeCluster) heldJobs() []Job {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	held := []Job{}
	for _, key := range sortedNames(f.jobs) {
		held = append(held, *f.jobs[key])
	}
	return held
}

// The suffix the API server mints onto a generateName. Its own is
// random; this one is fixed, so a test names the object a pass created.
const mintedSuffix = "b2k9x"

// CreatePlay answers a create the way the API server does: the name is
// minted from the generateName, and the object it answers with is the one it
// holds.
func (f *fakeCluster) createPlay(w http.ResponseWriter, r *http.Request) {
	if f.refuseCreate {
		w.WriteHeader(http.StatusConflict)
		return
	}
	var created Play
	_ = json.NewDecoder(r.Body).Decode(&created)
	created.Metadata.Name = created.Metadata.GenerateName + mintedSuffix
	created.Metadata.GenerateName = ""
	f.plays = append(f.plays, created)
	_ = json.NewEncoder(w).Encode(created)
}

// Selects answers the way the API server answers a label selector: an
// equality selector keeps the pods that carry the pair, and an empty
// selector keeps every pod. The operator sends one pair and no other form,
// so a list of scanner pods never answers with a screen pod.
func selects(selector string, pod *Pod) bool {
	if selector == "" {
		return true
	}
	key, value, _ := strings.Cut(selector, "=")
	return pod.Metadata.Labels[key] == value
}

// The API server's own behavior: conditional on the stated
// resourceVersion, and a deleting object with no finalizer left is
// removed.
func (f *fakeCluster) patchLibrary(w http.ResponseWriter, r *http.Request, name string) {
	held := f.libraries[name]
	if held == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var patch struct {
		Metadata struct {
			ResourceVersion string   `json:"resourceVersion"`
			Finalizers      []string `json:"finalizers"`
		} `json:"metadata"`
	}
	_ = json.NewDecoder(r.Body).Decode(&patch)
	if patch.Metadata.ResourceVersion != held.Metadata.ResourceVersion {
		w.WriteHeader(http.StatusConflict)
		return
	}
	held.Metadata.Finalizers = patch.Metadata.Finalizers
	held.Metadata.ResourceVersion = nextVersion(held.Metadata.ResourceVersion)
	if held.Metadata.deleting() && len(held.Metadata.Finalizers) == 0 {
		delete(f.libraries, name)
	}
	_ = json.NewEncoder(w).Encode(held)
}

// The resourceVersion a write produces, which every later conditional
// write on the object has to state.
func nextVersion(current string) string {
	number, err := strconv.Atoi(current)
	if err != nil {
		return "1"
	}
	return strconv.Itoa(number + 1)
}

// NamespaceOf reads the namespace out of a collection path, which is
// how the fake cluster keys the objects a namespaced verb writes. A
// path with no namespace segment answers an empty string, and no verb
// the operator sends takes one.
func namespaceOf(urlPath string) string {
	_, after, found := strings.Cut(urlPath, "/namespaces/")
	if !found {
		return ""
	}
	namespace, _, _ := strings.Cut(after, "/")
	return namespace
}

// ServeEndpointSlice answers a catalog slice the way the API server
// does: an absent slice is a 404, a create stores what the body
// carries, and an update replaces it. The stored resourceVersion is
// what a conditional write is checked against.
func (f *fakeCluster) serveEndpointSlice(w http.ResponseWriter, r *http.Request, key string) {
	switch r.Method {
	case http.MethodPost:
		if f.refuseCreate {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.writeEndpointSlice(w, r, "1")
	case http.MethodPut:
		f.writeEndpointSlice(w, r, "2")
	default:
		answer(w, f.slices[key])
	}
}

func (f *fakeCluster) writeEndpointSlice(w http.ResponseWriter, r *http.Request, resourceVersion string) {
	var written EndpointSlice
	_ = json.NewDecoder(r.Body).Decode(&written)
	written.Metadata.ResourceVersion = resourceVersion
	f.slices[written.Metadata.Namespace+"/"+written.Metadata.Name] = &written
	_ = json.NewEncoder(w).Encode(written)
}

// AssignedClusterIP is the address the fake API server gives a Service
// that asked for none, which is what an update must carry back
// untouched.
const assignedClusterIP = "10.43.0.7"

// ServeService answers a Service the same way, and it assigns the
// clusterIP the API server would: a headless Service keeps the None the
// operator asked for, and any other Service is given an address. The
// value is the API server's from then on.
func (f *fakeCluster) serveService(w http.ResponseWriter, r *http.Request, key string) {
	switch r.Method {
	case http.MethodPost:
		if f.refuseCreate {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.writeService(w, r, "1")
	case http.MethodPut:
		f.writeService(w, r, "2")
	default:
		answer(w, f.services[key])
	}
}

func (f *fakeCluster) writeService(w http.ResponseWriter, r *http.Request, resourceVersion string) {
	var written Service
	_ = json.NewDecoder(r.Body).Decode(&written)
	written.Metadata.ResourceVersion = resourceVersion
	if written.Spec.ClusterIP == "" {
		written.Spec.ClusterIP = assignedClusterIP
	}
	if written.Spec.ClusterIPs == nil {
		written.Spec.ClusterIPs = []string{written.Spec.ClusterIP}
	}
	f.services[written.Metadata.Namespace+"/"+written.Metadata.Name] = &written
	_ = json.NewEncoder(w).Encode(written)
}

// Held reads one object out of the cluster under the lock, so a test
// that inspects the cluster while a watch request is in flight reads
// no torn map.
func (f *fakeCluster) heldPod(name string) *Pod {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.pods[name]
}

func (f *fakeCluster) heldPlays() []Play {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return slices.Clone(f.plays)
}

func (f *fakeCluster) heldLibrary(name string) *Library {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.libraries[name]
}

func (f *fakeCluster) heldEndpointSlice(namespace, name string) *EndpointSlice {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.slices[namespace+"/"+name]
}

func (f *fakeCluster) heldService(namespace, name string) *Service {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.services[namespace+"/"+name]
}

// HoldService puts a Service into the cluster as something other than
// this operator wrote it, so a test drives the divergence a pass finds
// and repairs.
func (f *fakeCluster) holdService(service *Service) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.services[service.Metadata.Namespace+"/"+service.Metadata.Name] = service
}

func (f *fakeCluster) heldCatalog(name string) *NamespaceCatalog {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.catalogs[name]
}

func (f *fakeCluster) heldClaim(name string) *PersistentVolumeClaim {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.claims[name]
}

// SeedPlayer puts one Player in the cluster with the idle block
// media-operator would publish. An empty controller stands for a Player with
// no idle block at all.
func seedPlayer(cluster *fakeCluster, name, namespace, controller string) *Player {
	player := &Player{
		Metadata: ObjectMeta{Name: name, Namespace: namespace, UID: name + "-uid"},
	}
	if controller != "" {
		player.Status.Idle = &PlayerIdleStatus{
			Controller: controller,
			Claim:      name + "-idle-devices",
			Requests:   []string{"draw", "render"},
		}
	}
	cluster.players[name] = player
	return player
}

// CountRequests counts the requests of one method against one kind of
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

// The namespace the seeded Library is in, and so the namespace its
// catalog Service and EndpointSlice are in.
const testLibraryNamespace = "house"

// TestOperator builds the operator around one fake cluster. Its bus is
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
		testScannerImage, testCorrosionImage, testBrowserImage, testBusAddress,
		defaultTopicBase, testOperatorNamespace, testWebhookAddress)
}

// The namespace the operator itself runs in, which is what every
// reported webhook address names, and the address its own endpoint
// listens on. Port zero is a port the kernel picks, so two tests that
// serve at once never collide.
const (
	testOperatorNamespace = "liken-system"
	testWebhookAddress    = "127.0.0.1:0"
)

// OperatorOnABroker is the operator of testOperator with its
// bus connected to a broker the test reads. A test that watches what
// the operator publishes needs the connection, because a publish made
// while the client is disconnected is dropped at QoS 0. The helper
// returns once the client has its write queue, so the first publish
// after it goes out on the connection.
func operatorOnABroker(t *testing.T, cluster *fakeCluster) (*operator, *fakeBroker) {
	t.Helper()
	address, accepted := testBroker(t)
	operator := testOperator(t, cluster)

	connected := make(chan *Bus, 1)
	running, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	operator.bus = newBus(address, "library-operator", nil, func(bus *Bus) { connected <- bus }, nil)
	go operator.bus.Run(running)

	broker := waitForBroker(t, accepted)
	waitForConnect(t, connected)
	return operator, broker
}

// ClearedTopics reads the next count publishes and answers
// with the topics they cleared. A message that clears a retained
// topic is empty and retained, and one that is not fails the test,
// because a payload of any length leaves the topic standing.
func clearedTopics(t *testing.T, broker *fakeBroker, count int) map[string]bool {
	t.Helper()
	cleared := map[string]bool{}
	for range count {
		message := waitForPublish(t, broker.pubs)
		if len(message.payload) != 0 {
			t.Errorf("the payload on %s is %q, want an empty one", message.topic, message.payload)
		}
		if !message.retained {
			t.Errorf("the message on %s is not retained, so it clears nothing", message.topic)
		}
		cleared[message.topic] = true
	}
	return cleared
}

// LibraryTopics names the two retained topics one Library
// stands on the bus, which is the pair a clear has to cover.
func libraryTopics(namespace, name string) []string {
	return []string{
		libraryStatusTopic(defaultTopicBase, namespace, name),
		libraryAvailabilityTopic(defaultTopicBase, namespace, name),
	}
}

// TestRunContext ends when the test does, so the bus the loop starts
// stops dialing a broker that is not there.
func testRunContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// CatalogOwner is the ownerReference the catalog Service and
// EndpointSlice carry, so a test states the owner it expects in the
// same shape a pass builds. The Catalog owns both, and it is the
// controller, because a namespace holds one Catalog.
func catalogOwner(name, uid string) OwnerReference {
	return OwnerReference{APIVersion: catalogAPIVersion, Kind: "Catalog", Name: name, UID: uid, Controller: true}
}

// SeedCatalog seeds one Catalog in a namespace, so a Library there
// proceeds and the operator stands the namespace's catalog cluster.
func seedCatalog(cluster *fakeCluster, name, namespace string) *NamespaceCatalog {
	catalog := &NamespaceCatalog{
		Metadata: ObjectMeta{Name: name, Namespace: namespace, UID: name + "-uid"},
	}
	cluster.catalogs[name] = catalog
	return catalog
}

// TestNamespaceCatalog is one Catalog a reconcile reads, so a test
// hands reconcile the choice a namespace with one Catalog resolves to.
func testNamespaceCatalog() *NamespaceCatalog {
	return &NamespaceCatalog{Metadata: ObjectMeta{Name: "house-catalog", Namespace: "house", UID: "house-catalog-uid"}}
}

// WithCatalog is the catalog choice a namespace with one Catalog
// resolves to, the ordinary state a Library is reconciled against.
func withCatalog() catalogChoice {
	return catalogChoice{catalog: testNamespaceCatalog()}
}

// ReadyCatalogPod is the namespace's catalog pod as the kubelet
// reports it with both containers up, which is the state a Library
// needs before it is Ready.
func readyCatalogPod(catalog, namespace string) *Pod {
	pod := buildCatalogPod(
		&NamespaceCatalog{Metadata: ObjectMeta{Name: catalog, Namespace: namespace, UID: catalog + "-uid"}},
		testScannerImage, testCorrosionImage, testBusAddress, defaultTopicBase)
	// The stamp is what a pass compares against, so a pod without one
	// would read as stale and be replaced on the pass that read it.
	if err := stampTemplateHash(&pod.Metadata, pod.Spec); err != nil {
		panic(err)
	}
	pod.Status = PodStatus{
		Phase:                 podRunning,
		PodIP:                 "10.42.0.9",
		InitContainerStatuses: []ContainerStatus{{Name: catalogContainer, Ready: true}},
		ContainerStatuses:     []ContainerStatus{{Name: reporterContainer, Ready: true}},
	}
	return pod
}

// StandingCatalog is the namespace's Catalog with its pod up, as
// a Library is reconciled against in the ordinary case.
func standingCatalog() catalogChoice {
	catalog := testNamespaceCatalog()
	return catalogChoice{
		catalog: catalog,
		pod:     readyCatalogPod(catalog.Metadata.Name, catalog.Metadata.Namespace),
	}
}

// BoundHouse seeds the cluster with a movies Library over a claim bound
// to an NFS volume, and the namespace's one Catalog, which is the
// ordinary state every other state is read against.
func boundHouse(cluster *fakeCluster) *Library {
	library := studioMovies()
	cluster.libraries["movies"] = library
	seedCatalog(cluster, "house-catalog", "house")
	cluster.claims["movies"] = &PersistentVolumeClaim{
		Metadata: ObjectMeta{Name: "movies", Namespace: "house"},
		Spec:     PersistentVolumeClaimSpec{VolumeName: "pv-movies"},
		Status:   PersistentVolumeClaimStatus{Phase: claimBound},
	}
	cluster.volumes["pv-movies"] = `{"metadata":{"name":"pv-movies"},"spec":` +
		`{"capacity":{"storage":"4Ti"},"accessModes":["ReadOnlyMany"],` +
		`"nfs":{"server":"syn.example","path":"/srv/media/movies"}}}`
	return library
}

// BoundStudio seeds a second Library in a second namespace, over a
// claim of its own and with its own Catalog, so a test sees two catalog
// clusters stand apart.
func boundStudio(cluster *fakeCluster) *Library {
	library := &Library{
		Metadata: ObjectMeta{Name: "series", Namespace: "studio", UID: "series-uid"},
		Spec: LibrarySpec{
			Storage: LibraryStorage{Claim: "shows", Root: "/"},
			Kind:    libraryKindSeries,
			Series:  &LibrarySettings{},
		},
	}
	cluster.libraries["series"] = library
	seedCatalog(cluster, "studio-catalog", "studio")
	cluster.claims["shows"] = &PersistentVolumeClaim{
		Metadata: ObjectMeta{Name: "shows", Namespace: "studio"},
		Spec:     PersistentVolumeClaimSpec{VolumeName: "pv-movies"},
		Status:   PersistentVolumeClaimStatus{Phase: claimBound},
	}
	return library
}
