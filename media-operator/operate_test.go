package main

// These tests run whole passes against a small API server that holds
// the kinds this operator reads and writes, so a pass is proved by what
// it left behind.

import (
	"encoding/json"
	"net/http"
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// The fake cluster: one map per kind, and the list of requests a pass
// made.
type fakeCluster struct {
	plays      map[string]*Play
	players    map[string]*Player
	remotes    map[string]*Remote
	keymaps    map[string]*Keymap
	mediaprefs map[string]*MediaPreferences
	claims     map[string]*ResourceClaim
	pods       map[string]*Pod
	requests   []string

	// The hardware the display-operator publishes: one Display
	// per panel, and the slices that name each allocated device. applies
	// is every apply this operator sent, in order, the refused ones
	// included.
	displays map[string]*Display
	slices   []ResourceSlice
	applies  []displayApplied

	// The bonded devices the bluetooth-operator publishes, one per
	// controller, keyed by the device's address in its lowercase dashed
	// form.
	peripherals map[string]*Peripheral

	// applyFails is a Display the API server refuses to write,
	// the failure a pass answers by leaving the panel where it stands
	// and trying again.
	applyFails bool

	// The collection paths this server refuses, so a test proves what a
	// pass does with a read it cannot make.
	fails map[string]bool
}

// One apply the operator made: the Display it named, the block
// it wrote, and the field manager it wrote under.
type displayApplied struct {
	name     string
	override *DisplayOverride
	manager  string
}

func newFakeCluster() *fakeCluster {
	return &fakeCluster{
		plays:      map[string]*Play{},
		players:    map[string]*Player{},
		remotes:    map[string]*Remote{},
		keymaps:    map[string]*Keymap{},
		mediaprefs: map[string]*MediaPreferences{},
		claims:     map[string]*ResourceClaim{},
		pods:       map[string]*Pod{},
		displays:   map[string]*Display{},

		peripherals: map[string]*Peripheral{},
		fails:       map[string]bool{},
	}
}

func (f *fakeCluster) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		if f.fails[r.URL.Path] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		name := path.Base(r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == playsPath:
			// The list is sorted so one pass reads the collection in one
			// order every time.
			list := PlayList{Metadata: ListMeta{ResourceVersion: "1"}}
			for _, key := range sortedNames(f.plays) {
				list.Items = append(list.Items, *f.plays[key])
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/players/") && strings.HasSuffix(r.URL.Path, "/status"):
			var written Player
			_ = json.NewDecoder(r.Body).Decode(&written)
			f.players[written.Metadata.Name] = &written
			_ = json.NewEncoder(w).Encode(written)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/remotes/") && strings.HasSuffix(r.URL.Path, "/status"):
			var written Remote
			_ = json.NewDecoder(r.Body).Decode(&written)
			f.remotes[written.Metadata.Name] = &written
			_ = json.NewEncoder(w).Encode(written)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/status"):
			var written Play
			_ = json.NewDecoder(r.Body).Decode(&written)
			f.plays[written.Metadata.Name] = &written
			_ = json.NewEncoder(w).Encode(written)
		case r.Method == http.MethodGet && r.URL.Path == playersPath:
			list := PlayerList{Metadata: ListMeta{ResourceVersion: "1"}}
			for _, key := range sortedNames(f.players) {
				list.Items = append(list.Items, *f.players[key])
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/plays/"):
			answer(w, f.plays[name])
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/players/"):
			answer(w, f.players[name])
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/remotes"):
			list := RemoteList{Metadata: ListMeta{ResourceVersion: "1"}}
			for _, key := range sortedNames(f.remotes) {
				list.Items = append(list.Items, *f.remotes[key])
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/remotes/"):
			answer(w, f.remotes[name])
		case r.Method == http.MethodGet && r.URL.Path == keymapsPath:
			list := KeymapList{Metadata: ListMeta{ResourceVersion: "1"}}
			for _, key := range sortedNames(f.keymaps) {
				list.Items = append(list.Items, *f.keymaps[key])
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/keymaps/"):
			answer(w, f.keymaps[name])
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/mediapreferences/"):
			answer(w, f.mediaprefs[name])
		case r.Method == http.MethodGet && r.URL.Path == slicesPath:
			_ = json.NewEncoder(w).Encode(ResourceSliceList{
				Metadata: ListMeta{ResourceVersion: "1"}, Items: f.slices})
		case r.Method == http.MethodGet && r.URL.Path == peripheralsPath:
			list := PeripheralList{Metadata: ListMeta{ResourceVersion: "1"}}
			for _, key := range sortedNames(f.peripherals) {
				list.Items = append(list.Items, *f.peripherals[key])
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/displays/"):
			answer(w, f.displays[name])
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/displays/"):
			f.apply(w, r, name)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/resourceclaims/"):
			answer(w, f.claims[name])
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/resourceclaims"):
			claim := &ResourceClaim{}
			_ = json.NewDecoder(r.Body).Decode(claim)
			f.claims[claim.Metadata.Name] = claim
			_ = json.NewEncoder(w).Encode(claim)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods/"):
			answer(w, f.pods[name])
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pods"):
			pod := &Pod{}
			_ = json.NewDecoder(r.Body).Decode(pod)
			f.pods[pod.Metadata.Name] = pod
			_ = json.NewEncoder(w).Encode(pod)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/plays/"):
			delete(f.plays, name)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/pods/"):
			delete(f.pods, name)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/resourceclaims/"):
			delete(f.claims, name)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// apply folds one server-side apply onto a Display. The
// override the body carries replaces the one the Display holds, and an
// apply with no override lifts it, which is what the API server does
// with a field the applying manager owned and no longer states.
func (f *fakeCluster) apply(w http.ResponseWriter, r *http.Request, name string) {
	held, standing := f.displays[name]
	if !standing {
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

// An object the cluster does not hold is a 404, which is the answer the
// operator creates against.
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

func testOperator(t *testing.T, cluster *fakeCluster, wake chan struct{}) *operator {
	t.Helper()
	return &operator{
		client:       testAPIClient(t, cluster.handler(t)),
		image:        "registry.example/player:test",
		idleImage:    testIdleImage,
		sidecarImage: "registry.example/sidecar:test",
		busAddress:   "bus.media.svc:1883",
		topicBase:    defaultTopicBase,
		// The bus is never Run, so a publish finds a nil write queue and
		// drops, which is what a pass wants with no broker under the test.
		bus:                   newBus("bus.media.svc:1883", "media-operator-test", nil, nil, nil),
		reports:               newReports(wake),
		focus:                 newFocusDesk(wake),
		peripherals:           newPeripheralDesk(),
		codes:                 newCodesDesk(wake),
		panels:                newPanelDesk(wake),
		panelOverrides:        map[string]panelOverride{},
		panelFaults:           map[string]string{},
		volumes:               newVolumeDesk(),
		positionWrites:        map[string]time.Time{},
		keysPublished:         map[string]string{},
		playerStatusPublished: map[string]string{},
		playReclaim:           map[string]time.Time{},
		recreateBackoff:       map[string]backoffState{},
		wake:                  wake,
	}
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

// housePlaybackPod builds the pod a running Play already has, the way a
// previous pass or a previous operator left it.
func housePlaybackPod() *Pod {
	play := housePlay("https://nas/film.mkv")
	pod := buildPod(play, buildClaim(play, housePlayer()),
		resolution{Items: []string{"https://nas/film.mkv"}},
		"registry.example/player:test", "registry.example/sidecar:test", "bus.media.svc:1883", defaultTopicBase, nil, resolvedPreferences{})
	pod.Status.Phase = podRunning
	return pod
}

// A pass writes each Player's status from the Plays it read. A player
// with a running play reads Playing and names the play.
func TestAPassWritesThePlayerStatusFromItsRunningPlay(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	cluster.pods["movie-playback"] = housePlaybackPod()
	cluster.claims["movie-devices"] = buildClaim(cluster.plays["movie"], housePlayer())
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	got := cluster.players["theater"].Status
	want := PlayerStatus{Activity: playerPlaying, Play: "movie"}
	if got != want {
		t.Errorf("player status = %+v, want %+v", got, want)
	}
}

// A Player no Play names reads Idle, and the pass writes it, so an idle
// unit is not blank in kubectl.
func TestAPassMarksAPlayerWithNoPlayIdle(t *testing.T) {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	got := cluster.players["theater"].Status
	want := PlayerStatus{Activity: playerIdle}
	if got != want {
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
		t.Errorf("requests = %v", got)
	}
	pod, held := cluster.pods["movie-playback"]
	if !held {
		t.Fatalf("no pod was created: %v", cluster.requests)
	}
	if got := pod.Spec.Containers[0].Args; len(got) != 1 || got[0] != "/media/1/film.mkv" {
		t.Errorf("args = %v", got)
	}
	status := cluster.plays["movie"].Status
	if status.Phase != phasePending || status.Pod != "movie-playback" {
		t.Errorf("status = %+v", status)
	}
}

// A second pass over the same Play creates nothing; the claim and the
// pod are already there.
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
		t.Errorf("message = %q", status.Message)
	}
	if len(cluster.claims) != 0 || len(cluster.pods) != 0 {
		t.Errorf("a Play with no Player created objects: claims %v pods %v", cluster.claims, cluster.pods)
	}
}

// The resolver refuses before any object exists, so a Play that names a
// scheme this operator does not resolve leaves nothing behind.
func TestAPlayWithAnUnknownSchemeFailsAndCreatesNothing(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("rtsp://camera/front")
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	status := cluster.plays["movie"].Status
	if status.Phase != phaseFailed {
		t.Errorf("phase = %q, want Failed", status.Phase)
	}
	want := "the scheme rtsp:// is not one the operator resolves; it resolves https://, nfs://, and claim://"
	if status.Message != want {
		t.Errorf("message = %q, want %q", status.Message, want)
	}
	if len(cluster.claims) != 0 || len(cluster.pods) != 0 {
		t.Errorf("a refused URI created objects: claims %v pods %v", cluster.claims, cluster.pods)
	}
}

// A Play the operator already retired, still inside its window, is read
// and left alone; the pass makes no request about the play itself, only the
// reads every pass makes: the four collection lists and the default
// MediaPreferences.
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

	want := "GET " + playsPath +
		",GET " + mediaPrefsPath + "/" + mediaPreferencesName +
		",GET " + remotesAllPath + ",GET " + peripheralsPath +
		",GET " + playersPath + ",GET " + keymapsPath
	if strings.Join(cluster.requests, ",") != want {
		t.Errorf("requests = %v, want the collection lists and the default MediaPreferences", cluster.requests)
	}
}

// stampAgo is a finishedAt the given time before now, in the form the
// operator writes and reads back.
func stampAgo(ago time.Duration) string {
	return time.Now().Add(-ago).UTC().Format(time.RFC3339)
}

// ttlSeconds is a spec's ttlSecondsAfterFinished. The field is a pointer,
// so a test that means zero says so with this and not with a nil.
func ttlSeconds(seconds int64) *int64 {
	return &seconds
}

// The film ended, so the playback pod and its claim go and the Play stands
// with the record of what played. The first pass folds the run to Finished,
// and the pass that reads that phase retires it.
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
		t.Fatal("the Play went with its pod, and the record of the run with it")
	}
	mustMatch(t, play.Status.Phase, phaseFinished)
	mustMatch(t, play.Status.Pod, "movie-playback")
	if play.Status.FinishedAt == "" {
		t.Error("a retired Play carries no finishedAt, so its window has no clock")
	}
}

// A Failed pod stands. Its log is the evidence a person debugs from, and
// the run resumes rather than ends, so nothing about a Failed Play is
// retired.
func TestAFailedRunKeepsItsPod(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.pods["movie-playback"].Status.Phase = podFailed
	media := testOperator(t, cluster, make(chan struct{}, 1))
	// The run already recreated once, so the backoff holds the next
	// recreate off and this pass leaves the dead pod where it is.
	media.recreateBackoff[runKey("house", "movie")] = backoffState{
		count: 1,
		last:  time.Now(),
		next:  time.Now().Add(time.Hour),
	}

	media.pass()

	if _, held := cluster.pods["movie-playback"]; !held {
		t.Error("a failed run lost the pod whose log is the evidence")
	}
	if got := countMethod(cluster.requests, http.MethodDelete); got != 0 {
		t.Errorf("a failed run deleted objects: %d deletes in %v", got, cluster.requests)
	}
	status := cluster.plays["movie"].Status
	mustMatch(t, status.Phase, phaseFailed)
	mustMatch(t, status.FinishedAt, "")
}

// The stamp is written on the pass that first reads the Finished phase, and
// every pass after it reads that stamp instead of writing a fresh one. The
// window counts from the end of the film, so a stamp that moved would hold
// a Finished Play forever.
func TestTheFinishedStampIsWrittenOnceAndNotOverwritten(t *testing.T) {
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

// The window is the seconds the spec states, and 300 when it states none.
// Zero is a value a spec may hold, so it reads as zero and not as absent.
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

// A window of zero has already passed the moment the run finishes, so the
// Play and its pod go together on the pass that reads the Finished phase.
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
		t.Error("a Play with a zero window stood after it finished")
	}
	if _, held := cluster.pods["movie-playback"]; held {
		t.Error("the pod of a Play with a zero window stood")
	}
}

// The window is measured from the finishedAt stamp. The two cases are the
// same Play with the same spec, and only the stamp differs, so nothing but
// the stamp puts one Play past its window and leaves the other inside it.
func TestThePlayGoesOnceItsStampIsOlderThanItsWindow(t *testing.T) {
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

// The command sidecar publishes a live position to the bus every second,
// but the operator writes a bare position advance to the resource no
// more than once per positionWriteInterval, so a status write does not
// wake the operator's own watch a second later and spin the loop.
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

	// A position write just happened, so the next second's advance waits.
	media.positionWrites[key] = time.Now()
	advanced := running
	advanced.Position = "0:01:01"
	if err := media.writePlay(play, advanced); err != nil {
		t.Fatal(err)
	}
	if got := cluster.plays["movie"].Status.Position; got != "0:01:00" {
		t.Errorf("a bare advance wrote through the throttle: %q", got)
	}

	// Past the interval, the advance writes.
	media.positionWrites[key] = time.Now().Add(-2 * positionWriteInterval)
	advanced.Position = "0:01:12"
	if err := media.writePlay(play, advanced); err != nil {
		t.Fatal(err)
	}
	if got := cluster.plays["movie"].Status.Position; got != "0:01:12" {
		t.Errorf("position = %q, want the advance to write past the interval", got)
	}
}

// A pause is what a person waits to see, so it writes at once even when a
// position write just happened.
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
		t.Fatal(err)
	}
	if !cluster.plays["movie"].Status.Paused {
		t.Error("a pause did not write through the throttle")
	}
}

// A Play that left the cluster leaves nothing on the report desk.
func TestAPassForgetsAPlayThatIsGone(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.reports.fold("house", "movie", runningReport())

	media.pass()

	if got := media.reports.latestFor("house", "movie"); got != nil {
		t.Errorf("report = %+v, want the desk to have forgotten it", *got)
	}
}

// A Remote in the namespace, named by the house player through its
// spec.remotes.
func houseRemote(keymap string) *Remote {
	return &Remote{
		Metadata: ObjectMeta{Name: "sofa", Namespace: "house"},
		Spec: RemoteSpec{
			Device: RemoteDevice{Class: "gamepad"},
			Keymap: keymap,
		},
	}
}

// The house player, naming the "sofa" Remote, so a Play on it wires
// that controller.
func housePlayerWithRemote() *Player {
	player := housePlayer()
	player.Spec.Remotes = []PlayerRemote{{Name: "sofa"}}
	return player
}

// A bound Remote reconciles into its own standing pod, and the playback
// pod's command sidecar names the same events topic the standing pod
// publishes to, so the two meet on the bus.
func TestAPassWiresABoundRemote(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	// The standing remote pod and its claim, one reader owned by the
	// Remote and pinned to the controller by the claim.
	remotePod, held := cluster.pods["sofa-remote"]
	if !held {
		t.Fatalf("no standing remote pod was created: %v", cluster.requests)
	}
	if got := remotePod.Spec.Containers[0].Command; len(got) != 2 || got[1] != remoteMode {
		t.Errorf("standing pod command = %v, want the reader mode", got)
	}
	if _, held := cluster.claims["sofa-remote-devices"]; !held {
		t.Errorf("no standing remote claim was created: %v", cluster.requests)
	}

	// The playback pod's command sidecar carries the remote's events and
	// focus topics, so it reads what the standing pod publishes and gates
	// on the mark the operator writes.
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

// The operator publishes each Remote's compiled table on that Remote's
// own keys topic, retained, so the standing pod reads it the instant it
// connects.
func TestAPassPublishesEachRemotesKeyTable(t *testing.T) {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	topic := remoteKeysTopic(defaultTopicBase, "house", "sofa")
	published, held := media.keysPublished[topic]
	mustMatch(t, held, true)
	var table []compiledBinding
	mustSucceed(t, json.Unmarshal([]byte(published), &table))
	mustMatch(t, len(table), len(baseKeys)+2)
	mustMatch(t, table[0], compiledBinding{EventType: evKey, Code: 0x130, Value: 1, Key: "KEY_PLAYPAUSE"})
}

// A Remote that is gone leaves no retained table behind: the pass
// clears the topic it wrote.
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

// A pass writes the focused Player onto the Remote's status, so
// kubectl answers which unit a controller drives.
func TestAPassWritesTheFocusedPlayerOntoTheRemote(t *testing.T) {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	mustMatch(t, cluster.remotes["sofa"].Status.Player, "theater")
}

// A pass writes the Peripheral of the device the Remote's standing claim
// allocated, so kubectl reaches the record of the controller's link from
// the Remote. A controller whose claim carries no allocation names none,
// and one allocated from another driver names none either.
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

// bondedRemote is the cluster a bonded controller makes: a unit that
// lists it, the Remote, its allocated claim, and the device's Peripheral.
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

// A pass reads a Remote's standing claim once. The read that resolves the
// controller's Peripheral is the read the standing reconcile uses, so a
// controller costs the API server one claim read a pass however many
// things need the claim.
func TestAPassReadsARemotesClaimOnce(t *testing.T) {
	cluster := bondedRemote(t)
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	read := "GET " + claimsPath("house") + "/" + remoteClaimName("sofa")
	reads := 0
	for _, request := range cluster.requests {
		if request == read {
			reads++
		}
	}
	mustMatch(t, reads, 1)
}

// A claim read that fails carries no entry, so the standing reconcile
// makes its own read rather than acting on a claim nothing read. The
// controller names no Peripheral on that pass.
func TestAPassReadsAClaimItselfWhenTheFirstReadFailed(t *testing.T) {
	cluster := bondedRemote(t)
	read := claimsPath("house") + "/" + remoteClaimName("sofa")
	cluster.fails[read] = true
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	reads := 0
	for _, request := range cluster.requests {
		if request == "GET "+read {
			reads++
		}
	}
	mustMatch(t, reads, 2)
	mustMatch(t, cluster.remotes["sofa"].Status.Peripheral, "")
}

// A peripherals list that fails leaves the desk holding what the last
// pass read, so one failed read does not blank every controller on the
// idle screen.
func TestAPassKeepsThePeripheralsItHeldWhenTheListFails(t *testing.T) {
	cluster := bondedRemote(t)
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.pass()

	cluster.fails[peripheralsPath] = true
	media.pass()

	connected, held := media.peripherals.connectedFor("aa-bb-cc-dd-ee-ff")
	mustMatch(t, held, true)
	mustMatch(t, connected, true)
}

// A remotes list that fails skips the whole Remote half of the pass, so
// no desk shrinks to a collection the operator could not read.
func TestAPassSkipsTheRemotesWhenTheirListFails(t *testing.T) {
	cluster := bondedRemote(t)
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.pass()
	mustMatch(t, cluster.remotes["sofa"].Status.Peripheral, "aa-bb-cc-dd-ee-ff")

	cluster.fails[remotesAllPath] = true
	media.pass()

	mustMatch(t, media.peripherals.peripheralFor(controllerKey("house", "sofa")),
		"aa-bb-cc-dd-ee-ff")
	mustMatch(t, len(media.keysPublished), 1)
}

// A status that already reads the current Player is not written
// again, so a settled Remote costs the API server nothing per pass.
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

// The pass reports the gap against the compiled table on the Remote,
// and a status that already reads the same gap is not written again.
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

// A Remote with no Keymap runs on the base, and the base leaves
// nothing unbound: every key code passes as itself and the hats are
// the arrows.
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

// A codes document reaches the desk through the bus alone, and the
// pod's own clear empties it.
func TestTheCodesTopicReachesTheDeskAndItsClearEmptiesIt(t *testing.T) {
	wake := make(chan struct{}, 1)
	media := &operator{topicBase: defaultTopicBase, codes: newCodesDesk(wake)}
	topic := remoteCodesTopic(defaultTopicBase, "house", "sofa")

	media.handleBusMessage(topic, []byte(`{"keys":[304]}`))
	codes, held := media.codes.codesFor(controllerKey("house", "sofa"))
	mustMatch(t, held, true)
	mustMatchAll(t, codes.Keys, []uint16{0x130})

	media.handleBusMessage(topic, nil)
	_, stillHeld := media.codes.codesFor(controllerKey("house", "sofa"))
	mustMatch(t, stillHeld, false)
}

// A Keymap edit is bus state, not pod shape. The container set is fixed
// once the pod runs, so a Keymap changed after the film started recreates
// no pod and the run keeps its status.
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

// A Player edit that reshapes the pod reaches a running Play. The
// container set is immutable, so the operator recreates the pod, and it
// recreates gracefully so the film keeps its place. These tests seed a
// running Play with the pod and claim a previous pass built, change the
// Player, and prove what the reconcile does.

// runningCluster seeds a Play already running on the given Player: the
// play, the player, and the pod and claim a previous pass built from it.
func runningCluster(player *Player) *fakeCluster {
	cluster := newFakeCluster()
	play := housePlay("https://nas/film.mkv")
	play.Status = PlayStatus{Phase: phaseRunning, Activity: activityPlaying, Pod: "movie-playback"}
	cluster.plays["movie"] = play
	cluster.players["theater"] = player
	pod := buildPod(play, buildClaim(play, player),
		resolution{Items: []string{"https://nas/film.mkv"}},
		"registry.example/player:test", "registry.example/sidecar:test", "bus.media.svc:1883", defaultTopicBase, nil, resolvedPreferences{})
	pod.Status.Phase = podRunning
	cluster.pods["movie-playback"] = pod
	cluster.claims["movie-devices"] = buildClaim(play, player)
	return cluster
}

// brightPlayer is the house player with a display parameter the house
// player does not carry, so the claim it produces diverges.
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

// podStart reads the start the player container carries, which is empty
// on an ordinary run and set to the stashed position on a recreate.
func podStart(pod *Pod) string {
	for _, variable := range pod.Spec.Containers[0].Env {
		if variable.Name == playStartVariable {
			return variable.Value
		}
	}
	return ""
}

// countMethod counts the requests that used one HTTP method.
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
// recreates the pod, and it starts mpv at the position the bus reported.
func TestAPlayerParameterChangeRecreatesThePodAtTheStashedPosition(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.players["theater"] = brightPlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.reports.fold("house", "movie", playReport{Item: 1, Position: "0:15:00", Duration: "1:58:00"})

	media.pass()

	pod, held := cluster.pods["movie-playback"]
	if !held {
		t.Fatalf("the pod is gone: %v", cluster.requests)
	}
	if got := podStart(pod); got != "0:15:00" {
		t.Errorf("recreated start = %q, want the stashed 0:15:00", got)
	}
	if got := countMethod(cluster.requests, http.MethodDelete); got == 0 {
		t.Errorf("the pod was not recreated: no delete in %v", cluster.requests)
	}
	claim := cluster.claims["movie-devices"]
	if len(claim.Spec.Devices.Config) == 0 {
		t.Errorf("the recreated claim carries no parameters: %+v", claim.Spec.Devices)
	}
}

// The recreate reads the film's place from the first source that has it:
// the retained bus status, then the last written status, then the Play's
// own spec.start when a pod that never reported is reshaped at startup.
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

// A Player edit that changes neither the claim nor the remote set, a
// spec.zone rename, recreates nothing.
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

// A Player that now names a different controller reshapes the remote set,
// so the operator recreates the pod at the film's place with the new
// controller's sidecar wiring.
func TestAPlayerRemoteChangeRecreatesThePodOnTheChangedRemoteSet(t *testing.T) {
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
		"registry.example/player:test", "registry.example/sidecar:test", "bus.media.svc:1883", defaultTopicBase, sofa, resolvedPreferences{})
	pod.Status.Phase = podRunning
	cluster.pods["movie-playback"] = pod
	cluster.claims["movie-devices"] = buildClaim(play, player)

	player.Spec.Remotes = []PlayerRemote{{Name: "armchair"}}

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
// operator recreates the pod at the film's saved place.
func TestAMissingPodForALiveRunIsRecreatedAtTheStashedPosition(t *testing.T) {
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

// mpv exited non-zero and the pod is Failed. The operator recreates it at
// the film's place, the way a Job restarts a failed pod.
func TestAFailedPodIsRecreatedAtTheStashedPosition(t *testing.T) {
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

// mpv exited zero and the pod Succeeded: the film ended. A finished film
// is terminal, so the operator recreates nothing and the Play reads
// Finished.
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

// A Finished Play whose pod the garbage collector already took is left
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

// A genuinely new run steals its controllers, the most-recent-steals
// default. A resume holds them already, so it steals nothing. The fresh
// bool ensurePlayback returns is what the steal keys on, and only a run
// with a resume point is a resume.
func TestOnlyANewRunStealsAndAResumeDoesNot(t *testing.T) {
	cases := []struct {
		name      string
		position  string
		wantFresh bool
	}{
		{name: "a new run steals", position: "", wantFresh: true},
		{name: "a resume does not steal", position: "0:10:00", wantFresh: false},
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

			_, fresh, err := media.ensurePlayback(play, claim,
				resolution{Items: []string{"https://nas/film.mkv"}}, resolvedPreferences{}, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			if fresh != one.wantFresh {
				t.Errorf("fresh = %v, want %v", fresh, one.wantFresh)
			}
		})
	}
}

// The recreate backoff bounds how fast a pod that keeps failing comes
// back. The first recreate is immediate, a rapid second one is blocked,
// and the delay doubles from the base up to the cap and stays there.
func TestTheBackoffBoundsTheRecreateRate(t *testing.T) {
	media := testOperator(t, newFakeCluster(), make(chan struct{}, 1))
	key := runKey("house", "movie")

	if !media.mayResume(key) {
		t.Fatal("the first recreate was blocked, want it immediate")
	}
	if media.mayResume(key) {
		t.Error("a rapid second recreate was allowed, want it blocked by backoff")
	}

	// Each recreate after its deadline grows the count, and the delay
	// doubles to the cap and holds there.
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

	// A run that stayed up past the reset window starts over at once.
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

// The command sidecar clears the retained status on termination and marks
// the run offline, which drops the desk's latest report. The Play's own
// status still carries the last position, so a resume after an eviction
// starts where the film was and does not fall back to the start.
func TestStashedPositionFallsBackToTheStatusWhenTheReportIsCleared(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	play := housePlay("https://nas/film.mkv")
	play.Status.Position = "0:41:00"

	media.reports.availability("house", "movie", false)

	if got := media.reports.latestFor("house", "movie"); got != nil {
		t.Fatalf("the desk still holds a report: %+v", *got)
	}
	position, ok := media.resumePoint(play)
	if !ok || position != "0:41:00" {
		t.Errorf("resumePoint = %q, %v, want 0:41:00, true", position, ok)
	}
	if got := media.stashedPosition(play); got != "0:41:00" {
		t.Errorf("stashedPosition = %q, want the status position 0:41:00", got)
	}
}

// A Player that names a Remote nobody wrote fails the Play before any pod
// exists, and the message names the Player and the missing Remote.
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
	want := "the Player theater names the Remote ghost, which does not exist in this namespace"
	if status.Message != want {
		t.Errorf("message = %q, want %q", status.Message, want)
	}
	if _, held := cluster.pods["movie-playback"]; held {
		t.Errorf("a missing remote created the playback pod: %v", cluster.pods)
	}
}

// A deleted Play's retained status and availability are cleared, but only
// after the grace: for a short while the operator holds them so a
// subscriber that reads just after the delete still sees the final state,
// then it empties both topics.
func TestADeletedPlayIsReclaimedAfterTheGrace(t *testing.T) {
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	broker := brokers[0]
	wake := make(chan struct{}, 1)
	media := &operator{
		topicBase:   defaultTopicBase,
		bus:         bus,
		reports:     newReports(wake),
		playReclaim: map[string]time.Time{},
	}

	// The desk has seen the run, and the collection no longer holds it.
	media.reports.availability("den", "old-film", false)
	live := map[string]bool{}

	// Inside the grace, nothing is cleared.
	media.reclaimPlays(live)
	select {
	case got := <-broker.pubs:
		t.Fatalf("a Play was reclaimed inside the grace: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}

	// Past the grace, the status and the availability are both cleared.
	media.playReclaim[runKey("den", "old-film")] = time.Now().Add(-2 * playReclaimGrace)
	media.reclaimPlays(live)

	cleared := map[string]bool{}
	for range 2 {
		published := waitForPublish(t, broker.pubs)
		if len(published.payload) != 0 || !published.retained {
			t.Errorf("reclaim published %+v, want an empty retained clear", published)
		}
		cleared[published.topic] = true
	}
	if !cleared[playStatusTopic(defaultTopicBase, "den", "old-film")] ||
		!cleared[playAvailabilityTopic(defaultTopicBase, "den", "old-film")] {
		t.Errorf("cleared topics = %v, want the status and the availability", cleared)
	}
}

// An empty availability payload is a cleared retained topic, not an
// offline signal, so the operator does not mark the run seen for it. The
// operator publishes that empty value itself when it reclaims a deleted
// Play, so reading it back as offline would make the run stale again and
// reclaim it forever. A real offline payload still marks the run seen.
func TestAnEmptyAvailabilityDoesNotMarkARunSeen(t *testing.T) {
	wake := make(chan struct{}, 1)
	media := &operator{
		topicBase: defaultTopicBase,
		reports:   newReports(wake),
		focus:     newFocusDesk(wake),
	}
	topic := playAvailabilityTopic(defaultTopicBase, "den", "old")

	media.handleBusMessage(topic, nil)
	if got := media.reports.stale(map[string]bool{}); len(got) != 0 {
		t.Errorf("an empty availability marked runs seen: %v", got)
	}

	media.handleBusMessage(topic, []byte(availabilityOffline))
	if got := media.reports.stale(map[string]bool{}); len(got) != 1 || got[0] != runKey("den", "old") {
		t.Errorf("a real offline signal did not mark the run seen: %v", got)
	}
}

// A fresh broker session holds none of the operator's retained state,
// so a reconnect clears the record of published key tables, which
// makes the next pass write them again, and republishes each focus
// mark, so a controller keeps the Player it drives across a broker
// restart.
func TestAReconnectReestablishesTheRetainedState(t *testing.T) {
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	broker := brokers[0]
	wake := make(chan struct{}, 1)
	media := &operator{
		topicBase: defaultTopicBase,
		bus:       bus,
		focus:     newFocusDesk(wake),
		keysPublished: map[string]string{
			remoteKeysTopic(defaultTopicBase, "house", "sofa"): "already-published",
		},
	}
	media.focus.setMark(controllerKey("den", "sofa"), "theater")

	media.reestablishRetained()

	if len(media.keysPublished) != 0 {
		t.Errorf("keysPublished still holds %v, want it cleared for a rewrite", media.keysPublished)
	}
	published := waitForPublish(t, broker.pubs)
	if published.topic != remoteFocusTopic(defaultTopicBase, "den", "sofa") ||
		string(published.payload) != "theater" || !published.retained {
		t.Errorf("focus republish = %+v, want the retained theater mark", published)
	}
}

// keysOperator wires an operator with a bus to a fake broker, so a
// test reads what the pass publishes on a Remote's keys topic.
func keysOperator(t *testing.T, cluster *fakeCluster) (*operator, *fakeBroker) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	return &operator{
		client:        testAPIClient(t, cluster.handler(t)),
		topicBase:     defaultTopicBase,
		bus:           bus,
		keysPublished: map[string]string{},
	}, brokers[0]
}

// keysRemote is the fixture the publish tests press: one Remote and
// the Keymap it names, with the table already compiled.
func keysRemote(cluster *fakeCluster) *Remote {
	cluster.keymaps["gamepad"] = testKeymap()
	remote := houseRemote("gamepad")
	cluster.remotes["sofa"] = remote
	return remote
}

// The pass compiles a Remote's table and publishes it retained, so the
// standing pod reads the current table the instant it connects.
func TestPublishKeysPublishesARetainedTable(t *testing.T) {
	cluster := newFakeCluster()
	media, broker := keysOperator(t, cluster)

	media.publishKeys(keysRemote(cluster), media.loadKeymaps(), map[string]bool{})

	published := waitForPublish(t, broker.pubs)
	mustMatch(t, published.topic, remoteKeysTopic(defaultTopicBase, "house", "sofa"))
	mustMatch(t, published.retained, true)
	var table []compiledBinding
	mustSucceed(t, json.Unmarshal(published.payload, &table))
	mustMatch(t, len(table), len(baseKeys)+2)
}

// The keys topic is retained, so an unchanged table republishes nothing
// on a later pass and the broker keeps serving the one it holds. A
// changed Keymap does publish.
func TestPublishKeysRepublishesOnlyOnChange(t *testing.T) {
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
}

// A Keymap that will not compile publishes nothing, so the last good
// retained table stays in place and the controller keeps working.
func TestPublishKeysPublishesNothingForAKeymapThatWillNotCompile(t *testing.T) {
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
}

// playersOperator wires an operator with a bus to a fake broker, so a
// test reads what the player reconcile publishes. The idle display class
// stays unset, so reconcileIdle builds nothing and the test isolates the
// re-present publish.
func playersOperator(t *testing.T, cluster *fakeCluster) (*operator, *fakeBroker) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	return &operator{
		client:                testAPIClient(t, cluster.handler(t)),
		topicBase:             defaultTopicBase,
		bus:                   bus,
		reports:               newReports(nil),
		focus:                 newFocusDesk(nil),
		peripherals:           newPeripheralDesk(),
		codes:                 newCodesDesk(nil),
		panels:                newPanelDesk(nil),
		volumes:               newVolumeDesk(),
		playerStatusPublished: map[string]string{},
		// These tests read what the status publish leaves on the bus,
		// so the volume seed is held off for longer than any of them runs.
		// The volume tests run their own operator with the grace elapsed.
		volumeSeedAfter: time.Now().Add(time.Hour),
	}, brokers[0]
}

// A Player that was playing and now names no Play crossed the play-end
// edge, so the operator publishes a re-present to its commands topic, not
// retained, and the idle screen client recreates the surface. The Idle
// status goes out first, so the display reads the film is over before the
// reveal that follows the re-present.
func TestAPlayEndPublishesTheIdleStatusThenARePresent(t *testing.T) {
	cluster := newFakeCluster()
	media, broker := playersOperator(t, cluster)
	player := Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house"},
		Status:   PlayerStatus{Activity: playerPlaying, Play: "movie"},
	}

	media.reconcilePlayers([]Player{player}, nil, "", nil)

	status := waitForPublish(t, broker.pubs)
	mustMatch(t, status.topic, playerStatusTopic(defaultTopicBase, "house", "theater"))
	var state playerBusStatus
	mustSucceed(t, json.Unmarshal(status.payload, &state))
	mustMatch(t, state.Activity, playerIdle)

	published := waitForPublish(t, broker.pubs)
	if published.topic != playerCommandsTopic(defaultTopicBase, "house", "theater") {
		t.Errorf("topic = %q, want the player commands topic", published.topic)
	}
	if published.retained {
		t.Error("the re-present was retained, want an event")
	}
	var command mediaCommand
	mustSucceed(t, json.Unmarshal(published.payload, &command))
	if command.Action != actionRePresent {
		t.Errorf("action = %q, want %q", command.Action, actionRePresent)
	}
}

// The sidecar's ending reaches the operator on the play status topic, and
// the pass it wakes reads the unit as idle while the pod still runs and the
// Play still reads Running. That is the whole point of the mark: the idle
// status and the re-present go out in bus time, seconds before the pod
// terminates, so the idle screen draws over the dying film.
func TestAReportedEndingPublishesTheIdleStatusThenARePresent(t *testing.T) {
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

	published := waitForPublish(t, broker.pubs)
	mustMatch(t, published.topic, playerCommandsTopic(defaultTopicBase, "house", "theater"))
	var command mediaCommand
	mustSucceed(t, json.Unmarshal(published.payload, &command))
	mustMatch(t, command.Action, actionRePresent)

	// The Play is still what the API server holds it to be, so kubectl
	// still lists the run for the seconds the pod takes to terminate.
	mustMatch(t, play.Status.Phase, phaseRunning)
}

// A Player already idle stays idle across the pass, which is no edge, so
// the operator publishes nothing. Without the guard the backstop would
// poke the idle screen client every tick.
func TestAnIdlePlayerPublishesNoRePresent(t *testing.T) {
	cluster := newFakeCluster()
	media, broker := playersOperator(t, cluster)
	player := Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house"},
		Status:   PlayerStatus{Activity: playerIdle},
	}

	media.reconcilePlayers([]Player{player}, nil, "", nil)

	mustPublishNoRePresent(t, broker)
}

// A Player still running its Play is no edge either, so a backstop pass
// over a playing unit publishes nothing.
func TestAPlayerStillPlayingPublishesNoRePresent(t *testing.T) {
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

	media.reconcilePlayers([]Player{player}, []Play{play}, "", nil)

	mustPublishNoRePresent(t, broker)
}

// Every pass publishes each unit's presentable state to its retained
// status topic, so an idle pod that just started reads the current state
// from the broker.
func TestAPassPublishesTheRetainedPlayerStatus(t *testing.T) {
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
}

// The status topic is retained, so a settled unit republishes nothing on
// the next pass: the broker still holds the payload, and a new subscriber
// reads it from there. An edit to the Player does publish, which is how a
// renamed part reaches the screen with no pod restart.
func TestThePlayerStatusRepublishesOnlyOnChange(t *testing.T) {
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
}

// A controller's Peripheral reaches the unit's status on the bus: the
// link it reports and the charge it reports, on the one remote part. The
// device that sleeps reads away on the next pass, so the screen dims the
// name it drew.
func TestAPeripheralReachesThePublishedPlayerStatus(t *testing.T) {
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
}

// A Player the cluster no longer holds has its retained status cleared
// with an empty publish, so a deleted unit leaves nothing on the bus for a
// subscriber to draw.
func TestADeletedPlayerClearsItsRetainedStatus(t *testing.T) {
	cluster := newFakeCluster()
	media, broker := playersOperator(t, cluster)

	media.reconcilePlayers([]Player{settledPlayer(housePlayer())}, nil, "", nil)
	waitForPublish(t, broker.pubs)

	media.reconcilePlayers(nil, nil, "", nil)

	cleared := waitForPublish(t, broker.pubs)
	mustMatch(t, cleared.topic, playerStatusTopic(defaultTopicBase, "house", "theater"))
	mustMatch(t, len(cleared.payload), 0)
	mustMatch(t, cleared.retained, true)
}

// volumeOperator wires a whole operator to a fake broker, with the seed
// grace already elapsed, so a test reads the levels a pass publishes.
func volumeOperator(t *testing.T, cluster *fakeCluster) (*operator, *fakeBroker) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.bus = bus
	return media, brokers[0]
}

// theaterVolumeTopic is the one unit these tests seed and write through.
func theaterVolumeTopic() string {
	return playerVolumeTopic(defaultTopicBase, "house", "theater")
}

// mustPublishNoVolume drains the broker for the window a publish would
// take and fails on any message that reached the unit's volume topic.
func mustPublishNoVolume(t *testing.T, broker *fakeBroker) {
	t.Helper()
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case got := <-broker.pubs:
			if got.topic == theaterVolumeTopic() {
				t.Fatalf("a level reached the bus: %+v", got)
			}
		case <-deadline:
			return
		}
	}
}

// A unit the broker holds no level for is seeded at unity, so the
// state is always readable off the bus and no reader carries a
// default. The seed runs once: the pass that follows reads the level
// it wrote and writes nothing more.
func TestAPassSeedsAPlayerWithNoLevel(t *testing.T) {
	media, broker := volumeOperator(t, newFakeCluster())
	player := settledPlayer(housePlayer())

	media.reconcilePlayers([]Player{player}, nil, "", nil)

	published := waitForPublish(t, broker.pubs)
	mustMatch(t, published.topic, theaterVolumeTopic())
	mustMatch(t, published.retained, true)
	mustMatch(t, string(published.payload), `{"level":100,"muted":false}`)

	media.reconcilePlayers([]Player{player}, nil, "", nil)
	mustPublishNoVolume(t, broker)
}

// A level that stands on the broker is never written over by the
// seed, so a room keeps the level a person set across an operator restart.
func TestThePassDoesNotSeedOverALevelThatStands(t *testing.T) {
	media, broker := volumeOperator(t, newFakeCluster())
	media.handleBusMessage(theaterVolumeTopic(), []byte(`{"level":30,"muted":true}`))

	media.reconcilePlayers([]Player{settledPlayer(housePlayer())}, nil, "", nil)

	mustPublishNoVolume(t, broker)
}

// A Player with no sinks is not seeded, because a unit with nothing
// to hear has no level to mean anything.
func TestThePassDoesNotSeedASpeakerlessPlayer(t *testing.T) {
	media, broker := volumeOperator(t, newFakeCluster())
	player := housePlayer()
	player.Spec.Sinks = nil

	media.reconcilePlayers([]Player{settledPlayer(player)}, nil, "", nil)

	mustPublishNoVolume(t, broker)
}

// A fresh broker session delivers its retained levels on its own
// goroutine moments after the subscribe, so the pass holds the seed back
// for the grace. A seed inside that window would write unity over a level
// a person had set.
func TestTheSeedWaitsOutTheGraceAfterAConnect(t *testing.T) {
	media, broker := volumeOperator(t, newFakeCluster())

	media.reestablishRetained()
	media.reconcilePlayers([]Player{settledPlayer(housePlayer())}, nil, "", nil)

	mustPublishNoVolume(t, broker)
}

// A Play that declares a starting level has it written through to
// the unit's topic before the pod exists, merged over what the unit already
// holds, and mpv starts at the merged value.
func TestAPlayWritesItsLevelThroughBeforeThePodExists(t *testing.T) {
	cluster := newFakeCluster()
	play := housePlay("https://nas/film.mkv")
	play.Spec.Volume = &PlayVolume{Level: level(35)}
	cluster.plays["movie"] = play
	cluster.players["theater"] = housePlayer()
	media, broker := volumeOperator(t, cluster)
	media.handleBusMessage(theaterVolumeTopic(), []byte(`{"level":80,"muted":true}`))

	media.pass()

	published := waitForPublish(t, broker.pubs)
	mustMatch(t, published.topic, theaterVolumeTopic())
	mustMatch(t, published.retained, true)
	mustMatch(t, string(published.payload), `{"level":35,"muted":true}`)
	mustMatch(t, envValue(cluster.pods["movie-playback"].Spec.Containers[0], playerOptionsVariable),
		"--volume=35\n--mute=yes")
}

// The write-through runs on the creating pass alone. A republish on
// a later pass of the same run would write the Play's level over every
// press a person made during the film.
func TestAPlayWritesItsLevelThroughOnlyOnce(t *testing.T) {
	cluster := newFakeCluster()
	play := housePlay("https://nas/film.mkv")
	play.Spec.Volume = &PlayVolume{Level: level(35)}
	cluster.plays["movie"] = play
	cluster.players["theater"] = housePlayer()
	media, broker := volumeOperator(t, cluster)

	media.pass()
	waitForPublish(t, broker.pubs)
	media.handleBusMessage(theaterVolumeTopic(), []byte(`{"level":60,"muted":false}`))

	media.pass()

	mustPublishNoVolume(t, broker)
}

// A Play may declare a level for a unit that has nothing to hear. The
// write-through reads the same speaker gate the seed does, so the
// declaration publishes nothing and the topic stays empty.
func TestAPlayAgainstASpeakerlessPlayerWritesNoLevelThrough(t *testing.T) {
	cluster := newFakeCluster()
	play := housePlay("https://nas/film.mkv")
	play.Spec.Volume = &PlayVolume{Level: level(35)}
	cluster.plays["movie"] = play
	player := housePlayer()
	player.Spec.Sinks = nil
	cluster.players["theater"] = player
	media, broker := volumeOperator(t, cluster)

	media.pass()

	mustPublishNoVolume(t, broker)
}

// A Play that declares no level starts the run at whatever the unit
// already holds, and the operator publishes nothing of its own.
func TestAPlayWithNoLevelStartsAtTheUnitsOwn(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	media, broker := volumeOperator(t, cluster)
	media.handleBusMessage(theaterVolumeTopic(), []byte(`{"level":80,"muted":false}`))

	media.pass()

	mustPublishNoVolume(t, broker)
	mustMatch(t, envValue(cluster.pods["movie-playback"].Spec.Containers[0], playerOptionsVariable),
		"--volume=80\n--mute=no")
}

// settledPlayer is a Player already idle, so a pass over it crosses no
// play-end edge and publishes its status alone.
func settledPlayer(player *Player) Player {
	player.Status = PlayerStatus{Activity: playerIdle}
	return *player
}

// mustPublishNothing proves nothing reached the broker in the window a
// publish would have taken.
func mustPublishNothing(t *testing.T, broker *fakeBroker) {
	t.Helper()
	select {
	case got := <-broker.pubs:
		t.Fatalf("a publish reached the bus: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// mustPublishNoRePresent proves the pass sent no re-present. Every pass
// publishes each Player's retained status, so the check reads what did
// arrive and fails only on a message to the commands topic.
func mustPublishNoRePresent(t *testing.T, broker *fakeBroker) {
	t.Helper()
	commands := playerCommandsTopic(defaultTopicBase, "house", "theater")
	deadline := time.After(50 * time.Millisecond)
	for {
		select {
		case got := <-broker.pubs:
			if got.topic == commands {
				t.Fatalf("a re-present reached the bus: %+v", got)
			}
		case <-deadline:
			return
		}
	}
}
