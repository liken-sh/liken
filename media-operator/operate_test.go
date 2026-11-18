package main

// These tests run whole passes against a fake API server that holds the
// kinds this operator reads and writes, and check what each pass left
// behind.

import (
	"encoding/json"
	"io"
	"net/http"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

// fakeCluster holds one map for each kind that the operator reads and
// writes, and the list of requests that the passes made. The Events
// that a pass posts go to events, in front of the cluster's own
// handler, so requests has no Event.
type fakeCluster struct {
	events *eventstest.Events

	plays      map[string]*Play
	players    map[string]*Player
	remotes    map[string]*Remote
	keymaps    map[string]*Keymap
	mediaprefs map[string]*MediaPreferences
	claims     map[string]*ResourceClaim
	pods       map[string]*Pod
	requests   []string

	// podsLinger names the pods that this server keeps after a delete,
	// with a deletionTimestamp set, as a real pod stays Terminating
	// while its containers stop.
	podsLinger map[string]bool

	// claimsLinger names the claims that this server keeps after a
	// delete, as a real claim stays until no pod uses it.
	claimsLinger map[string]bool

	// The hardware that display-operator publishes: one Display for
	// each panel, and the ResourceSlices that name each allocated
	// device. applies is every apply that this operator sent, in order,
	// including the refused ones.
	displays map[string]*Display
	slices   []ResourceSlice
	applies  []displayApplied

	// The bonded devices that bluetooth-operator publishes, one for
	// each controller, keyed by the device address in lowercase with
	// dashes.
	peripherals map[string]*Peripheral

	// The equipment that equipment-operator publishes, one Receiver for
	// each piece of equipment. sessions is every status.session apply
	// that this operator sent, and specReleases is every spec apply
	// that removes spec.session, in order, including the refused ones.
	receivers       map[string]*Receiver
	sessions        []receiverApplied
	specReleases    []receiverApplied
	sessionsFail    bool
	receiversAbsent bool

	// The Sinks that audio-operator publishes, and every session apply
	// that this operator sent to a Sink, in order. The Televisions that
	// equipment-operator publishes, whose screenAsk requests the pass
	// relays.
	sinks        map[string]*Sink
	sinkSessions []sinkApplied
	televisions  map[string]*Television

	// podPatchFails makes the API server refuse a pod label patch. A
	// pass leaves the pod unlabeled and patches it again on the next
	// pass.
	podPatchFails bool

	// podDeleteFails makes the API server refuse a pod delete, so the
	// pod stays and keeps running.
	podDeleteFails bool

	// playerApplies is every apply of spec.power that this operator
	// sent. playerApplyFails makes the API server refuse the next ones.
	playerApplies    []playerApplied
	playerApplyFails bool

	// applyFails makes the API server refuse to write a Display. A pass
	// leaves the panel as it is and tries again.
	applyFails bool

	// fails names the paths that this server answers with a 500, and
	// whose objects the view returns malformed, so they do not convert
	// to their Go type. A test uses it to show what a pass does with a
	// read that fails.
	fails map[string]bool

	// unseen names the objects that the view does not have yet, as when
	// a watch has not received an object that the API server already
	// has.
	unseen map[string]bool

	// held has the requests that this server answers only after the
	// test releases them, keyed as requests records them. A held request
	// acts as a slow API server, so a test can check what a pass did
	// before it reached that request.
	held map[string]chan struct{}

	// arrived, when a test sets it, receives each held request as the
	// server starts to hold it, so the test can act while the pass
	// waits.
	arrived chan string
}

// displayApplied is one apply that the operator made: the Display it
// named, the override block it wrote, and the field manager it wrote
// under.
type displayApplied struct {
	name     string
	override *DisplayOverride
	manager  string
}

// playerApplied is one apply of a Player's spec that the operator made:
// the fields of the spec that it set, as JSON, the field manager, and
// whether it forced the fields away from another manager.
type playerApplied struct {
	fields  string
	manager string
	force   bool
}

// sinkApplied is one session apply that the operator made on a Sink.
type sinkApplied struct {
	name    string
	session *SinkSession
	manager string
}

// receiverApplied is one session apply that the operator made: the
// Receiver it named, the block it wrote, and the field manager it wrote
// under.
type receiverApplied struct {
	name    string
	session *ReceiverSession
	manager string
}

func newFakeCluster() *fakeCluster {
	return &fakeCluster{
		events:       &eventstest.Events{},
		plays:        map[string]*Play{},
		players:      map[string]*Player{},
		remotes:      map[string]*Remote{},
		keymaps:      map[string]*Keymap{},
		mediaprefs:   map[string]*MediaPreferences{},
		claims:       map[string]*ResourceClaim{},
		pods:         map[string]*Pod{},
		podsLinger:   map[string]bool{},
		claimsLinger: map[string]bool{},
		displays:     map[string]*Display{},

		receivers:   map[string]*Receiver{},
		sinks:       map[string]*Sink{},
		televisions: map[string]*Television{},

		peripherals: map[string]*Peripheral{},
		fails:       map[string]bool{},
		unseen:      map[string]bool{},
		held:        map[string]chan struct{}{},
	}
}

// hold makes the server answer one request, named by its method and
// path, only after the test calls the returned function. The function
// releases the request at most once, so a test can both defer it and
// call it.
func (f *fakeCluster) hold(request string) func() {
	held := make(chan struct{})
	f.held[request] = held
	return sync.OnceFunc(func() { close(held) })
}

func (f *fakeCluster) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		if release, holding := f.held[r.Method+" "+r.URL.Path]; holding {
			if f.arrived != nil {
				f.arrived <- r.Method + " " + r.URL.Path
			}
			<-release
		}
		if f.fails[r.URL.Path] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		name := path.Base(r.URL.Path)
		switch {
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/players/") && strings.HasSuffix(r.URL.Path, "/status"):
			// A Player status write answers 409 for a stale read and
			// moves the version on, as for a Play below.
			var written Player
			_ = json.NewDecoder(r.Body).Decode(&written)
			if held, exists := f.players[written.Metadata.Name]; exists &&
				held.Metadata.ResourceVersion != written.Metadata.ResourceVersion {
				w.WriteHeader(http.StatusConflict)
				return
			}
			version, _ := strconv.Atoi(written.Metadata.ResourceVersion)
			written.Metadata.ResourceVersion = strconv.Itoa(version + 1)
			f.players[written.Metadata.Name] = &written
			_ = json.NewEncoder(w).Encode(written)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/remotes/") && strings.HasSuffix(r.URL.Path, "/status"):
			var written Remote
			_ = json.NewDecoder(r.Body).Decode(&written)
			f.remotes[written.Metadata.Name] = &written
			_ = json.NewEncoder(w).Encode(written)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/status"):
			// A status write names the resourceVersion that it read,
			// and the server answers 409 when the Play was written
			// since, as the API server refuses a write from a stale
			// read. An accepted write moves the version on.
			var written Play
			_ = json.NewDecoder(r.Body).Decode(&written)
			if held, exists := f.plays[written.Metadata.Name]; exists &&
				held.Metadata.ResourceVersion != written.Metadata.ResourceVersion {
				w.WriteHeader(http.StatusConflict)
				return
			}
			version, _ := strconv.Atoi(written.Metadata.ResourceVersion)
			written.Metadata.ResourceVersion = strconv.Itoa(version + 1)
			f.plays[written.Metadata.Name] = &written
			_ = json.NewEncoder(w).Encode(written)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/plays/"):
			writeObject(w, f.plays[name])
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/players/"):
			writeObject(w, f.players[name])
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/remotes/"):
			writeObject(w, f.remotes[name])
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/plays/"):
			f.patchPlay(w, r, name)
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/players/"):
			f.applyPlayer(w, r, name)
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/sinks/") && name == "status":
			f.applySinkSession(w, r, path.Base(path.Dir(r.URL.Path)))
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/receivers/") && name == "status":
			f.applySession(w, r, path.Base(path.Dir(r.URL.Path)))
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/receivers/"):
			f.releaseSpecSession(w, r, name)
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/displays/"):
			f.apply(w, r, name)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/resourceclaims/"):
			writeObject(w, f.claims[name])
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/resourceclaims"):
			claim := &ResourceClaim{}
			_ = json.NewDecoder(r.Body).Decode(claim)
			f.claims[claim.Metadata.Name] = claim
			_ = json.NewEncoder(w).Encode(claim)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods/"):
			writeObject(w, f.pods[name])
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pods"):
			pod := &Pod{}
			_ = json.NewDecoder(r.Body).Decode(pod)
			// A name that is taken answers 409, including the name of a
			// lingering pod, as the API server refuses a create until a
			// deleted pod is gone.
			if _, taken := f.pods[pod.Metadata.Name]; taken {
				w.WriteHeader(http.StatusConflict)
				return
			}
			// The API server gives each pod its own UID, so a pod that
			// is recreated under the name of an old pod is a different
			// object.
			if pod.Metadata.UID == "" {
				pod.Metadata.UID = "pod-uid-" + strconv.Itoa(len(f.requests))
			}
			f.pods[pod.Metadata.Name] = pod
			_ = json.NewEncoder(w).Encode(pod)
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/pods/"):
			f.patchPodLabels(w, r, name)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/plays/"):
			delete(f.plays, name)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/pods/") && f.podDeleteFails:
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/pods/"):
			if pod, exists := f.pods[name]; exists && f.podsLinger[name] {
				pod.Metadata.DeletionTimestamp = "2026-09-09T10:30:00Z"
			} else {
				delete(f.pods, name)
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/resourceclaims/"):
			if claim, exists := f.claims[name]; exists && f.claimsLinger[name] {
				claim.Metadata.DeletionTimestamp = "2026-09-09T10:30:00Z"
			} else {
				delete(f.claims, name)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("the fake API server received %s %s, which it does not serve", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// patchPlay applies one merge patch to a Play's finalizer list, as the
// API server does: the resourceVersion in the patch must equal the
// object's, the write moves that version on, and a deleting Play whose
// last finalizer was removed is deleted by the server.
func (f *fakeCluster) patchPlay(w http.ResponseWriter, r *http.Request, name string) {
	held, exists := f.plays[name]
	if !exists {
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
	version, _ := strconv.Atoi(held.Metadata.ResourceVersion)
	held.Metadata.ResourceVersion = strconv.Itoa(version + 1)
	if held.Metadata.deleting() && len(held.Metadata.Finalizers) == 0 {
		delete(f.plays, name)
	}
	_ = json.NewEncoder(w).Encode(held)
}

// patchPodLabels applies one merge patch to a pod's labels, as the API
// server does: the labels in the patch are added to the pod's labels,
// and every other field of the pod stays.
func (f *fakeCluster) patchPodLabels(w http.ResponseWriter, r *http.Request, name string) {
	held, exists := f.pods[name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var patch struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	_ = json.NewDecoder(r.Body).Decode(&patch)
	if f.podPatchFails {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if held.Metadata.Labels == nil {
		held.Metadata.Labels = map[string]string{}
	}
	for key, value := range patch.Metadata.Labels {
		held.Metadata.Labels[key] = value
	}
	_ = json.NewEncoder(w).Encode(held)
}

// applyPlayer applies one server-side apply of spec.power to a Player.
// The API server would refuse to create a Player from the apply alone,
// because the schema requires a display or a sink, so an absent Player
// gets a 404 here. An accepted apply moves the version on.
func (f *fakeCluster) applyPlayer(w http.ResponseWriter, r *http.Request, name string) {
	var applied struct {
		Spec json.RawMessage `json:"spec"`
	}
	_ = json.NewDecoder(r.Body).Decode(&applied)
	f.playerApplies = append(f.playerApplies, playerApplied{
		fields:  string(applied.Spec),
		manager: r.URL.Query().Get("fieldManager"),
		force:   r.URL.Query().Get("force") == "true",
	})
	held, exists := f.players[name]
	switch {
	case f.playerApplyFails:
		w.WriteHeader(http.StatusInternalServerError)
		return
	case !exists:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var spec struct {
		Power string `json:"power"`
	}
	_ = json.Unmarshal(applied.Spec, &spec)
	held.Spec.Power = spec.Power
	version, _ := strconv.Atoi(held.Metadata.ResourceVersion)
	held.Metadata.ResourceVersion = strconv.Itoa(version + 1)
	_ = json.NewEncoder(w).Encode(held)
}

// apply applies one server-side apply to a Display. The override in the
// body replaces the Display's override, and an apply with no override
// removes it, as the API server removes a field that the applying
// manager owned and no longer sets.
func (f *fakeCluster) apply(w http.ResponseWriter, r *http.Request, name string) {
	held, exists := f.displays[name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var applied displayApply
	_ = json.NewDecoder(r.Body).Decode(&applied)
	if f.applyFails {
		f.applies = append(f.applies, displayApplied{
			name:     name,
			override: applied.Spec.Override,
			manager:  r.URL.Query().Get("fieldManager"),
		})
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	held.Spec.Override = applied.Spec.Override
	f.applies = append(f.applies, displayApplied{
		name:     name,
		override: applied.Spec.Override,
		manager:  r.URL.Query().Get("fieldManager"),
	})
	_ = json.NewEncoder(w).Encode(held)
}

// applySession applies one server-side apply of the status to a
// Receiver. The session in the body replaces the session in the status,
// and an apply with no session removes it.
func (f *fakeCluster) applySession(w http.ResponseWriter, r *http.Request, name string) {
	held, exists := f.receivers[name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var applied receiverStatusApply
	_ = json.NewDecoder(r.Body).Decode(&applied)
	f.sessions = append(f.sessions, receiverApplied{
		name:    name,
		session: applied.Status.Session,
		manager: r.URL.Query().Get("fieldManager"),
	})
	if f.sessionsFail {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	held.Status.Session = applied.Status.Session
	_ = json.NewEncoder(w).Encode(held)
}

// applySinkSession applies one server-side apply of the status to a
// Sink. The session in the body replaces the session in the status.
func (f *fakeCluster) applySinkSession(w http.ResponseWriter, r *http.Request, name string) {
	held, exists := f.sinks[name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var applied sinkStatusApply
	_ = json.NewDecoder(r.Body).Decode(&applied)
	f.sinkSessions = append(f.sinkSessions, sinkApplied{
		name:    name,
		session: applied.Status.Session,
		manager: r.URL.Query().Get("fieldManager"),
	})
	held.Status.Session = applied.Status.Session
	_ = json.NewEncoder(w).Encode(held)
}

// releaseSpecSession applies one server-side apply of the spec to a
// Receiver. The apply has no session, so the API server removes the
// spec.session that this manager owned.
func (f *fakeCluster) releaseSpecSession(w http.ResponseWriter, r *http.Request, name string) {
	held, exists := f.receivers[name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var applied receiverSpecApply
	_ = json.NewDecoder(r.Body).Decode(&applied)
	f.specReleases = append(f.specReleases, receiverApplied{
		name:    name,
		session: applied.Spec.Session,
		manager: r.URL.Query().Get("fieldManager"),
	})
	if f.sessionsFail {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	held.Spec.Session = applied.Spec.Session
	_ = json.NewEncoder(w).Encode(held)
}

// writeObject writes the object as JSON, or a 404 when the cluster does not
// have it, which is the answer that makes the operator create the
// object.
func writeObject[T any](w http.ResponseWriter, held *T) {
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

func testOperator(t *testing.T, cluster *fakeCluster, wake chan struct{}) *operator {
	t.Helper()
	client := testAPIClient(t, cluster.events.Around(cluster.handler(t)))
	media := &operator{
		client:       client,
		view:         cluster.view(),
		image:        "registry.example/player:test",
		idleImage:    testIdleImage,
		sidecarImage: "registry.example/sidecar:test",
		busAddress:   "bus.media.svc:1883",
		topicBase:    defaultTopicBase,
		// The bus is never run, so a publish finds a nil write queue
		// and is dropped. That is what a pass needs when the test has
		// no broker.
		bus:              newBus("bus.media.svc:1883", "media-operator-test", nil, nil, nil),
		reports:          newReports(wake),
		focus:            newFocusCache(wake),
		playerReceivers:  newPlayerReceivers(),
		peripherals:      newPeripheralCache(),
		codes:            newCodesCache(wake),
		panels:           newPanelCache(wake),
		panelOverrides:   map[string]panelOverride{},
		panelFaults:      map[string]string{},
		receiverSessions: map[string]receiverSession{},
		lastScreens:      map[string]lastScreen{},
		specReleased:     map[string]bool{},
		endingLabeled:    map[string]string{},
		positionWrites:   map[string]time.Time{},
		displayRestarts:  map[string]displayRestartMemo{},
		keysPublished:    map[string]string{},
		recreateBackoff:  map[string]backoffState{},
		replacements:     map[string]recreate{},
		wake:             wake,
		now:              time.Now,
		recorder: events.New(t.Context(), client, operatorComponent,
			events.Options{Instance: "media-operator-test", Log: io.Discard}),
	}
	media.levels = media.newVolumeEngine()
	media.reports.readPodsFrom(media.view)
	return media
}

func housePlay(uris ...string) *Play {
	items := make([]PlayItem, len(uris))
	for index, uri := range uris {
		items[index] = PlayItem{URI: uri}
	}
	return &Play{
		Metadata: ObjectMeta{Name: "movie", Namespace: "house", UID: "play-uid", ResourceVersion: "9"},
		Spec:     PlaySpec{Players: []string{"theater"}, Items: items},
	}
}

func housePlayer() *Player {
	return &Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house"},
		Spec: PlayerSpec{
			Zone:    "living-room",
			Display: &PlayerDevice{Class: "display-output"},
			Sinks:   []PlayerDevice{{Class: "audio-sink"}},
			Render:  &PlayerDevice{Class: "gpu-render"},
		},
	}
}

// housePlaybackPod builds the pod that a running Play already has, as
// an earlier pass or an earlier operator left it.
func housePlaybackPod() *Pod {
	play := housePlay("https://nas/film.mkv")
	pod := buildPod(play, buildClaim(play, housePlayer()),
		resolution{Items: []string{"https://nas/film.mkv"}},
		"registry.example/player:test", "registry.example/sidecar:test", "registry.example/display:test",
		"bus.media.svc:1883", defaultTopicBase, nil, resolvedPreferences{}, nil, "")
	pod.Status.Phase = podRunning
	return pod
}

// A pass writes each Player's status from the Plays it read. A Player
// with a running Play shows Playing and the name of that Play.
func TestAPassWritesThePlayerStatusFromItsRunningPlay(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	cluster.pods["movie-playback"] = housePlaybackPod()
	cluster.claims["movie-devices"] = buildClaim(cluster.plays["movie"], housePlayer())
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	got := cluster.players["theater"].Status
	want := PlayerStatus{Activity: playerPlaying, Play: "movie", Power: playerPowerOn}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("player status = %+v, want %+v", got, want)
	}
}

// A Player that no Play names shows Idle, and the pass writes that
// status, so an idle Player has a status in kubectl. The pass also
// adopts the Player's power.
func TestAPassMarksAPlayerWithNoPlayIdle(t *testing.T) {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	got := cluster.players["theater"].Status
	want := PlayerStatus{Activity: playerIdle, Power: playerPowerOn}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("player status = %+v, want %+v", got, want)
	}
}

func TestAPassCreatesTheClaimAndThePodAndReportsPending(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("nfs://nas/movies/film.mkv")
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	claim, held := cluster.claims["movie-devices"]
	if !held {
		t.Fatalf("no claim was created: %v", cluster.requests)
	}
	if got := claimRequests(claim); strings.Join(got, ",") != "screen,audio0,render" {
		t.Errorf("claim requests = %v, want screen, audio0, render", got)
	}
	pod, held := cluster.pods["movie-playback"]
	if !held {
		t.Fatalf("no pod was created: %v", cluster.requests)
	}
	if got := pod.Spec.Containers[0].Args; len(got) != 1 || got[0] != "/media/1/film.mkv" {
		t.Errorf("pod args = %v, want [/media/1/film.mkv]", got)
	}
	status := cluster.plays["movie"].Status
	if status.Phase != phasePending || status.Pod != "movie-playback" {
		t.Errorf("Play status = %+v, want phase Pending and pod movie-playback", status)
	}
}

// A second pass over the same Play creates nothing, because the claim
// and the pod already exist.
func TestASecondPassCreatesNothingNew(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()
	created := len(cluster.requests)
	media.pass()

	posts := 0
	for _, request := range cluster.requests[created:] {
		if strings.HasPrefix(request, http.MethodPost) {
			posts++
		}
	}
	if posts != 0 {
		t.Errorf("the second pass created objects: %v", cluster.requests[created:])
	}
}

func TestAPlayWhosePlayerIsAbsentStaysPending(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	status := cluster.plays["movie"].Status
	if status.Phase != phasePending {
		t.Errorf("phase = %q, want Pending", status.Phase)
	}
	if status.Message != "the Player theater does not exist in this namespace" {
		t.Errorf("message = %q, want that the Player theater does not exist in this namespace", status.Message)
	}
	if len(cluster.claims) != 0 || len(cluster.pods) != 0 {
		t.Errorf("a Play with no Player created objects: claims %v pods %v", cluster.claims, cluster.pods)
	}
}

// The resolver refuses the Play before any object exists, so a Play
// that names a scheme that this operator does not resolve, or a claim
// in another namespace, leaves no object.
func TestAPlayWithAURIItCannotResolveFailsAndCreatesNothing(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		want string
	}{
		{
			name: "an unknown scheme",
			uri:  "rtsp://camera/front",
			want: "the scheme rtsp:// is not one the operator resolves, which are https://, nfs://, claim://, and pattern://",
		},
		{
			name: "a claim in another namespace",
			uri:  "claim://studio/films/movies/film.mkv",
			want: `the URI "claim://studio/films/movies/film.mkv" names a claim in the namespace studio, ` +
				"and a Play in the namespace house mounts only a claim in its own namespace",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			cluster.plays["movie"] = housePlay(one.uri)
			cluster.players["theater"] = housePlayer()
			media := testOperator(t, cluster, make(chan struct{}, 1))

			media.pass()

			status := cluster.plays["movie"].Status
			if status.Phase != phaseFailed {
				t.Errorf("phase = %q, want Failed", status.Phase)
			}
			if status.Message != one.want {
				t.Errorf("message = %q, want %q", status.Message, one.want)
			}
			if len(cluster.claims) != 0 || len(cluster.pods) != 0 {
				t.Errorf("a refused URI created objects: claims %v pods %v", cluster.claims, cluster.pods)
			}
		})
	}
}

// A retired Play inside its time to live is left alone. The first pass
// adds only the finalizer, and every later pass sends the API server no
// request.
func TestARetiredPlayInsideItsWindowIsLeftAlone(t *testing.T) {
	cluster := newFakeCluster()
	finished := housePlay("https://nas/film.mkv")
	finished.Status = PlayStatus{
		Phase:      phaseFinished,
		Pod:        "movie-playback",
		FinishedAt: stampAgo(time.Minute),
	}
	cluster.plays["movie"] = finished
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()
	cluster.requests = nil
	media.pass()

	mustMatchAll(t, cluster.requests, nil)
}

func stampAgo(ago time.Duration) string {
	return time.Now().Add(-ago).UTC().Format(time.RFC3339)
}

// ttlSeconds returns a spec's ttlSecondsAfterFinished. The field is a
// pointer, so a test that needs zero uses this function and not nil.
func ttlSeconds(seconds int64) *int64 {
	return &seconds
}

// The film ended, so the playback pod and its claim are deleted, and
// the Play remains as the record of what played. The first pass sets
// the run's phase to Finished, and the pass that reads that phase
// retires it.
func TestAFinishedRunLosesItsPodAndItsClaimAndKeepsItsPlay(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.pods["movie-playback"].Status.Phase = podSucceeded
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()
	media.pass()

	if _, held := cluster.pods["movie-playback"]; held {
		t.Errorf("a finished run kept its pod: %v", cluster.pods)
	}
	if _, held := cluster.claims["movie-devices"]; held {
		t.Errorf("a finished run kept its claim: %v", cluster.claims)
	}
	play, held := cluster.plays["movie"]
	if !held {
		t.Fatal("the Play was deleted along with its pod, and its record of the run went with it")
	}
	mustMatch(t, play.Status.Phase, phaseFinished)
	mustMatch(t, play.Status.Pod, "movie-playback")
	if play.Status.FinishedAt == "" {
		t.Error("a retired Play has no status.finishedAt, so its retention window has no start time")
	}
}

// A Failed Play is not retired, so its pod and the pod's log remain for
// a person to debug from.
func TestAFailedRunKeepsItsPod(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.pods["movie-playback"].Status.Phase = podFailed
	media := testOperator(t, cluster, make(chan struct{}, 1))
	// The backoff blocks a recreate, so the pass leaves the failed pod.
	media.recreateBackoff[runKey("house", "movie")] = backoffState{
		count: 1,
		last:  time.Now(),
		next:  time.Now().Add(time.Hour),
	}

	media.pass()

	if _, held := cluster.pods["movie-playback"]; !held {
		t.Error("a failed run deleted its pod, and the pod log is the only record of the failure")
	}
	if got := countMethod(cluster.requests, http.MethodDelete); got != 0 {
		t.Errorf("a failed run deleted objects: %d deletes in %v", got, cluster.requests)
	}
	status := cluster.plays["movie"].Status
	mustMatch(t, status.Phase, phaseFailed)
	mustMatch(t, status.FinishedAt, "")
}

// finishedAt is written by the first pass that reads the Finished
// phase, and every later pass reads it and does not write a new one.
// The time to live counts from finishedAt, so a finishedAt that moved
// would keep a Finished Play forever.
func TestFinishedAtIsWrittenOnceAndNotOverwritten(t *testing.T) {
	cluster := newFakeCluster()
	finished := housePlay("https://nas/film.mkv")
	finished.Status = PlayStatus{Phase: phaseFinished, Pod: "movie-playback"}
	cluster.plays["movie"] = finished
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	if cluster.plays["movie"].Status.FinishedAt == "" {
		t.Fatal("the pass that read the Finished phase wrote no stamp")
	}

	stamped := stampAgo(time.Minute)
	cluster.plays["movie"].Status.FinishedAt = stamped
	media.pass()

	mustMatch(t, cluster.plays["movie"].Status.FinishedAt, stamped)
}

// The time to live is spec.ttlSecondsAfterFinished, or 300 seconds when
// the spec sets none. A spec that sets zero gets zero.
func TestThePlayWindowTakesTheSpecOverTheDefault(t *testing.T) {
	cases := []struct {
		name string
		ttl  *int64
		want time.Duration
	}{
		{name: "absent", ttl: nil, want: 300 * time.Second},
		{name: "zero", ttl: ttlSeconds(0), want: 0},
		{name: "an hour", ttl: ttlSeconds(3600), want: time.Hour},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			play := housePlay("https://nas/film.mkv")
			play.Spec.TTLSecondsAfterFinished = testCase.ttl
			mustMatch(t, playTTL(play), testCase.want)
		})
	}
}

// With a time to live of zero, the pass that reads the Finished phase
// deletes the Play and its pod together.
func TestAWindowOfZeroDeletesThePlayOnThePassThatReadsItFinished(t *testing.T) {
	cluster := newFakeCluster()
	finished := housePlay("https://nas/film.mkv")
	finished.Spec.TTLSecondsAfterFinished = ttlSeconds(0)
	finished.Status = PlayStatus{Phase: phaseFinished, Pod: "movie-playback"}
	cluster.plays["movie"] = finished
	cluster.players["theater"] = housePlayer()
	cluster.pods["movie-playback"] = housePlaybackPod()
	cluster.claims["movie-devices"] = buildClaim(finished, housePlayer())
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	if _, held := cluster.plays["movie"]; held {
		t.Error("a Play with a zero retention window still exists after it finished, want it deleted")
	}
	if _, held := cluster.pods["movie-playback"]; held {
		t.Error("the pod of a Play with a zero retention window still exists, want it deleted")
	}
}

// The time to live counts from finishedAt. The cases differ only in
// finishedAt.
func TestThePlayGoesOnceItsFinishedAtIsOlderThanItsWindow(t *testing.T) {
	cases := []struct {
		name     string
		finished time.Duration
		wantHeld bool
	}{
		{name: "inside the window", finished: 30 * time.Second, wantHeld: true},
		{name: "past the window", finished: 10 * time.Minute, wantHeld: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := newFakeCluster()
			finished := housePlay("https://nas/film.mkv")
			finished.Status = PlayStatus{
				Phase:      phaseFinished,
				Pod:        "movie-playback",
				FinishedAt: stampAgo(testCase.finished),
			}
			cluster.plays["movie"] = finished
			media := testOperator(t, cluster, make(chan struct{}, 1))

			media.pass()

			_, held := cluster.plays["movie"]
			mustMatch(t, held, testCase.wantHeld)
		})
	}
}

func TestARunningPodFoldsTheLatestReportIntoTheStatus(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()
	cluster.pods["movie-playback"].Status.Phase = podRunning
	media.reports.fold("house", "movie", playReport{
		Item:     1,
		Position: "0:03:20",
		Duration: "1:58:00",
	})
	media.pass()

	status := cluster.plays["movie"].Status
	want := PlayStatus{
		Phase:    phaseRunning,
		Activity: activityPlaying,
		Pod:      "movie-playback",
		Item:     1,
		Position: "0:03:20",
		Duration: "1:58:00",
	}
	if !reflect.DeepEqual(status, want) {
		t.Errorf("status = %+v, want %+v", status, want)
	}
}

func TestAPodThatSucceededFinishesThePlay(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()
	cluster.pods["movie-playback"].Status.Phase = podSucceeded
	media.pass()

	if got := cluster.plays["movie"].Status.Phase; got != phaseFinished {
		t.Errorf("phase = %q, want Finished", got)
	}
}

// A bare position advance is written at most once per
// positionWriteInterval.
func TestABarePositionAdvanceIsThrottled(t *testing.T) {
	cluster := newFakeCluster()
	running := PlayStatus{
		Phase: phaseRunning, Activity: activityPlaying, Pod: "movie-playback",
		Item: 1, Position: "0:01:00", Duration: "1:30:00",
	}
	play := housePlay("https://nas/film.mkv")
	play.Status = running
	cluster.plays["movie"] = play
	media := testOperator(t, cluster, make(chan struct{}, 1))
	key := runKey("house", "movie")

	// A position write just happened, so the advance one second later
	// waits.
	media.positionWrites[key] = time.Now()
	advanced := running
	advanced.Position = "0:01:01"
	if err := media.writePlay(play, advanced); err != nil {
		t.Fatalf("writing the Play status: %v", err)
	}
	if got := cluster.plays["movie"].Status.Position; got != "0:01:00" {
		t.Errorf("a bare advance wrote through the throttle: %q", got)
	}

	// Past the interval, the advance is written.
	media.positionWrites[key] = time.Now().Add(-2 * positionWriteInterval)
	advanced.Position = "0:01:12"
	if err := media.writePlay(play, advanced); err != nil {
		t.Fatalf("writing the Play status: %v", err)
	}
	if got := cluster.plays["movie"].Status.Position; got != "0:01:12" {
		t.Errorf("position = %q, want the advance to write past the interval", got)
	}
}

// A pause is what a person waits to see, so it is written at once, even
// when a position write just happened.
func TestAPauseWritesThroughTheThrottle(t *testing.T) {
	cluster := newFakeCluster()
	running := PlayStatus{
		Phase: phaseRunning, Activity: activityPlaying, Pod: "movie-playback",
		Item: 1, Position: "0:01:00", Duration: "1:30:00",
	}
	play := housePlay("https://nas/film.mkv")
	play.Status = running
	cluster.plays["movie"] = play
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.positionWrites[runKey("house", "movie")] = time.Now()

	paused := running
	paused.Paused = true
	paused.Activity = activityPaused
	paused.Position = "0:01:01"
	if err := media.writePlay(play, paused); err != nil {
		t.Fatalf("writing the Play status: %v", err)
	}
	if !cluster.plays["movie"].Status.Paused {
		t.Error("a pause did not write through the throttle")
	}
}

// A Play that left the cluster leaves nothing in the report cache.
func TestAPassForgetsAPlayThatIsGone(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.reports.fold("house", "movie", runningReport())

	media.pass()

	if got := media.reports.latestFor("house", "movie"); got != nil {
		t.Errorf("report = %+v, want the operator to have dropped it", *got)
	}
}

// houseRemote returns a Remote in the namespace that the house Player
// names in spec.remotes.
func houseRemote(keymap string) *Remote {
	return &Remote{
		Metadata: ObjectMeta{Name: "sofa", Namespace: "house"},
		Spec: RemoteSpec{
			Device: RemoteDevice{Class: "gamepad"},
			Keymap: keymap,
		},
	}
}

// housePlayerWithRemote returns the house Player with the Remote sofa
// in spec.remotes, so a Play on it wires that controller.
func housePlayerWithRemote() *Player {
	player := housePlayer()
	player.Spec.Remotes = []PlayerRemote{{Name: "sofa"}}
	return player
}

// A bound Remote is reconciled into its own persistent pod, and the
// command sidecar of the playback pod names the same events topic that
// the persistent pod publishes to, so the two meet on the bus.
func TestAPassWiresABoundRemote(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	// The persistent remote pod and its claim: one reader that the
	// Remote owns and that the claim ties to the controller.
	remotePod, held := cluster.pods["sofa-remote"]
	if !held {
		t.Fatalf("no reader pod was created: %v", cluster.requests)
	}
	if got := remotePod.Spec.Containers[0].Command; len(got) != 2 || got[1] != remoteMode {
		t.Errorf("reader pod command = %v, want the reader mode", got)
	}
	if _, held := cluster.claims["sofa-remote-devices"]; !held {
		t.Errorf("no Remote claim was created: %v", cluster.requests)
	}

	// The command sidecar of the playback pod has the Remote's events
	// and focus topics, so it reads what the persistent pod publishes
	// and is gated by the mark that the operator writes.
	playback, held := cluster.pods["movie-playback"]
	if !held {
		t.Fatalf("no playback pod was created: %v", cluster.requests)
	}
	command := initContainer(t, playback, commandContainer)
	mustMatch(t, envValue(command, remoteEventsTopicsVariable),
		remoteEventsTopic(defaultTopicBase, "house", "sofa"))
	mustMatch(t, envValue(command, remoteFocusTopicsVariable),
		remoteFocusTopic(defaultTopicBase, "house", "sofa"))
}

// connectedBus returns a bus with a session to a fake broker that
// accepts every publish, for a test that reads what the operator
// records as published. The operator records a payload only when the
// bus queued it, and a bus with no session queues nothing.
func connectedBus(t *testing.T) *Bus {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	go func() {
		for {
			select {
			case <-brokers[0].pubs:
			case <-t.Context().Done():
				return
			}
		}
	}()
	return bus
}

// The operator publishes each Remote's compiled table, retained, on
// that Remote's keys topic, so the persistent pod reads it as soon as
// it connects.
func TestAPassPublishesEachRemotesKeyTable(t *testing.T) {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.bus = connectedBus(t)

	media.pass()

	topic := remoteKeysTopic(defaultTopicBase, "house", "sofa")
	published, held := media.keysPublished[topic]
	mustMatch(t, held, true)
	var table []compiledBinding
	mustSucceed(t, json.Unmarshal([]byte(published), &table))
	mustMatch(t, len(table), len(baseKeys)+2)
	mustMatch(t, table[0], compiledBinding{EventType: evKey, Code: 0x130, Value: 1, Key: "KEY_PLAYPAUSE"})
}

// A Remote that is gone leaves no retained table: the pass clears the
// topic that it wrote.
func TestAPassClearsTheKeyTableOfADepartedRemote(t *testing.T) {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.pass()

	delete(cluster.remotes, "sofa")
	media.pass()

	mustMatch(t, len(media.keysPublished), 0)
}

// A pass writes the focused Player into the Remote's status, so kubectl
// shows which Player a controller drives.
func TestAPassWritesTheFocusedPlayerOntoTheRemote(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		cluster.players["theater"] = housePlayerWithRemote()
		cluster.remotes["sofa"] = houseRemote("gamepad")
		cluster.keymaps["gamepad"] = testKeymap()
		media, _ := caughtUpOperator(t, cluster)

		media.pass()

		mustMatch(t, cluster.remotes["sofa"].Status.Player, "theater")
	})
}

// A pass writes the Peripheral of the device that the Remote's
// persistent claim allocated, so kubectl shows the record of the
// controller's link from the Remote. A controller whose claim has no
// allocation gets no Peripheral name, and neither does one that was
// allocated from another driver.
func TestAPassNamesTheRemotesPeripheral(t *testing.T) {
	cases := []struct {
		name    string
		results []DeviceRequestAllocationResult
		want    string
	}{
		{
			name: "a bonded controller",
			results: []DeviceRequestAllocationResult{
				{Driver: bluetoothDriver, Pool: "node-1", Device: "aa-bb-cc-dd-ee-ff"},
			},
			want: "aa-bb-cc-dd-ee-ff",
		},
		{
			name: "a controller from another driver",
			results: []DeviceRequestAllocationResult{
				{Driver: "input.liken.sh", Pool: "node-1", Device: "event3"},
			},
		},
		{name: "a claim the scheduler has not allocated"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := newFakeCluster()
			cluster.players["theater"] = housePlayerWithRemote()
			cluster.remotes["sofa"] = houseRemote("gamepad")
			cluster.keymaps["gamepad"] = testKeymap()
			claim := buildRemoteClaim(houseRemote("gamepad"))
			if each.results != nil {
				claim.Status = &ResourceClaimStatus{
					Allocation: &DeviceAllocationResult{
						Devices: DeviceAllocationDevices{Results: each.results},
					},
				}
			}
			cluster.claims[claim.Metadata.Name] = claim
			media := testOperator(t, cluster, make(chan struct{}, 1))

			media.pass()

			mustMatch(t, cluster.remotes["sofa"].Status.Peripheral, each.want)
		})
	}
}

// bondedRemote returns the cluster that a bonded controller makes: a
// Player that lists the Remote, the Remote, its allocated claim, and
// the device's Peripheral.
func bondedRemote(t *testing.T) *fakeCluster {
	t.Helper()
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	claim := buildRemoteClaim(houseRemote("gamepad"))
	claim.Status = &ResourceClaimStatus{
		Allocation: &DeviceAllocationResult{
			Devices: DeviceAllocationDevices{Results: []DeviceRequestAllocationResult{
				{Driver: bluetoothDriver, Pool: "node-1", Device: "aa-bb-cc-dd-ee-ff"},
			}},
		},
	}
	cluster.claims[claim.Metadata.Name] = claim
	peripheral := bonded("aa-bb-cc-dd-ee-ff", conditionTrue)
	cluster.peripherals[peripheral.Metadata.Name] = &peripheral
	return cluster
}

// A pass reads a Remote's persistent claim from the view. The read that
// finds the controller's Peripheral is the read that the persistent
// reconcile uses, so a settled controller costs the API server no
// request.
func TestAPassReadsARemotesClaimFromTheView(t *testing.T) {
	cluster, media := settledHouse(t, make(chan struct{}, 8))
	cluster.requests = nil

	media.pass()

	mustMatch(t, countPathRequests(cluster.requests, "GET "+claimsPath("house")+"/"+remoteClaimName("sofa")), 0)
	mustMatch(t, cluster.remotes["sofa"].Status.Peripheral, "aa-bb-cc-dd-ee-ff")
}

// A claim read that fails leaves no entry, so the persistent reconcile
// makes its own read and does not act on a claim that nothing read. The
// controller gets no Peripheral name in that pass.
func TestAPassNamesNoPeripheralWhenTheClaimReadFailed(t *testing.T) {
	cluster := bondedRemote(t)
	cluster.fails[claimsPath("house")+"/"+remoteClaimName("sofa")] = true
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	mustMatch(t, cluster.remotes["sofa"].Status.Peripheral, "")
}

// When the Peripherals list fails, the cache keeps what the last pass
// read, so one failed read does not blank every controller on the idle
// screen.
func TestAPassKeepsThePeripheralsOfTheLastReadWhenTheListFails(t *testing.T) {
	cluster := bondedRemote(t)
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.pass()

	cluster.fails[peripheralsPath] = true
	media.pass()

	connected, held := media.peripherals.connectedFor("aa-bb-cc-dd-ee-ff")
	mustMatch(t, held, true)
	mustMatch(t, connected, true)
}

// When the Remotes list fails, the pass skips the whole Remote half, so
// no cache shrinks to a collection that the operator could not read.
func TestAPassSkipsTheRemotesWhenTheirListFails(t *testing.T) {
	cluster := bondedRemote(t)
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.bus = connectedBus(t)
	media.pass()
	mustMatch(t, cluster.remotes["sofa"].Status.Peripheral, "aa-bb-cc-dd-ee-ff")

	cluster.fails[remotesAllPath] = true
	media.pass()

	mustMatch(t, media.peripherals.peripheralFor(controllerKey("house", "sofa")),
		"aa-bb-cc-dd-ee-ff")
	mustMatch(t, len(media.keysPublished), 1)
}

// A status that already shows the current Player is not written again,
// so a settled Remote costs the API server no request on each pass.
func TestAPassSkipsAnUnchangedRemoteStatus(t *testing.T) {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()
	settled := len(cluster.requests)
	media.pass()

	for _, request := range cluster.requests[settled:] {
		mustMatch(t, strings.HasSuffix(request, "/remotes/sofa/status"), false)
	}
}

// The pass reports the codes that the compiled table leaves unbound in
// the Remote's status, and a status that already has the same codes is
// not written again.
func TestAPassWritesTheUnboundCodesOntoTheRemote(t *testing.T) {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = &Keymap{
		Metadata: ObjectMeta{Name: "gamepad"},
		Spec:     KeymapSpec{Buttons: []KeymapButton{{Press: "BTN_EAST", Key: keyNone}}},
	}
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.codes.setCodes(controllerKey("house", "sofa"), remoteCodes{
		Keys: []uint16{0x0a4, 0x130, 0x131},
		Axes: []uint16{0x10, 0x11},
	})

	media.pass()

	want := []UnboundCode{{Code: 0x131, Name: "BTN_EAST", Type: unboundKey}}
	if !reflect.DeepEqual(cluster.remotes["sofa"].Status.Unbound, want) {
		t.Errorf("unbound = %+v, want %+v", cluster.remotes["sofa"].Status.Unbound, want)
	}

	settled := len(cluster.requests)
	media.pass()
	for _, request := range cluster.requests[settled:] {
		mustMatch(t, strings.HasSuffix(request, "/remotes/sofa/status"), false)
	}
}

// A Remote with no Keymap uses the base table, and the base leaves
// nothing unbound: every key code maps to itself and the hats are the
// arrows.
func TestARemoteWithNoKeymapReportsNothingUnbound(t *testing.T) {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("")
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.codes.setCodes(controllerKey("house", "sofa"), remoteCodes{
		Keys: []uint16{0x130},
		Axes: []uint16{0x10},
	})

	media.pass()

	if got := cluster.remotes["sofa"].Status.Unbound; got != nil {
		t.Errorf("unbound = %+v, want none", got)
	}
}

// A codes document reaches the codes cache only through the bus, and
// the pod's own clear empties it.
func TestTheCodesTopicReachesTheCodesCacheAndItsClearEmptiesIt(t *testing.T) {
	wake := make(chan struct{}, 1)
	media := &operator{topicBase: defaultTopicBase, codes: newCodesCache(wake)}
	topic := remoteCodesTopic(defaultTopicBase, "house", "sofa")

	media.handleBusMessage(topic, []byte(`{"keys":[304]}`))
	codes, held := media.codes.codesFor(controllerKey("house", "sofa"))
	mustMatch(t, held, true)
	mustMatchAll(t, codes.Keys, []uint16{0x130})

	media.handleBusMessage(topic, nil)
	_, stillHeld := media.codes.codesFor(controllerKey("house", "sofa"))
	mustMatch(t, stillHeld, false)
}

// The Keymap is not part of the pod, so a Keymap edit during a film
// recreates no pod and the run keeps its status.
func TestAKeymapEditAfterTheRunStartedLeavesThePlayAlone(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()
	cluster.pods["movie-playback"].Status.Phase = podRunning
	edited := testKeymap()
	edited.Spec.Buttons[0].Key = "KEY_MUTE"
	cluster.keymaps["gamepad"] = edited
	created := len(cluster.requests)
	media.pass()

	status := cluster.plays["movie"].Status
	if status.Phase != phaseRunning {
		t.Errorf("phase = %q, want Running", status.Phase)
	}
	if got := countMethod(cluster.requests[created:], http.MethodDelete); got != 0 {
		t.Errorf("a keymap edit recreated the pod: %d deletes in %v", got, cluster.requests[created:])
	}
}

// A pod's containers cannot change, so a Player edit that changes the
// pod's claim or Remotes makes the operator recreate the pod at the
// film's position. The tests below start from a running Play, change
// the Player, and check what the reconcile does.

// runningCluster returns a cluster with a Play that is already running
// on the given Player: the Play, the Player, and the pod and claim that
// an earlier pass built from it.
func runningCluster(player *Player) *fakeCluster {
	cluster := newFakeCluster()
	play := housePlay("https://nas/film.mkv")
	play.Status = PlayStatus{Phase: phaseRunning, Activity: activityPlaying, Pod: "movie-playback"}
	cluster.plays["movie"] = play
	cluster.players["theater"] = player
	pod := buildPod(play, buildClaim(play, player),
		resolution{Items: []string{"https://nas/film.mkv"}},
		"registry.example/player:test", "registry.example/sidecar:test", "registry.example/display:test",
		"bus.media.svc:1883", defaultTopicBase, nil, resolvedPreferences{}, nil, "")
	pod.Status.Phase = podRunning
	cluster.pods["movie-playback"] = pod
	cluster.claims["movie-devices"] = buildClaim(play, player)
	return cluster
}

// brightPlayer is the house Player with a display parameter that the
// house Player does not have, so the claim that it produces differs
// from the existing one.
func brightPlayer() *Player {
	player := housePlayer()
	player.Spec.Display = &PlayerDevice{
		Class: "display-output",
		Parameters: &DeviceParameters{
			Driver: "display.liken.sh",
			Values: json.RawMessage(`{"brightness":80}`),
		},
	}
	return player
}

// podStart returns the start that the player container has: empty on an
// ordinary run and the saved position on a recreate.
func podStart(pod *Pod) string {
	for _, variable := range pod.Spec.Containers[0].Env {
		if variable.Name == playStartVariable {
			return variable.Value
		}
	}
	return ""
}

func countMethod(requests []string, method string) int {
	count := 0
	for _, request := range requests {
		if strings.HasPrefix(request, method) {
			count++
		}
	}
	return count
}

// A changed device parameter reshapes the claim, so the operator
// recreates the pod, and mpv starts at the position that the bus
// reported.
func TestAPlayerParameterChangeRecreatesThePodAtThePositionTheBusReported(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.players["theater"] = brightPlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.reports.fold("house", "movie", playReport{Item: 1, Position: "0:15:00", Duration: "1:58:00"})

	media.pass()

	pod, held := cluster.pods["movie-playback"]
	if !held {
		t.Fatalf("the playback pod does not exist: %v", cluster.requests)
	}
	if got := podStart(pod); got != "0:15:00" {
		t.Errorf("recreated start = %q, want the stashed 0:15:00", got)
	}
	if got := countMethod(cluster.requests, http.MethodDelete); got == 0 {
		t.Errorf("the pod was not recreated: no delete in %v", cluster.requests)
	}
	claim := cluster.claims["movie-devices"]
	if len(claim.Spec.Devices.Config) == 0 {
		t.Errorf("the recreated claim has no device configuration: %+v, want parameters", claim.Spec.Devices)
	}
}

// A recreate starts at the position from the first source that has one:
// the bus report, then the Play's status, then spec.start for a run
// that never reported a position.
func TestARecreatePreservesTheFilmsPlaceFromEachSource(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeCluster, *operator)
		want  string
	}{{
		name: "the retained bus status",
		setup: func(c *fakeCluster, o *operator) {
			c.plays["movie"].Status.Position = "0:02:00"
			o.reports.fold("house", "movie", playReport{Item: 1, Position: "0:15:00"})
		},
		want: "0:15:00",
	}, {
		name: "the last written status",
		setup: func(c *fakeCluster, o *operator) {
			c.plays["movie"].Status.Position = "0:20:00"
		},
		want: "0:20:00",
	}, {
		name: "the spec start when nothing reported",
		setup: func(c *fakeCluster, o *operator) {
			c.plays["movie"].Spec.Start = "0:05:00"
			c.pods["movie-playback"].Status.Phase = podPending
		},
		want: "0:05:00",
	}}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := runningCluster(housePlayer())
			cluster.players["theater"] = brightPlayer()
			media := testOperator(t, cluster, make(chan struct{}, 1))
			one.setup(cluster, media)

			media.pass()

			if got := podStart(cluster.pods["movie-playback"]); got != one.want {
				t.Errorf("recreated start = %q, want %q", got, one.want)
			}
		})
	}
}

// A Player edit that changes neither the claim nor the Remote set, such
// as a change of spec.zone, recreates nothing.
func TestANonShapingPlayerEditRecreatesNothing(t *testing.T) {
	cluster := runningCluster(housePlayer())
	zoned := housePlayer()
	zoned.Spec.Zone = "den"
	cluster.players["theater"] = zoned
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	if got := countMethod(cluster.requests, http.MethodDelete); got != 0 {
		t.Errorf("a non-shaping edit deleted %d objects: %v", got, cluster.requests)
	}
	if got := podStart(cluster.pods["movie-playback"]); got != "" {
		t.Errorf("the pod was recreated: start = %q, want none", got)
	}
}

// remoteChangeCluster returns a cluster with a Play that runs with the
// sofa controller, under a Player that a person has since edited to
// name the armchair controller instead.
func remoteChangeCluster() *fakeCluster {
	cluster := newFakeCluster()
	play := housePlay("https://nas/film.mkv")
	play.Status = PlayStatus{Phase: phaseRunning, Activity: activityPlaying, Pod: "movie-playback"}
	cluster.plays["movie"] = play

	player := housePlayerWithRemote()
	cluster.players["theater"] = player
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.remotes["armchair"] = &Remote{
		Metadata: ObjectMeta{Name: "armchair", Namespace: "house"},
		Spec:     RemoteSpec{Device: RemoteDevice{Class: "gamepad"}, Keymap: "gamepad"},
	}
	cluster.keymaps["gamepad"] = testKeymap()

	sofa := []boundRemote{{
		Name:        "sofa",
		EventsTopic: remoteEventsTopic(defaultTopicBase, "house", "sofa"),
		FocusTopic:  remoteFocusTopic(defaultTopicBase, "house", "sofa"),
	}}
	pod := buildPod(play, buildClaim(play, player),
		resolution{Items: []string{"https://nas/film.mkv"}},
		"registry.example/player:test", "registry.example/sidecar:test", "registry.example/display:test",
		"bus.media.svc:1883", defaultTopicBase, sofa, resolvedPreferences{}, nil, "")
	pod.Status.Phase = podRunning
	cluster.pods["movie-playback"] = pod
	cluster.claims["movie-devices"] = buildClaim(play, player)

	player.Spec.Remotes = []PlayerRemote{{Name: "armchair"}}
	return cluster
}

// A Player that now names a different controller changes the Remote
// set, so the operator recreates the pod at the film's position, with
// the wiring of the new controller's sidecar.
func TestAPlayerRemoteChangeRecreatesThePodOnTheChangedRemoteSet(t *testing.T) {
	cluster := remoteChangeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.reports.fold("house", "movie", playReport{Item: 1, Position: "0:30:00"})
	media.pass()

	topics := podRemoteTopics(cluster.pods["movie-playback"])
	want := remoteEventsTopic(defaultTopicBase, "house", "armchair")
	if strings.Join(topics, ",") != want {
		t.Errorf("recreated remote set = %v, want [%s]", topics, want)
	}
	if got := podStart(cluster.pods["movie-playback"]); got != "0:30:00" {
		t.Errorf("recreated start = %q, want the stashed 0:30:00", got)
	}
}

// Kubernetes removed the playback pod of a running Play: a device taint
// evicted it, or its node was lost. The Play is not Finished, so the
// operator recreates the pod at the film's saved position.
func TestAMissingPodForALiveRunIsRecreatedAtTheSavedPosition(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.plays["movie"].Status.Position = "0:41:00"
	delete(cluster.pods, "movie-playback")
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	pod, held := cluster.pods["movie-playback"]
	if !held {
		t.Fatalf("the evicted pod was not recreated: %v", cluster.requests)
	}
	if got := podStart(pod); got != "0:41:00" {
		t.Errorf("recreated start = %q, want the stashed 0:41:00", got)
	}
}

// mpv exited with a non-zero status and the pod is Failed. The operator
// recreates it at the film's position, as a Job restarts a failed pod.
func TestAFailedPodIsRecreatedAtTheSavedPosition(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.pods["movie-playback"].Status.Phase = podFailed
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.reports.fold("house", "movie", playReport{Item: 1, Position: "0:22:00"})

	media.pass()

	if got := countMethod(cluster.requests, http.MethodDelete); got == 0 {
		t.Errorf("the failed pod was not recreated: no delete in %v", cluster.requests)
	}
	if got := podStart(cluster.pods["movie-playback"]); got != "0:22:00" {
		t.Errorf("recreated start = %q, want the stashed 0:22:00", got)
	}
}

// mpv exited with status zero and the pod Succeeded, so the film ended.
// A finished film is final, so the operator recreates nothing and the
// Play shows Finished.
func TestASucceededPodIsNotRecreated(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.pods["movie-playback"].Status.Phase = podSucceeded
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	if got := countMethod(cluster.requests, http.MethodDelete); got != 0 {
		t.Errorf("a finished film recreated its pod: %d deletes in %v", got, cluster.requests)
	}
	if got := cluster.plays["movie"].Status.Phase; got != phaseFinished {
		t.Errorf("phase = %q, want Finished", got)
	}
}

// A Finished Play whose pod garbage collection already removed is left
// alone. The film ended, so the operator does not bring the pod back.
func TestAMissingPodForAFinishedPlayIsNotRecreated(t *testing.T) {
	cluster := newFakeCluster()
	finished := housePlay("https://nas/film.mkv")
	finished.Status = PlayStatus{Phase: phaseFinished, Pod: "movie-playback", Position: "1:58:00"}
	cluster.plays["movie"] = finished
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	if _, held := cluster.pods["movie-playback"]; held {
		t.Errorf("a finished play's pod was recreated: %v", cluster.pods)
	}
	if got := countMethod(cluster.requests, http.MethodPost); got != 0 {
		t.Errorf("a finished play created objects: %d posts in %v", got, cluster.requests)
	}
}

// ensurePlayback reports a new run, which takes over its controllers'
// focus, only when the run has no resume point.
func TestOnlyANewRunTakesOverControllersAndAResumeDoesNot(t *testing.T) {
	cases := []struct {
		name      string
		position  string
		wantFresh bool
	}{
		{name: "a new run takes over the controllers", position: "", wantFresh: true},
		{name: "a resume does not take them", position: "0:10:00", wantFresh: false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			play := housePlay("https://nas/film.mkv")
			play.Status.Position = one.position
			cluster.plays["movie"] = play
			cluster.players["theater"] = housePlayer()
			media := testOperator(t, cluster, make(chan struct{}, 1))
			claim := buildClaim(play, housePlayer())

			_, fresh, err := media.ensurePlayback(play, housePlayer(), claim,
				resolution{Items: []string{"https://nas/film.mkv"}}, resolvedPreferences{}, nil, false)
			if err != nil {
				t.Fatalf("ensuring the playback pod: %v", err)
			}
			if fresh != one.wantFresh {
				t.Errorf("fresh = %v, want %v", fresh, one.wantFresh)
			}
		})
	}
}

// The recreate backoff limits how fast a pod that keeps failing comes
// back. The first recreate is immediate, a quick second one is blocked,
// and the delay doubles from the base up to the cap and stays at the
// cap.
func TestTheBackoffBoundsTheRecreateRate(t *testing.T) {
	media := testOperator(t, newFakeCluster(), make(chan struct{}, 1))
	key := runKey("house", "movie")

	if !media.mayResume(key) {
		t.Fatal("the first recreate was blocked, want it immediate")
	}
	if media.mayResume(key) {
		t.Error("a rapid second recreate was allowed, want it blocked by backoff")
	}

	// Each recreate after its deadline raises the count, and the delay
	// doubles up to the cap and stays there.
	for range 10 {
		state := media.recreateBackoff[key]
		state.next = time.Now().Add(-time.Millisecond)
		media.recreateBackoff[key] = state
		if !media.mayResume(key) {
			t.Fatal("a recreate past its deadline was blocked")
		}
	}
	if got := backoffDelay(media.recreateBackoff[key].count); got != recreateBackoffCap {
		t.Errorf("delay after a long loop = %v, want the cap %v", got, recreateBackoffCap)
	}

	// A run with no recreate for recreateBackoffReset starts the count
	// again.
	state := media.recreateBackoff[key]
	state.last = time.Now().Add(-2 * recreateBackoffReset)
	media.recreateBackoff[key] = state
	if !media.mayResume(key) {
		t.Error("a recreate after a stable run was blocked, want it immediate")
	}
	if got := media.recreateBackoff[key].count; got != 1 {
		t.Errorf("count after the reset = %d, want it started over at 1", got)
	}
}

func TestBackoffDelayDoublesToTheCap(t *testing.T) {
	cases := []struct {
		count int
		want  time.Duration
	}{
		{count: 1, want: recreateBackoffBase},
		{count: 2, want: 2 * recreateBackoffBase},
		{count: 3, want: 4 * recreateBackoffBase},
		{count: 100, want: recreateBackoffCap},
	}
	for _, one := range cases {
		if got := backoffDelay(one.count); got != one.want {
			t.Errorf("backoffDelay(%d) = %v, want %v", one.count, got, one.want)
		}
	}
}

// The command sidecar clears the retained status when it terminates and
// marks the run offline, which drops the latest report in reports. The
// Play's own status still has the last position, so a resume after an
// eviction starts where the film was and does not fall back to the
// start.
func TestTheStartPositionFallsBackToThePlayStatusWhenTheReportIsCleared(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	play := housePlay("https://nas/film.mkv")
	play.Status.Position = "0:41:00"

	media.reports.availability("house", "movie", false, "")

	if got := media.reports.latestFor("house", "movie"); got != nil {
		t.Fatalf("the operator still holds a report: %+v, want none", *got)
	}
	position, ok := media.resumePoint(play)
	if !ok || position != "0:41:00" {
		t.Errorf("resumePoint = %q, %v, want 0:41:00, true", position, ok)
	}
	if got := media.savedPosition(play); got != "0:41:00" {
		t.Errorf("stashedPosition = %q, want the status position 0:41:00", got)
	}
}

// A Player that names a Remote that does not exist fails the Play
// before any pod exists, and the message names the Player and the
// missing Remote.
func TestAPlayWhosePlayerNamesAMissingRemoteFailsBeforeItsPod(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	player := housePlayer()
	player.Spec.Remotes = []PlayerRemote{{Name: "ghost"}}
	cluster.players["theater"] = player
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	status := cluster.plays["movie"].Status
	if status.Phase != phaseFailed {
		t.Errorf("phase = %q, want Failed", status.Phase)
	}
	want := "the Player theater lists the Remote ghost in spec.remotes, and no Remote of that name exists in this namespace"
	if status.Message != want {
		t.Errorf("message = %q, want %q", status.Message, want)
	}
	if _, held := cluster.pods["movie-playback"]; held {
		t.Errorf("a missing remote created the playback pod: %v", cluster.pods)
	}
}

// An empty availability payload is the clear of a retained topic, so it
// does not mark the run as seen (handleBusMessage). An offline payload
// does.
func TestAnEmptyAvailabilityDoesNotMarkARunSeen(t *testing.T) {
	wake := make(chan struct{}, 1)
	media := &operator{
		topicBase: defaultTopicBase,
		reports:   newReports(wake),
		focus:     newFocusCache(wake),
	}
	topic := playAvailabilityTopic(defaultTopicBase, "den", "old")

	media.handleBusMessage(topic, nil)
	if got := media.reports.stale(map[string]bool{}); len(got) != 0 {
		t.Errorf("an empty availability message marked runs %v as stale, want none", got)
	}

	media.handleBusMessage(topic, []byte(availabilityOffline))
	if got := media.reports.stale(map[string]bool{}); len(got) != 1 || got[0] != runKey("den", "old") {
		t.Errorf("an offline availability message marked runs %v as stale, want the run den/old", got)
	}
}

// A new broker session may have none of the operator's retained state,
// so a reconnect clears the record of published key tables, and the
// next pass writes them again. It does not write the focus marks:
// reconcileFocus publishes only the marks that the session did not send
// back.
func TestAReconnectRewritesTheKeyTablesAndNoMark(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 1, nil, nil)
		waitForConnect(t, connected)
		broker := brokers[0]
		wake := make(chan struct{}, 1)
		media := &operator{
			topicBase: defaultTopicBase,
			bus:       bus,
			focus:     newFocusCache(wake),
			keysPublished: map[string]string{
				remoteKeysTopic(defaultTopicBase, "house", "sofa"): "already-published",
			},
		}
		media.focus.setMark(controllerKey("den", "sofa"), "theater")

		media.reestablishRetained()

		if len(media.keysPublished) != 0 {
			t.Errorf("keysPublished is %v, want it empty so the key tables are published again", media.keysPublished)
		}
		mustPublishNothing(t, broker)
	})
}

// keysOperator returns an operator with a bus connected to a fake
// broker, so a test reads what the pass publishes on a Remote's keys
// topic.
func keysOperator(t *testing.T, cluster *fakeCluster) (*operator, *fakeBroker) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	return &operator{
		client:        testAPIClient(t, cluster.handler(t)),
		view:          cluster.view(),
		topicBase:     defaultTopicBase,
		bus:           bus,
		keysPublished: map[string]string{},
	}, brokers[0]
}

// keysRemote is the fixture of the publish tests: one Remote and the
// Keymap it names, which compiles to a table.
func keysRemote(cluster *fakeCluster) *Remote {
	cluster.keymaps["gamepad"] = testKeymap()
	remote := houseRemote("gamepad")
	cluster.remotes["sofa"] = remote
	return remote
}

// The pass compiles a Remote's table and publishes it retained, so the
// persistent pod reads the current table as soon as it connects.
func TestPublishKeysPublishesARetainedTable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		media, broker := keysOperator(t, cluster)

		media.publishKeys(keysRemote(cluster), media.loadKeymaps(), map[string]bool{})

		published := waitForPublish(t, broker.pubs)
		mustMatch(t, published.topic, remoteKeysTopic(defaultTopicBase, "house", "sofa"))
		mustMatch(t, published.retained, true)
		var table []compiledBinding
		mustSucceed(t, json.Unmarshal(published.payload, &table))
		mustMatch(t, len(table), len(baseKeys)+2)
	})
}

// The keys topic is retained, so an unchanged table is not published
// again on a later pass, and the broker keeps sending the table that it
// has. A changed Keymap is published.
func TestPublishKeysRepublishesOnlyOnChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		media, broker := keysOperator(t, cluster)
		remote := keysRemote(cluster)

		media.publishKeys(remote, media.loadKeymaps(), map[string]bool{})
		waitForPublish(t, broker.pubs)

		media.publishKeys(remote, media.loadKeymaps(), map[string]bool{})
		select {
		case got := <-broker.pubs:
			t.Fatalf("an unchanged table republished %+v", got)
		case <-time.After(50 * time.Millisecond):
		}

		keymap := cluster.keymaps["gamepad"]
		keymap.Spec.Buttons = keymap.Spec.Buttons[:1]
		media.publishKeys(remote, media.loadKeymaps(), map[string]bool{})

		changed := waitForPublish(t, broker.pubs)
		mustMatch(t, changed.topic, remoteKeysTopic(defaultTopicBase, "house", "sofa"))
	})
}

// A Keymap that does not compile publishes nothing, so the last good
// retained table stays and the controller keeps working.
func TestPublishKeysPublishesNothingForAKeymapThatWillNotCompile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		cluster.keymaps["broken"] = &Keymap{
			Metadata: ObjectMeta{Name: "broken"},
			Spec:     KeymapSpec{Buttons: []KeymapButton{{Press: "BTN_NOPE", Key: "KEY_ENTER"}}},
		}
		cluster.remotes["sofa"] = houseRemote("broken")
		media, broker := keysOperator(t, cluster)

		table := media.publishKeys(cluster.remotes["sofa"], media.loadKeymaps(), map[string]bool{})

		if table != nil {
			t.Errorf("table = %+v, want none", table)
		}
		select {
		case got := <-broker.pubs:
			t.Fatalf("a Keymap that will not compile published %+v", got)
		case <-time.After(50 * time.Millisecond):
		}
	})
}

// playersOperator returns an operator with a bus connected to a fake
// broker, so a test reads what the Player reconcile publishes. The idle
// display class is unset, so reconcileIdle builds nothing and the test
// reads only the status publish.
func playersOperator(t *testing.T, cluster *fakeCluster) (*operator, *fakeBroker) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	return &operator{
		client:          testAPIClient(t, cluster.handler(t)),
		view:            cluster.view(),
		topicBase:       defaultTopicBase,
		bus:             bus,
		reports:         newReports(nil),
		focus:           newFocusCache(nil),
		playerReceivers: newPlayerReceivers(),
		peripherals:     newPeripheralCache(),
		codes:           newCodesCache(nil),
		panels:          newPanelCache(nil),
		// These tests read what the status publish leaves on the bus.
		// catchUpEnds stays zero, so the operator treats the broker as
		// not caught up and publishes no level. The relay tests build
		// their own operator with the grace over.
		levels: newVolumeEngine(nil, func(string, []byte) {}, io.Discard),
	}, brokers[0]
}

// The sidecar's ending report makes the operator publish the Player as
// Idle while the pod still runs and the Play still shows Running, so
// the idle screen draws over the film as it ends and does not wait for
// the pod to terminate.
func TestAReportedEndingPublishesTheIdleStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		media, broker := playersOperator(t, cluster)
		player := Player{
			Metadata: ObjectMeta{Name: "theater", Namespace: "house"},
			Status:   PlayerStatus{Activity: playerPlaying, Play: "movie"},
		}
		play := Play{
			Metadata: ObjectMeta{Name: "movie", Namespace: "house"},
			Spec:     PlaySpec{Players: []string{"theater"}},
			Status:   PlayStatus{Phase: phaseRunning},
		}

		media.handleBusMessage(playStatusTopic(defaultTopicBase, "house", "movie"),
			[]byte(`{"item":1,"position":"0:20:00","ended":true}`))
		media.reconcilePlayers([]Player{player}, []Play{play}, "", nil)

		status := waitForPublish(t, broker.pubs)
		mustMatch(t, status.topic, playerStatusTopic(defaultTopicBase, "house", "theater"))
		var state playerBusStatus
		mustSucceed(t, json.Unmarshal(status.payload, &state))
		mustMatch(t, state.Activity, playerIdle)
		mustMatch(t, state.Play == nil, true)

		// The Play is still what the API server has, so kubectl still
		// lists the run for the seconds that the pod takes to
		// terminate.
		mustMatch(t, play.Status.Phase, phaseRunning)
	})
}

// A pass publishes each Player's bus status retained, so an idle pod
// that just started reads the current state from the broker.
func TestAPassPublishesTheRetainedPlayerStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		media, broker := playersOperator(t, cluster)
		player := settledPlayer(housePlayerWithRemote())
		player.Spec.DisplayName = "Studio Lab"

		media.reconcilePlayers([]Player{player}, nil, "", nil)

		published := waitForPublish(t, broker.pubs)
		mustMatch(t, published.topic, playerStatusTopic(defaultTopicBase, "house", "theater"))
		mustMatch(t, published.retained, true)
		var status playerBusStatus
		mustSucceed(t, json.Unmarshal(published.payload, &status))
		mustMatch(t, status.DisplayName, "Studio Lab")
		mustMatch(t, status.Activity, playerIdle)
		mustMatchAll(t, componentNames(status), []string{"display-output", "audio-sink", "sofa"})
	})
}

// A Player whose bus status did not change is not published again,
// because the broker keeps the retained payload. A new
// spec.displayName is published, so a rename reaches the screen without
// a pod restart.
func TestThePlayerStatusRepublishesOnlyOnChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		media, broker := playersOperator(t, cluster)
		player := settledPlayer(housePlayer())

		media.reconcilePlayers([]Player{player}, nil, "", nil)
		waitForPublish(t, broker.pubs)

		media.reconcilePlayers([]Player{player}, nil, "", nil)
		mustPublishNothing(t, broker)

		player.Spec.DisplayName = "Studio Lab"
		media.reconcilePlayers([]Player{player}, nil, "", nil)
		changed := waitForPublish(t, broker.pubs)
		mustMatch(t, changed.topic, playerStatusTopic(defaultTopicBase, "house", "theater"))
	})
}

// The link and the charge that a controller's Peripheral reports reach
// the remote component of the Player's bus status. A device that
// disconnects shows as disconnected on the next pass.
func TestAPeripheralReachesThePublishedPlayerStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		media, broker := playersOperator(t, cluster)
		key := controllerKey("house", "sofa")
		charged := bonded("aa-bb-cc-dd-ee-ff", conditionTrue)
		charged.Status.Battery = &PeripheralBattery{Percentage: 62}
		media.peripherals = holding(key, charged)
		player := settledPlayer(housePlayerWithRemote())

		media.reconcilePlayers([]Player{player}, nil, "", nil)

		var status playerBusStatus
		mustSucceed(t, json.Unmarshal(waitForPublish(t, broker.pubs).payload, &status))
		remote := status.Components[len(status.Components)-1]
		mustMatch(t, remote.Kind, remoteComponent)
		mustMatch(t, remote.Connected != nil && *remote.Connected, true)
		mustMatch(t, remote.Battery != nil && *remote.Battery == 62, true)

		media.peripherals = holding(key, bonded("aa-bb-cc-dd-ee-ff", "False"))
		media.reconcilePlayers([]Player{player}, nil, "", nil)

		mustSucceed(t, json.Unmarshal(waitForPublish(t, broker.pubs).payload, &status))
		remote = status.Components[len(status.Components)-1]
		mustMatch(t, remote.Connected != nil && !*remote.Connected, true)
	})
}

// A Player that the cluster no longer has gets its retained status
// cleared with an empty publish, so a deleted Player leaves nothing on
// the bus for a subscriber to draw.
func TestADeletedPlayerClearsItsRetainedStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		media, broker := playersOperator(t, cluster)

		media.reconcilePlayers([]Player{settledPlayer(housePlayer())}, nil, "", nil)
		waitForPublish(t, broker.pubs)

		media.reconcilePlayers(nil, nil, "", nil)

		cleared := waitForPublish(t, broker.pubs)
		mustMatch(t, cleared.topic, playerStatusTopic(defaultTopicBase, "house", "theater"))
		mustMatch(t, len(cleared.payload), 0)
		mustMatch(t, cleared.retained, true)
	})
}

// caughtUpOperator returns a whole operator connected to a fake broker,
// with the broker's catch-up already over, so a test reads the levels
// and focus marks that a pass publishes for Players that the broker has
// nothing for.
func caughtUpOperator(t *testing.T, cluster *fakeCluster) (*operator, *fakeBroker) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.bus = bus
	media.catchUpEnds = time.Now()
	return media, brokers[0]
}

// theaterVolumeTopic is the volume topic of the Player that the relay
// tests publish for.
func theaterVolumeTopic() string {
	return playerVolumeTopic(defaultTopicBase, "house", "theater")
}

// mustPublishVolume reads publishes until the Player's level arrives,
// and returns it, skipping the Player status messages that a pass also
// publishes.
func mustPublishVolume(t *testing.T, broker *fakeBroker) brokerPublish {
	t.Helper()
	for {
		published := waitForPublish(t, broker.pubs)
		if published.topic == theaterVolumeTopic() {
			return published
		}
	}
}

// mustPublishNoVolume drains the broker once every goroutine in the
// test's synctest bubble has settled, and fails on any message that
// reached the Player's volume topic.
func mustPublishNoVolume(t *testing.T, broker *fakeBroker) {
	t.Helper()
	synctest.Wait()
	for {
		select {
		case got := <-broker.pubs:
			if got.topic == theaterVolumeTopic() {
				t.Fatalf("a level reached the bus: %+v", got)
			}
		default:
			return
		}
	}
}

// settledPlayer returns a Player that is already idle, so a pass over
// it sees no Play end and publishes only its status.
func settledPlayer(player *Player) Player {
	player.Status = PlayerStatus{Activity: playerIdle}
	return *player
}

// mustPublishNothing checks that nothing reached the broker, after
// every goroutine in the test's synctest bubble has settled.
func mustPublishNothing(t *testing.T, broker *fakeBroker) {
	t.Helper()
	synctest.Wait()
	select {
	case got := <-broker.pubs:
		t.Fatalf("a publish reached the bus: %+v", got)
	default:
	}
}

// playOnPlayer returns a Play on the named Player, created at the given
// time. The pass orders two Plays on one Player by the creation time
// and then the name.
func playOnPlayer(name, player, created string) *Play {
	play := housePlay("https://nas/film.mkv")
	play.Metadata.Name = name
	play.Metadata.CreationTimestamp = created
	play.Spec.Players = []string{player}
	play.Status.Phase = phaseRunning
	return play
}

// A Player runs one Play, so when two unfinished Plays name it, the
// newest runs and the pass deletes the other.
func TestAPassDeletesEveryOlderPlayOnOnePlayer(t *testing.T) {
	cases := []struct {
		name   string
		older  *Play
		newer  *Play
		wanted string
	}{
		{
			name:   "the newer creation time wins",
			older:  playOnPlayer("early", "theater", "2026-09-08T10:00:00Z"),
			newer:  playOnPlayer("late", "theater", "2026-09-08T11:00:00Z"),
			wanted: "late",
		},
		{
			name:   "the later name breaks a tie",
			older:  playOnPlayer("alpha", "theater", "2026-09-08T10:00:00Z"),
			newer:  playOnPlayer("omega", "theater", "2026-09-08T10:00:00Z"),
			wanted: "omega",
		},
		{
			name:   "a Play with no stamp is the older one",
			older:  playOnPlayer("unstamped", "theater", ""),
			newer:  playOnPlayer("stamped", "theater", "2026-09-08T10:00:00Z"),
			wanted: "stamped",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			cluster.plays[one.older.Metadata.Name] = one.older
			cluster.plays[one.newer.Metadata.Name] = one.newer
			cluster.players["theater"] = housePlayer()
			media := testOperator(t, cluster, make(chan struct{}, 1))

			media.pass()

			if _, exists := cluster.plays[one.wanted]; !exists {
				t.Errorf("the pass deleted the newest Play %q, want it kept", one.wanted)
			}
			if len(cluster.plays) != 1 {
				t.Errorf("plays = %v, want only %q", sortedNames(cluster.plays), one.wanted)
			}
		})
	}
}

// The only Play on a Player is the newest, so nothing deletes it.
func TestAPassKeepsTheOnlyPlayOnAPlayer(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = playOnPlayer("movie", "theater", "2026-09-08T10:00:00Z")
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	if _, exists := cluster.plays["movie"]; !exists {
		t.Error("the pass deleted the only Play on the unit")
	}
}

// Each Player is ordered on its own. Two Plays on two Players are two
// runs, and neither ends the other.
func TestAPassLeavesPlaysOnOtherPlayersAlone(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["theater-film"] = playOnPlayer("theater-film", "theater", "2026-09-08T10:00:00Z")
	cluster.plays["kitchen-film"] = playOnPlayer("kitchen-film", "kitchen", "2026-09-08T11:00:00Z")
	cluster.players["theater"] = housePlayer()
	kitchen := housePlayer()
	kitchen.Metadata.Name = "kitchen"
	cluster.players["kitchen"] = kitchen
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	mustMatchAll(t, sortedNames(cluster.plays), []string{"kitchen-film", "theater-film"})
}

// Garbage collection keeps a deleting Play's pod until the finalizer
// clears, so the operator deletes the pod itself, and the pod of the
// next run can take the claim.
func TestAPassDeletesThePodOfADeletingPlay(t *testing.T) {
	cluster := newFakeCluster()
	play := playOnPlayer("movie", "theater", "2026-09-08T10:00:00Z")
	play.Metadata.DeletionTimestamp = "2026-09-08T10:30:00Z"
	cluster.plays["movie"] = play
	cluster.players["theater"] = housePlayer()
	cluster.pods["movie-playback"] = housePlaybackPod()
	cluster.claims["movie-devices"] = buildClaim(play, housePlayer())
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()
	if _, exists := cluster.pods["movie-playback"]; exists {
		t.Fatal("the pod of the Play with a deletion timestamp still exists after the pass, want it deleted")
	}

	// A second pass shows that the reconcile does not build the pod
	// again for a Play that is being deleted.
	media.pass()
	if _, exists := cluster.pods["movie-playback"]; exists {
		t.Error("the pass recreated the pod of a Play that has a deletion timestamp, want none")
	}
}

// A deleting Play is not the run on its Player, so the Play that
// follows it is kept and gets its own pod.
func TestADeletingPlayDoesNotSupersedeTheNextOne(t *testing.T) {
	cluster := newFakeCluster()
	leaving := playOnPlayer("leaving", "theater", "2026-09-08T11:00:00Z")
	leaving.Metadata.DeletionTimestamp = "2026-09-08T11:30:00Z"
	cluster.plays["leaving"] = leaving
	cluster.plays["arriving"] = playOnPlayer("arriving", "theater", "2026-09-08T10:00:00Z")
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	if _, exists := cluster.plays["arriving"]; !exists {
		t.Fatal("the pass deleted the Play arriving, want it kept")
	}
	if _, held := cluster.pods["arriving-playback"]; !held {
		t.Error("the Play arriving has no pod, want one created")
	}
}
