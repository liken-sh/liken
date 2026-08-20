package main

// These tests run whole passes against a small API server that holds
// the four kinds this operator reads and writes, so a pass is proved
// by what it left behind.

import (
	"encoding/json"
	"net/http"
	"path"
	"sort"
	"strings"
	"testing"
)

// The fake cluster: one map per kind, and the list of requests a
// pass made.
type fakeCluster struct {
	plays    map[string]*Play
	players  map[string]*Player
	claims   map[string]*ResourceClaim
	pods     map[string]*Pod
	requests []string
}

func newFakeCluster() *fakeCluster {
	return &fakeCluster{
		plays:   map[string]*Play{},
		players: map[string]*Player{},
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
			// The list is sorted so one pass reads the collection
			// in one order every time.
			list := PlayList{Metadata: ListMeta{ResourceVersion: "1"}}
			for _, key := range sortedNames(f.plays) {
				list.Items = append(list.Items, *f.plays[key])
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/status"):
			var written Play
			_ = json.NewDecoder(r.Body).Decode(&written)
			f.plays[written.Metadata.Name] = &written
			_ = json.NewEncoder(w).Encode(written)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/plays/"):
			answer(w, f.plays[name])
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/players/"):
			answer(w, f.players[name])
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

// An object the cluster does not hold is a 404, which is the answer
// the operator creates against.
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
		client:      testAPIClient(t, cluster.handler(t)),
		image:       "registry.example/player:test",
		operatorURL: "http://media-operator.media.svc:8080",
		reports:     newReports(wake),
	}
}

// A token the test can name, so a report can be posted with it.
func mintedToken(t *testing.T, token string) {
	t.Helper()
	previous := mintToken
	mintToken = func() (string, error) { return token, nil }
	t.Cleanup(func() { mintToken = previous })
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

func TestAPassCreatesTheClaimAndThePodAndReportsPending(t *testing.T) {
	mintedToken(t, "cafef00dcafef00dcafef00dcafef00d")
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
	if got := tokenFromPod(pod); got != "cafef00dcafef00dcafef00dcafef00d" {
		t.Errorf("token = %q", got)
	}
	// The operator remembers what it minted, so the pod's first
	// report is accepted.
	if got := media.reports.token("house", "movie"); got != "cafef00dcafef00dcafef00dcafef00d" {
		t.Errorf("remembered token = %q", got)
	}
	status := cluster.plays["movie"].Status
	if status.Phase != phasePending || status.Pod != "movie-playback" {
		t.Errorf("status = %+v", status)
	}
}

// A second pass over the same Play creates nothing; the claim and
// the pod are already there.
func TestASecondPassCreatesNothingNew(t *testing.T) {
	mintedToken(t, "0000000000000000000000000000cafe")
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

// The resolver refuses before any object exists, so a Play that
// names a scheme this operator does not resolve leaves nothing
// behind.
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

// A Play that reached a terminal phase is read and left alone; the
// pass asks the API server for nothing else about it.
func TestATerminalPlayIsReadAndLeftAlone(t *testing.T) {
	cluster := newFakeCluster()
	finished := housePlay("https://nas/film.mkv")
	finished.Status = PlayStatus{Phase: phaseFinished, Pod: "movie-playback"}
	cluster.plays["movie"] = finished
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	if len(cluster.requests) != 1 || cluster.requests[0] != "GET "+playsPath {
		t.Errorf("requests = %v, want the list alone", cluster.requests)
	}
}

func TestARunningPodFoldsTheLatestReportIntoTheStatus(t *testing.T) {
	mintedToken(t, "1111111111111111111111111111ffff")
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()
	cluster.pods["movie-playback"].Status.Phase = podRunning
	accepted := media.reports.accept(playReport{
		Namespace: "house",
		Name:      "movie",
		Token:     "1111111111111111111111111111ffff",
		Item:      1,
		Position:  "0:03:20",
		Duration:  "1:58:00",
	})
	if !accepted {
		t.Fatal("the operator refused the token it minted")
	}
	media.pass()

	status := cluster.plays["movie"].Status
	want := PlayStatus{
		Phase:    phaseRunning,
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
	mintedToken(t, "2222222222222222222222222222ffff")
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

// An operator that restarted holds no tokens; the pod it finds is
// where the minted token survived.
func TestTheOperatorAdoptsTheTokenOfAPodItDidNotCreate(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	running := buildPod(cluster.plays["movie"], buildClaim(cluster.plays["movie"], housePlayer()),
		resolution{Items: []string{"https://nas/film.mkv"}},
		"registry.example/player:test", "3333333333333333333333333333ffff", "http://media-operator.media.svc:8080")
	running.Status.Phase = podRunning
	cluster.pods["movie-playback"] = running
	cluster.claims["movie-devices"] = buildClaim(cluster.plays["movie"], housePlayer())
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	if got := media.reports.token("house", "movie"); got != "3333333333333333333333333333ffff" {
		t.Errorf("adopted token = %q", got)
	}
	if !media.reports.accept(playReport{
		Namespace: "house", Name: "movie", Token: "3333333333333333333333333333ffff", Item: 1,
	}) {
		t.Error("the operator refused the token it adopted")
	}
}

// A Play that left the cluster leaves nothing on the report desk.
func TestAPassForgetsAPlayThatIsGone(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.reports.remember("house", "movie", "4444444444444444444444444444ffff")

	media.pass()

	if got := media.reports.token("house", "movie"); got != "" {
		t.Errorf("token = %q, want the desk to have forgotten it", got)
	}
}
