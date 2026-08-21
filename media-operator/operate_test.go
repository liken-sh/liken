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
		client:     testAPIClient(t, cluster.handler(t)),
		image:      "registry.example/player:test",
		busAddress: "bus.media.svc:1883",
		topicBase:  defaultTopicBase,
		// The bus is never Run, so a publish finds a nil write queue and
		// drops, which is what a pass wants with no broker under the test.
		bus:             newBus("bus.media.svc:1883", "media-operator-test", nil, nil, nil),
		reports:         newReports(wake),
		focus:           newFocusDesk(wake),
		positionWrites:  map[string]time.Time{},
		keymapPublished: map[string]string{},
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

	want := "GET " + playsPath + ",GET " + playersPath + ",GET " + remotesAllPath + ",GET " + keymapsPath
	if strings.Join(cluster.requests, ",") != want {
		t.Errorf("requests = %v, want the four list reads alone", cluster.requests)
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
// pod's translator sidecar names the same events topic the standing pod
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

	// The playback pod's translator sidecar carries the remote's events,
	// keymap, and focus topics, so it subscribes to what the standing pod
	// publishes and to the retained keymap.
	playback, held := cluster.pods["movie-playback"]
	if !held {
		t.Fatalf("no playback pod was created: %v", cluster.requests)
	}
	translator := initContainer(t, playback, "translate-sofa")
	if got, want := envValue(translator, remoteEventsVariable), remoteEventsTopic(defaultTopicBase, "house", "sofa"); got != want {
		t.Errorf("events topic = %q, want %q", got, want)
	}
	if got, want := envValue(translator, keymapTopicVariable), keymapTopic(defaultTopicBase, "gamepad"); got != want {
		t.Errorf("keymap topic = %q, want %q", got, want)
	}
	if got, want := envValue(translator, focusTopicVariable), remoteFocusTopic(defaultTopicBase, "house", "sofa"); got != want {
		t.Errorf("focus topic = %q, want %q", got, want)
	}
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
	edited.Spec.Buttons[0].Action = actionMute
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
		"registry.example/player:test", "bus.media.svc:1883", defaultTopicBase, nil)
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
		Keymap:      "gamepad",
		EventsTopic: remoteEventsTopic(defaultTopicBase, "house", "sofa"),
		KeymapTopic: keymapTopic(defaultTopicBase, "gamepad"),
		FocusTopic:  remoteFocusTopic(defaultTopicBase, "house", "sofa"),
	}}
	pod := buildPod(play, buildClaim(play, player),
		resolution{Items: []string{"https://nas/film.mkv"}},
		"registry.example/player:test", "bus.media.svc:1883", defaultTopicBase, sofa)
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

// keymapOperator wires an operator with a bus to a fake broker, so a test
// reads what the keymap reconcile publishes.
func keymapOperator(t *testing.T, cluster *fakeCluster) (*operator, *fakeBroker) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	return &operator{
		client:          testAPIClient(t, cluster.handler(t)),
		topicBase:       defaultTopicBase,
		bus:             bus,
		keymapPublished: map[string]string{},
	}, brokers[0]
}

// The keymap reconcile compiles each Keymap and publishes the table to
// its topic, retained, so a translator reads the current keymap the
// instant it connects. When the Keymap is deleted, the next reconcile
// clears the retained value.
func TestReconcileKeymapsPublishesAndClearsARetainedTable(t *testing.T) {
	cluster := newFakeCluster()
	cluster.keymaps["gamepad"] = testKeymap()
	media, broker := keymapOperator(t, cluster)

	media.reconcileKeymaps()

	published := waitForPublish(t, broker.pubs)
	if published.topic != keymapTopic(defaultTopicBase, "gamepad") {
		t.Errorf("topic = %q", published.topic)
	}
	if !published.retained {
		t.Error("the compiled keymap was not retained")
	}
	var bindings []compiledBinding
	if err := json.Unmarshal(published.payload, &bindings); err != nil {
		t.Fatalf("payload does not decode: %v", err)
	}
	if len(bindings) != 7 {
		t.Errorf("bindings = %+v, want the seven of the test keymap", bindings)
	}

	delete(cluster.keymaps, "gamepad")
	media.reconcileKeymaps()

	cleared := waitForPublish(t, broker.pubs)
	if cleared.topic != keymapTopic(defaultTopicBase, "gamepad") || len(cleared.payload) != 0 || !cleared.retained {
		t.Errorf("clear = %+v, want an empty retained publish", cleared)
	}
}

// The keymap topic is retained, so an unchanged Keymap republishes
// nothing on a later pass. The broker still holds the table, and a new
// subscriber reads it from there, so a steady film does not churn the bus
// with a table nobody edited. A change to the Keymap does publish.
func TestReconcileKeymapsRepublishesOnlyOnChange(t *testing.T) {
	cluster := newFakeCluster()
	cluster.keymaps["gamepad"] = testKeymap()
	media, broker := keymapOperator(t, cluster)

	media.reconcileKeymaps()
	waitForPublish(t, broker.pubs)

	media.reconcileKeymaps()
	select {
	case got := <-broker.pubs:
		t.Fatalf("an unchanged keymap republished %+v", got)
	case <-time.After(50 * time.Millisecond):
	}

	keymap := cluster.keymaps["gamepad"]
	keymap.Spec.Buttons = keymap.Spec.Buttons[:1]
	media.reconcileKeymaps()
	changed := waitForPublish(t, broker.pubs)
	if changed.topic != keymapTopic(defaultTopicBase, "gamepad") {
		t.Errorf("changed topic = %q, want the gamepad keymap", changed.topic)
	}
}

// A Keymap that will not compile publishes nothing, so the last-good
// retained value stays in place.
func TestReconcileKeymapsPublishesNothingForAKeymapThatWillNotCompile(t *testing.T) {
	cluster := newFakeCluster()
	cluster.keymaps["broken"] = &Keymap{
		Metadata: ObjectMeta{Name: "broken"},
		Spec:     KeymapSpec{Buttons: []KeymapButton{{Press: "BTN_NOPE", Action: actionPause}}},
	}
	media, broker := keymapOperator(t, cluster)

	media.reconcileKeymaps()

	select {
	case got := <-broker.pubs:
		t.Fatalf("a keymap that will not compile published %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}
