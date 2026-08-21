package main

// These tests run whole passes against a small API server that holds
// the kinds this operator reads and writes, so a pass is proved by what
// it left behind.

import (
	"encoding/json"
	"net/http"
	"path"
	"sort"
	"strings"
	"testing"
	"time"
)

// The fake cluster: one map per kind, and the list of requests a pass
// made.
type fakeCluster struct {
	plays    map[string]*Play
	players  map[string]*Player
	remotes  map[string]*Remote
	keymaps  map[string]*Keymap
	claims   map[string]*ResourceClaim
	pods     map[string]*Pod
	requests []string
}

func newFakeCluster() *fakeCluster {
	return &fakeCluster{
		plays:   map[string]*Play{},
		players: map[string]*Player{},
		remotes: map[string]*Remote{},
		keymaps: map[string]*Keymap{},
		claims:  map[string]*ResourceClaim{},
		pods:    map[string]*Pod{},
	}
}

func (f *fakeCluster) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
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
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/keymaps/"):
			answer(w, f.keymaps[name])
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
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
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
		client:         testAPIClient(t, cluster.handler(t)),
		image:          "registry.example/player:test",
		busAddress:     "bus.media.svc:1883",
		topicBase:      defaultTopicBase,
		reports:        newReports(wake),
		positionWrites: map[string]time.Time{},
	}
}

func housePlay(uris ...string) *Play {
	return &Play{
		Metadata: ObjectMeta{Name: "movie", Namespace: "house", UID: "play-uid", ResourceVersion: "9"},
		Spec:     PlaySpec{Players: []string{"theater"}, URIs: uris},
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
		"registry.example/player:test", "bus.media.svc:1883", defaultTopicBase, nil)
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
	want := "the scheme rtsp:// is not one the operator resolves; it resolves https:// and nfs://"
	if status.Message != want {
		t.Errorf("message = %q, want %q", status.Message, want)
	}
	if len(cluster.claims) != 0 || len(cluster.pods) != 0 {
		t.Errorf("a refused URI created objects: claims %v pods %v", cluster.claims, cluster.pods)
	}
}

// A Play that reached a terminal phase is read and left alone; the pass
// makes no request about the play itself, only the three list reads
// every pass makes.
func TestATerminalPlayIsReadAndLeftAlone(t *testing.T) {
	cluster := newFakeCluster()
	finished := housePlay("https://nas/film.mkv")
	finished.Status = PlayStatus{Phase: phaseFinished, Pod: "movie-playback"}
	cluster.plays["movie"] = finished
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	want := "GET " + playsPath + ",GET " + playersPath + ",GET " + remotesAllPath
	if strings.Join(cluster.requests, ",") != want {
		t.Errorf("requests = %v, want the three list reads alone", cluster.requests)
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
	if status != want {
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

// The bridge publishes a live position to the bus every second, but the
// operator writes a bare position advance to the resource no more than
// once per positionWriteInterval, so a status write does not wake the
// operator's own watch a second later and spin the loop.
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

// A Remote in the namespace, bound to the house player.
func houseRemote(keymap string) *Remote {
	return &Remote{
		Metadata: ObjectMeta{Name: "sofa", Namespace: "house"},
		Spec: RemoteSpec{
			Device:   RemoteDevice{Class: "gamepad"},
			Keymap:   keymap,
			Bindings: []RemoteBinding{{Player: "theater"}},
		},
	}
}

// bridgeRemotes reads the compiled remote set the operator wrote into
// the bridge sidecar's environment, so a test reads what the bridge
// subscribes to without a broker.
func bridgeRemotes(t *testing.T, pod *Pod) []remoteBindings {
	t.Helper()
	for _, container := range pod.Spec.InitContainers {
		if container.Name != bridgeContainer {
			continue
		}
		for _, env := range container.Env {
			if env.Name != remotesVariable {
				continue
			}
			var remotes []remoteBindings
			if err := json.Unmarshal([]byte(env.Value), &remotes); err != nil {
				t.Fatalf("%s does not decode: %v", remotesVariable, err)
			}
			return remotes
		}
	}
	t.Fatalf("the playback pod has no bridge sidecar carrying %s", remotesVariable)
	return nil
}

// A bound Remote reconciles into its own standing pod, and the playback
// pod's bridge sidecar names the same events topic the standing pod
// publishes to, so the two meet on the bus.
func TestAPassWiresABoundRemote(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
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

	// The playback pod's bridge sidecar carries the remote's events topic
	// and the compiled keymap, so it subscribes to what the standing pod
	// publishes and maps each press.
	playback, held := cluster.pods["movie-playback"]
	if !held {
		t.Fatalf("no playback pod was created: %v", cluster.requests)
	}
	remotes := bridgeRemotes(t, playback)
	if len(remotes) != 1 {
		t.Fatalf("bridge %s = %v, want one entry", remotesVariable, remotes)
	}
	if want := remoteEventsTopic(defaultTopicBase, "house", "sofa"); remotes[0].EventsTopic != want {
		t.Errorf("events topic = %q, want %q", remotes[0].EventsTopic, want)
	}
	if len(remotes[0].Bindings) == 0 {
		t.Error("the bridge carries no compiled bindings for the remote")
	}
}

// A Remote that names a Keymap nobody wrote fails the Play before the
// playback pod, because the bridge sidecar needs the compiled keymap and
// the compile runs before the pod. The Remote's own standing pod still
// runs: the reader needs no keymap.
func TestAPlayWhoseKeymapIsAbsentFailsBeforeItsPod(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	cluster.remotes["sofa"] = houseRemote("nowhere")
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	status := cluster.plays["movie"].Status
	if status.Phase != phaseFailed {
		t.Errorf("phase = %q, want Failed", status.Phase)
	}
	want := "the Remote sofa names the Keymap nowhere, which does not exist in this namespace"
	if status.Message != want {
		t.Errorf("message = %q, want %q", status.Message, want)
	}
	if _, held := cluster.pods["movie-playback"]; held {
		t.Errorf("a broken Remote created the playback pod: %v", cluster.pods)
	}
}

// The container set is fixed once the pod runs, so a Keymap broken after
// the film started changes nothing and the run keeps its status.
func TestARemoteBrokenAfterTheRunStartedLeavesThePlayAlone(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()
	cluster.pods["movie-playback"].Status.Phase = podRunning
	cluster.keymaps["gamepad"] = &Keymap{
		Metadata: ObjectMeta{Name: "gamepad", Namespace: "house"},
		Spec:     KeymapSpec{Buttons: []KeymapButton{{Press: "BTN_NOPE", Action: actionPause}}},
	}
	media.pass()

	status := cluster.plays["movie"].Status
	if status.Phase != phaseRunning {
		t.Errorf("phase = %q, want Running", status.Phase)
	}
	if status.Message != "" {
		t.Errorf("message = %q, want none", status.Message)
	}
}
