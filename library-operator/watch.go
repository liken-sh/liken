//go:build !pod

package main

// A watch keeps this operator's view of a collection current without a
// timer. The API server sends each change to the collection as it
// happens, and a watch with no change to send costs nothing.
//
// The shared informer package runs each watch on client-go's reflector.
//
// Each informer holds the collection it watches, and a pass reads the
// collection from that copy instead of listing it from the API server.
// The informer writes a change into its copy before it calls the
// handler, so the pass that a change wakes reads that change. The
// Libraries, the Catalogs, and the MetadataProviders are read through
// objectcache.go, because the operator writes them and the copy can be
// older than its own write.
//
// The shared apiclient package still sends every other read and every
// write a pass makes.
//
// This file is not in the pod build. The pods and Jobs the operator
// creates run the same program with the build tag pod, and none of
// them watches anything (operate_pod.go says why).

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// The eight collections this operator watches.
var (
	libraryResource          = mustGroupVersion(libraryAPIVersion).WithResource("libraries")
	catalogResource          = mustGroupVersion(libraryAPIVersion).WithResource("catalogs")
	metadataProviderResource = mustGroupVersion(metadataProviderAPIVersion).WithResource("metadataproviders")
	playerResource           = mustGroupVersion(playerAPIVersion).WithResource("players")
	mediaPreferencesResource = mustGroupVersion(playerAPIVersion).WithResource("mediapreferences")
	playResource             = mustGroupVersion(playerAPIVersion).WithResource("plays")
	personResource           = mustGroupVersion(personAPIVersion).WithResource("people")
	podResource              = schema.GroupVersionResource{Version: podAPIVersion, Resource: "pods"}
	claimResource            = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}
	volumeResource           = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}
	jobResource              = mustGroupVersion(batchAPIVersion).WithResource("jobs")
	nodeResource             = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
	serviceResource          = schema.GroupVersionResource{Version: "v1", Resource: "services"}
	endpointSliceResource    = mustGroupVersion(endpointSliceAPIVersion).WithResource("endpointslices")
	configMapResource        = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	claimTemplateResource    = mustGroupVersion(deviceAPIVersion).WithResource("resourceclaimtemplates")
)

// The selectors of the watches of the objects the operator stands. A
// Service and a people ConfigMap carry scannerLabelKey, and an
// EndpointSlice carries the managed-by label every slice this operator
// writes carries.
const (
	ownedServicesSelector = scannerLabelKey + " in (" +
		catalogLabelValue + "," + progressLabelValue + "," + jellyfinLabelValue + ")"
	ownedSlicesSelector     = managedByLabel + "=" + endpointSliceManager
	ownedConfigMapsSelector = scannerLabelKey + "=" + screenLabelValue
	// Every trickplay template carries the label of the Library it
	// belongs to.
	ownedClaimTemplatesSelector = libraryLabelKey
)

func mustGroupVersion(apiVersion string) schema.GroupVersion {
	version, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		panic(err)
	}
	return version
}

// watches holds the informers and answers the pass from them.
type watches struct {
	libraries         *collection[Library]
	catalogs          *collection[NamespaceCatalog]
	memberPods        *collection[Pod]
	players           *collection[Player]
	mediaPreferences  *collection[MediaPreferences]
	metadataProviders *collection[MetadataProvider]
	plays             *collection[Play]
	people            *collection[Person]

	// The objects the pass stands, and the objects it reads to stand
	// them. A Library names any claim in its namespace, and a bound
	// claim names any volume, so the claims, the volumes, and the nodes
	// are watched whole. The pods, the Jobs, the Services, the slices,
	// and the ConfigMaps are the operator's own, selected by its labels.
	claims          *collection[PersistentVolumeClaim]
	volumes         *collection[PersistentVolume]
	stoodPods       *collection[Pod]
	progressMembers *collection[Pod]
	workerJobs      *collection[Job]
	nodes           *collection[Node]
	services        *collection[Service]
	endpointSlices  *collection[EndpointSlice]
	configMaps      *collection[ConfigMap]
	claimTemplates  *collection[ResourceClaimTemplate]

	// The client and the memos objectcache.go reads the Libraries, the
	// Catalogs, and the MetadataProviders with. The memos are the
	// operator's own, because the operator's writes note them.
	client   *apiclient.Client
	versions objectVersions

	group sync.WaitGroup
}

// oneWatch is what startWatches and settle need of one collection,
// whatever struct it decodes into.
type oneWatch interface {
	wait()
	settledOrEnded(ctx context.Context) error
}

// startWatches starts every informer. Each change a watch delivers
// sends a wake, and wait returns when ctx has ended and every informer
// has stopped.
//
// Two rules decide which change wakes the pass, because the pass reads
// two kinds of object:
//
//   - A Library, a Catalog, and a MetadataProvider are objects a person
//     declares and whose status only this operator writes. A change
//     wakes the pass when it changes the spec, the deletion mark, or the
//     object's identity. So this operator's own status writes, and its
//     own finalizer patches on a Library, wake no pass.
//   - A Player, MediaPreferences, a Play, a Person, and a catalog member
//     pod are objects other writers change: media-operator,
//     people-operator, and the kubelet. The pass reads their status, so a
//     change wakes the pass when any field this operator reads changed.
//     A field outside the operator's structs, such as a status field that
//     only media-operator reads, wakes nothing.
func startWatches(ctx context.Context, client dynamic.Interface, wake chan<- struct{}, m *metrics,
	reader *apiclient.Client, versions objectVersions) *watches {
	w := &watches{
		client:   reader,
		versions: versions,
		libraries: newCollection(ctx, client, wake, m, "libraries", kindLibrary, libraryResource, "",
			edited(func(library *Library) *ObjectMeta { return &library.Metadata })),
		catalogs: newCollection(ctx, client, wake, m, "catalogs", kindCatalog, catalogResource, "",
			edited(func(catalog *NamespaceCatalog) *ObjectMeta { return &catalog.Metadata })),
		memberPods: newCollection(ctx, client, wake, m, "catalog member pods", kindPod, podResource, catalogMemberSelector,
			readChanged(func(pod *Pod) *ObjectMeta { return &pod.Metadata })),
		players: newCollection(ctx, client, wake, m, "players", kindPlayer, playerResource, "",
			readChanged(func(player *Player) *ObjectMeta { return &player.Metadata })),
		mediaPreferences: newCollection(ctx, client, wake, m, "media preferences", kindMediaPreferences,
			mediaPreferencesResource, "",
			readChanged(func(preferences *MediaPreferences) *ObjectMeta { return &preferences.Metadata })),
		metadataProviders: newCollection(ctx, client, wake, m, "metadata providers", kindMetadataProvider,
			metadataProviderResource, "",
			edited(func(provider *MetadataProvider) *ObjectMeta { return &provider.Metadata })),
		plays: newCollection(ctx, client, wake, m, "plays", kindPlay, playResource, "",
			readChanged(func(play *Play) *ObjectMeta { return &play.Metadata })),
		people: newCollection(ctx, client, wake, m, "people", kindPerson, personResource, "",
			readChanged(func(person *Person) *ObjectMeta { return &person.Metadata })),

		claims: newCollection(ctx, client, wake, m, "claims", kindClaim, claimResource, "",
			readChanged(func(claim *PersistentVolumeClaim) *ObjectMeta { return &claim.Metadata })),
		volumes: newCollection(ctx, client, wake, m, "volumes", kindVolume, volumeResource, "",
			readChanged(func(volume *PersistentVolume) *ObjectMeta { return &volume.Metadata })),
		stoodPods: newCollection(ctx, client, wake, m, "stood pods", kindPod, podResource, stoodPodsSelector,
			readChanged(func(pod *Pod) *ObjectMeta { return &pod.Metadata })),
		progressMembers: newCollection(ctx, client, wake, m, "progress member pods", kindPod, podResource,
			progressMemberSelector, readChanged(func(pod *Pod) *ObjectMeta { return &pod.Metadata })),
		workerJobs: newCollection(ctx, client, wake, m, "worker jobs", kindJob, jobResource, workerJobsSelector,
			readChanged(func(job *Job) *ObjectMeta { return &job.Metadata })),
		nodes: newCollection(ctx, client, wake, m, "nodes", kindNode, nodeResource, "",
			readChanged(func(node *Node) *ObjectMeta { return &node.Metadata })),
		services: newCollection(ctx, client, wake, m, "services", kindService, serviceResource, ownedServicesSelector,
			addedOrRemoved[Service]),
		endpointSlices: newCollection(ctx, client, wake, m, "endpoint slices", kindEndpointSlice, endpointSliceResource,
			ownedSlicesSelector, addedOrRemoved[EndpointSlice]),
		configMaps: newCollection(ctx, client, wake, m, "people config maps", kindConfigMap, configMapResource,
			ownedConfigMapsSelector, addedOrRemoved[ConfigMap]),
		claimTemplates: newCollection(ctx, client, wake, m, "trickplay templates", kindClaimTemplate,
			claimTemplateResource, ownedClaimTemplatesSelector, addedOrRemoved[ResourceClaimTemplate]),
	}
	for _, one := range w.all() {
		w.group.Go(one.wait)
	}
	return w
}

// all is every informer, the seven the operator needs first.
func (w *watches) all() []oneWatch {
	return []oneWatch{w.libraries, w.catalogs, w.memberPods,
		w.claims, w.volumes, w.stoodPods, w.workerJobs,
		w.players, w.mediaPreferences, w.metadataProviders, w.plays, w.people,
		w.progressMembers, w.nodes, w.services, w.endpointSlices, w.configMaps, w.claimTemplates}
}

// required is how many of all() the operator must read before its first
// pass.
const required = 7

// wait returns when every informer has stopped and made its last
// handler call.
func (w *watches) wait() { w.group.Wait() }

// settle waits until every collection has been read once, or has
// failed its first read. The pass acts on what it reads, so a first
// pass on a copy that is still empty would read every Library as gone.
//
// The Libraries, the Catalogs, the member pods, the claims, the volumes,
// the stood pods, and the worker Jobs must be read: without them a pass
// cannot tell what stands from what it must create, so a failure there
// ends the operator. The Players, the MediaPreferences, the
// MetadataProviders, the Plays, and the people belong to operators a
// cluster may not run, and a collection nobody serves reads as empty
// until its informer reads it. The rest cost a pass one step when they
// have not been read, as a failed read of them always has.
func (w *watches) settle(ctx context.Context) error {
	for index, one := range w.all() {
		err := one.settledOrEnded(ctx)
		if err == nil {
			continue
		}
		// An optional collection that has not answered within the bound
		// reads as not read yet, and its informer keeps trying, as a
		// failed list of it never ended the operator.
		if index < required {
			return err
		}
	}
	return nil
}

// A read of the Libraries or the Catalogs that fails ends the pass, the
// way a failed list of them always has.
func (w *watches) readLibraries(ctx context.Context) (*LibraryList, error) {
	if !w.libraries.read.Load() {
		items, err := w.libraries.items()
		return &LibraryList{Items: items}, err
	}
	items, err := informer.CurrentList[Library](w.client.WithContext(ctx),
		informer.Held{View: w.libraries.view(), Versions: w.versions.libraries}, memo.NamespacedPath(libraryPath))
	return &LibraryList{Items: items}, err
}

func (w *watches) readCatalogs(ctx context.Context) (*CatalogList, error) {
	if !w.catalogs.read.Load() {
		items, err := w.catalogs.items()
		return &CatalogList{Items: items}, err
	}
	items, err := informer.CurrentList[NamespaceCatalog](w.client.WithContext(ctx),
		informer.Held{View: w.catalogs.view(), Versions: w.versions.catalogs}, memo.NamespacedPath(catalogPath))
	return &CatalogList{Items: items}, err
}

func (w *watches) readMemberPods() (*PodList, error) {
	items, err := w.memberPods.items()
	return &PodList{Items: items}, err
}

func (w *watches) readPlayers() (*PlayerList, error) {
	items, err := w.players.items()
	return &PlayerList{Items: items}, err
}

func (w *watches) readMediaPreferences() (*MediaPreferencesList, error) {
	items, err := w.mediaPreferences.items()
	return &MediaPreferencesList{Items: items}, err
}

func (w *watches) readMetadataProviders(ctx context.Context) (*MetadataProviderList, error) {
	if !w.metadataProviders.read.Load() {
		items, err := w.metadataProviders.items()
		return &MetadataProviderList{Items: items}, err
	}
	items := currentOrCached[MetadataProvider](ctx, w.client,
		informer.Held{View: w.metadataProviders.view(), Versions: w.versions.providers}, memo.NamespacedPath(metadataProviderPath))
	return &MetadataProviderList{Items: items}, nil
}

func (w *watches) readPlays() (*PlayList, error) {
	items, err := w.plays.items()
	return &PlayList{Items: items}, err
}

func (w *watches) readPeople() (*PersonList, error) {
	items, err := w.people.items()
	return &PersonList{Items: items}, err
}

func (w *watches) readClaims() (*PersistentVolumeClaimList, error) {
	items, err := w.claims.items()
	return &PersistentVolumeClaimList{Items: items}, err
}

func (w *watches) readVolumes() (*PersistentVolumeList, error) {
	items, err := w.volumes.items()
	return &PersistentVolumeList{Items: items}, err
}

func (w *watches) readStoodPods() (*PodList, error) {
	items, err := w.stoodPods.items()
	return &PodList{Items: items}, err
}

func (w *watches) readProgressMembers() (*PodList, error) {
	items, err := w.progressMembers.items()
	return &PodList{Items: items}, err
}

// The worker Jobs are read through the memo, from a store that holds
// the whole collection of this operator's Jobs: every Job it creates
// carries the label the watch selects. So a list answers a Job the pass
// created that the store does not hold yet, and leaves out one it
// deleted that the store still holds.
//
// Each Job takes a new name, so the memo forgets each Job that is gone
// (memo.Versions.ForgetGone), or it would hold a record of every Job for
// the life of the process.
func (w *watches) readWorkerJobs(ctx context.Context) (*JobList, error) {
	if !w.workerJobs.read.Load() {
		items, err := w.workerJobs.items()
		return &JobList{Items: items}, err
	}
	return currentJobs(ctx, w.client, informer.Held{View: w.workerJobs.view(), Versions: w.versions.jobs})
}

// currentJobs answers the worker Jobs from a store that holds its first
// read, through the memo, and forgets each Job that is gone.
func currentJobs(ctx context.Context, c *apiclient.Client, held informer.Held) (*JobList, error) {
	held.View.Whole = true
	items, err := informer.CurrentList[Job](c.WithContext(ctx), held,
		memo.NamespacedPath(func(namespace, name string) string { return jobsPath(namespace) + "/" + name }))
	if err != nil {
		return nil, err
	}
	listed := make(map[string]bool, len(items))
	for index := range items {
		listed[informer.Key(&items[index].Metadata)] = true
	}
	held.Versions.ForgetGone(held.View.Store, listed)
	return &JobList{Items: items}, nil
}

func (w *watches) readNodes() (*NodeList, error) {
	items, err := w.nodes.items()
	return &NodeList{Items: items}, err
}

func (w *watches) readService(ctx context.Context, namespace, name string) (*Service, error) {
	return readStood[Service](ctx, w.client, informer.Held{View: w.services.view(), Versions: w.versions.services},
		informer.Key(&ObjectMeta{Namespace: namespace, Name: name}), servicesPath(namespace)+"/"+name)
}

func (w *watches) readEndpointSlice(ctx context.Context, namespace, name string) (*EndpointSlice, error) {
	return readStood[EndpointSlice](ctx, w.client,
		informer.Held{View: w.endpointSlices.view(), Versions: w.versions.endpointSlices},
		informer.Key(&ObjectMeta{Namespace: namespace, Name: name}), endpointSlicesPath(namespace)+"/"+name)
}

func (w *watches) readClaimTemplate(ctx context.Context, namespace, name string) (*ResourceClaimTemplate, error) {
	return readStood[ResourceClaimTemplate](ctx, w.client,
		informer.Held{View: w.claimTemplates.view(), Versions: w.versions.claimTemplates},
		informer.Key(&ObjectMeta{Namespace: namespace, Name: name}), claimTemplatesPath(namespace)+"/"+name)
}

func (w *watches) readConfigMap(ctx context.Context, namespace, name string) (*ConfigMap, error) {
	return readStood[ConfigMap](ctx, w.client, informer.Held{View: w.configMaps.view(), Versions: w.versions.configMaps},
		informer.Key(&ObjectMeta{Namespace: namespace, Name: name}), configMapsPath(namespace)+"/"+name)
}

// collection is one informer, and the state of its first read.
type collection[T any] struct {
	// what is the plural a log line names.
	what string
	// changed answers whether an update from before to after wakes the
	// pass.
	changed func(before, after T) bool

	watch *informer.Collection

	// settled closes when the first read has succeeded or has failed.
	// failure holds the last failed read, until a read succeeds. read is
	// true once the informer has read the whole collection.
	settled    chan struct{}
	settleOnce sync.Once
	mu         sync.Mutex
	failure    error
	read       atomic.Bool
}

// newCollection starts one informer, which runs until ctx ends. A label
// selector, when it is not empty, narrows the list and the watch to the
// objects that carry that label. The watch of a namespaced resource
// covers every namespace. Every watch the API server accepts after the
// first counts as a restart.
func newCollection[T any](ctx context.Context, client dynamic.Interface, wake chan<- struct{}, m *metrics,
	what, kind string, resource schema.GroupVersionResource, selector string,
	changed func(before, after T) bool) *collection[T] {
	c := &collection[T]{what: what, changed: changed, settled: make(chan struct{})}
	signal := func() { poke(wake) }
	c.watch = informer.Start(ctx, client, informer.Source{Resource: resource, LabelSelector: selector}, informer.Options{
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc:    func(object any) { c.added(object, signal) },
			UpdateFunc: func(before, after any) { c.updated(before, after, signal) },
			DeleteFunc: func(object any) { c.removed(object, signal) },
		},
		Synced:     c.succeeded,
		Reopened:   func() { m.recordWatchRestart(kind) },
		ListFailed: c.failed,
		Transform:  dropNodeImages,
	})
	return c
}

// wait returns after the informer's last handler call, once ctx ends.
func (c *collection[T]) wait() { <-c.watch.Done() }

// failed records a read that failed, and settles the collection if it
// has not been read yet.
func (c *collection[T]) failed(err error) {
	c.mu.Lock()
	c.failure = err
	c.mu.Unlock()
	c.settleOnce.Do(func() { close(c.settled) })
}

// succeeded clears a failure once the informer has read the whole
// collection.
func (c *collection[T]) succeeded() {
	c.mu.Lock()
	c.failure = nil
	c.mu.Unlock()
	c.read.Store(true)
	c.settleOnce.Do(func() { close(c.settled) })
}

// settledOrEnded waits for the first read. It answers the read's error,
// or ctx's error when ctx ends first.
func (c *collection[T]) settledOrEnded(ctx context.Context) error {
	select {
	case <-c.settled:
	case <-ctx.Done():
		return fmt.Errorf("reading the %s: %w", c.what, ctx.Err())
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure != nil && !c.read.Load() {
		return c.failure
	}
	return nil
}

// view is the informer's store, for objectcache.go.
func (c *collection[T]) view() informer.View { return c.watch.View() }

// items answers every object the informer holds, in namespace and name
// order, the order a list from the API server takes, through the shared
// informer.CachedList. Before the first read succeeds it answers that
// read's error. An object that does not convert is logged and left out,
// the rule every liken-sh operator follows. The Libraries, the Catalogs,
// and the MetadataProviders are read through informer.CurrentList
// instead, which reads such an object from the API server, so a pass
// never reads a Library that is there as one that is gone.
//
// items answers from the store even while the API server forbids the
// watch, which is an exception to the rule of the operators skill.
// This operator has no read of these collections from the API server:
// the pass reads the member pods, the Players, the MediaPreferences, the
// Plays, the people, the claims, the volumes, the stood pods, the
// progress pods, and the nodes only from their stores. While the watch
// is forbidden, the reflector lists the collection again after each
// backoff, up to thirty seconds apart, so the store is at most that old.
// A read of each collection from the API server on every pass would
// cost more than the delay it saves.
func (c *collection[T]) items() ([]T, error) {
	if !c.read.Load() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.failure != nil {
			return nil, c.failure
		}
		return nil, fmt.Errorf("the %s have not been read yet", c.what)
	}
	return informer.CachedList[T](c.view()), nil
}

// added wakes the pass for a new object. The informer also reports the
// first read of the collection this way, one object at a time, and a
// read after a gap in the watch as the differences from what it held.
func (c *collection[T]) added(object any, wake func()) {
	if _, err := informer.Convert[T](object); err != nil {
		informer.Report("the "+c.what, err)
	}
	wake()
}

// removed wakes the pass for an object the API server removed.
func (c *collection[T]) removed(object any, wake func()) {
	if _, err := informer.Convert[T](object); err != nil {
		informer.Report("the "+c.what, err)
	}
	wake()
}

// updated wakes the pass when the collection's rule says the change is
// one the pass acts on. A copy that does not convert is reported, and
// the change wakes the pass, because nothing says what it changed.
func (c *collection[T]) updated(before, after any, wake func()) {
	now, err := informer.Convert[T](after)
	if err != nil {
		informer.Report("the "+c.what, err)
		wake()
		return
	}
	was, err := informer.Convert[T](before)
	if err != nil || c.changed(was, now) {
		wake()
	}
}

// edited is the rule for an object a person declares and only this
// operator writes the status of. metadata.generation counts the changes
// to the spec, and a write to the status subresource or to the metadata
// does not raise it. The deletion mark starts a departure. The UID is in
// the compare because an object that somebody deleted and created again
// with the same name during a gap in the watch reaches the handler as an
// update, and the new object can have the old one's generation.
func edited[T any](meta func(*T) *ObjectMeta) func(before, after T) bool {
	return func(before, after T) bool {
		was, now := meta(&before), meta(&after)
		return was.UID != now.UID || was.Generation != now.Generation || was.deleting() != now.deleting()
	}
}

// readChanged is the rule for an object another writer changes. The
// resourceVersion changes with every write, so the compare leaves it
// out, and what is left is every field this operator reads.
func readChanged[T any](meta func(*T) *ObjectMeta) func(before, after T) bool {
	return func(before, after T) bool {
		meta(&before).ResourceVersion = ""
		meta(&after).ResourceVersion = ""
		return !reflect.DeepEqual(before, after)
	}
}

// addedOrRemoved is the rule for an object only this operator writes. Its
// own writes arrive as updates and wake nothing, because the pass that
// wrote the object already holds what it wrote. A create or a delete
// wakes the pass, so a Service a person deletes is stood again at once.
// An edit by another writer waits for the next pass.
func addedOrRemoved[T any](before, after T) bool { return false }

// dropNodeImages removes status.images from a Node before the informer
// stores it. The field lists every image the Node's runtime holds, which
// is most of a Node's size, and the operator never reads it.
func dropNodeImages(item *unstructured.Unstructured) {
	if item.GetKind() == "Node" {
		unstructured.RemoveNestedField(item.Object, "status", "images")
	}
}
