package main

// The derivation is a pure function over what the pass read, so
// these tests are a table of facts and the status each set of facts
// earns.

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
)

func statusTestPlay() *Play {
	return &Play{
		Metadata: ObjectMeta{Name: "movie", Namespace: "house", ResourceVersion: "12"},
		Spec:     PlaySpec{Players: []string{"theater"}, URIs: []string{"https://nas/film.mkv"}},
	}
}

func playbackPod(phase string) *Pod {
	return &Pod{
		Metadata: ObjectMeta{Name: "movie-playback", Namespace: "house"},
		Status:   PodStatus{Phase: phase},
	}
}

func TestDerivePlayStatus(t *testing.T) {
	player := &Player{Metadata: ObjectMeta{Name: "theater", Namespace: "house"}}
	playing := &playReport{Item: 2, Position: "0:12:30", Duration: "1:45:00"}
	paused := &playReport{Item: 2, Position: "0:12:30", Duration: "1:45:00", Paused: true}

	failed := playbackPod(podFailed)
	failed.Status.ContainerStatuses = []ContainerStatus{{
		Name: playerContainer,
		State: ContainerState{Terminated: &ContainerStateTerminated{
			ExitCode: 2,
			Reason:   "Error",
		}},
	}}

	unschedulable := playbackPod(podPending)
	unschedulable.Status.Message = "no machine holds every claimed device"

	cases := []struct {
		name     string
		player   *Player
		buildErr error
		pod      *Pod
		report   *playReport
		want     PlayStatus
	}{{
		name:     "a refused URI fails the play before anything exists",
		buildErr: errors.New("the scheme rtsp:// is not one the operator resolves; it resolves https:// and nfs://"),
		want: PlayStatus{
			Phase:   phaseFailed,
			Message: "the scheme rtsp:// is not one the operator resolves; it resolves https:// and nfs://",
		},
	}, {
		name:     "a Keymap that will not compile fails the play the same way",
		buildErr: errors.New("the Keymap gamepad binds BTN_NOPE, which is not an evdev button name this operator knows"),
		want: PlayStatus{
			Phase:   phaseFailed,
			Message: "the Keymap gamepad binds BTN_NOPE, which is not an evdev button name this operator knows",
		},
	}, {
		name: "an absent Player is Pending, not Failed",
		want: PlayStatus{Phase: phasePending, Message: "the Player theater does not exist in this namespace"},
	}, {
		name:   "a Play whose pod does not exist yet is Pending",
		player: player,
		want:   PlayStatus{Phase: phasePending},
	}, {
		name:   "a pod that has not started is Pending and carries its name",
		player: player,
		pod:    playbackPod(podPending),
		want:   PlayStatus{Phase: phasePending, Pod: "movie-playback"},
	}, {
		name:   "a pod that cannot schedule reports what the pod says",
		player: player,
		pod:    unschedulable,
		want: PlayStatus{
			Phase:   phasePending,
			Pod:     "movie-playback",
			Message: "no machine holds every claimed device",
		},
	}, {
		name:   "a running pod with no report yet carries no numbers",
		player: player,
		pod:    playbackPod(podRunning),
		want:   PlayStatus{Phase: phaseRunning, Pod: "movie-playback"},
	}, {
		name:   "a running pod folds in the latest report",
		player: player,
		pod:    playbackPod(podRunning),
		report: playing,
		want: PlayStatus{
			Phase:    phaseRunning,
			Pod:      "movie-playback",
			Item:     2,
			Position: "0:12:30",
			Duration: "1:45:00",
		},
	}, {
		name:   "a paused film stays Running",
		player: player,
		pod:    playbackPod(podRunning),
		report: paused,
		want: PlayStatus{
			Phase:    phaseRunning,
			Paused:   true,
			Pod:      "movie-playback",
			Item:     2,
			Position: "0:12:30",
			Duration: "1:45:00",
		},
	}, {
		name:   "a pod that succeeded finished, and keeps where it ended",
		player: player,
		pod:    playbackPod(podSucceeded),
		report: paused,
		want: PlayStatus{
			Phase:    phaseFinished,
			Pod:      "movie-playback",
			Item:     2,
			Position: "0:12:30",
			Duration: "1:45:00",
		},
	}, {
		name:   "a pod that failed says which container ended and how",
		player: player,
		pod:    failed,
		want: PlayStatus{
			Phase:   phaseFailed,
			Pod:     "movie-playback",
			Message: "the playback pod failed: Error (exit code 2)",
		},
	}, {
		name:   "a failure with no container status falls back to the pod's own message",
		player: player,
		pod: &Pod{
			Metadata: ObjectMeta{Name: "movie-playback"},
			Status:   PodStatus{Phase: podFailed, Message: "the node was drained"},
		},
		want: PlayStatus{
			Phase:   phaseFailed,
			Pod:     "movie-playback",
			Message: "the playback pod failed: the node was drained",
		},
	}, {
		name:   "a pod phase this operator does not map is Pending",
		player: player,
		pod:    playbackPod("Unknown"),
		want:   PlayStatus{Phase: phasePending, Pod: "movie-playback"},
	}}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := derivePlayStatus(statusTestPlay(), one.player, one.buildErr, one.pod, one.report)
			if !reflect.DeepEqual(got, one.want) {
				t.Errorf("status = %+v, want %+v", got, one.want)
			}
		})
	}
}

// A small API server that holds one Play and can refuse a stated
// number of writes with a conflict.
type statusAPI struct {
	stored    Play
	conflicts int
	requests  []string
	writes    []Play
}

func (s *statusAPI) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(s.stored)
		case http.MethodPut:
			if s.conflicts > 0 {
				s.conflicts--
				w.WriteHeader(http.StatusConflict)
				return
			}
			var written Play
			_ = json.NewDecoder(r.Body).Decode(&written)
			s.writes = append(s.writes, written)
			s.stored = written
			_ = json.NewEncoder(w).Encode(written)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

func TestAnUnchangedStatusIsNotWritten(t *testing.T) {
	api := &statusAPI{}
	play := statusTestPlay()
	play.Status = PlayStatus{Phase: phaseRunning, Pod: "movie-playback", Position: "0:01:00"}

	err := writePlayStatus(testAPIClient(t, api.handler(t)), play, play.Status)
	if err != nil {
		t.Fatal(err)
	}
	if len(api.requests) != 0 {
		t.Errorf("an unchanged status reached the API server: %v", api.requests)
	}
}

func TestAChangedStatusIsWrittenOnce(t *testing.T) {
	api := &statusAPI{}
	play := statusTestPlay()
	play.Status = PlayStatus{Phase: phasePending}
	desired := PlayStatus{Phase: phaseRunning, Pod: "movie-playback", Item: 1}

	if err := writePlayStatus(testAPIClient(t, api.handler(t)), play, desired); err != nil {
		t.Fatal(err)
	}
	if len(api.writes) != 1 {
		t.Fatalf("writes = %d, want one: %v", len(api.writes), api.requests)
	}
	if !reflect.DeepEqual(api.writes[0].Status, desired) {
		t.Errorf("status = %+v, want %+v", api.writes[0].Status, desired)
	}
}

// A losing write reads the object again and writes once more; the
// second attempt is the last.
func TestAConflictEarnsOneRetryWithTheFreshVersion(t *testing.T) {
	api := &statusAPI{conflicts: 1}
	api.stored = *statusTestPlay()
	api.stored.Metadata.ResourceVersion = "13"

	play := statusTestPlay()
	desired := PlayStatus{Phase: phaseRunning, Pod: "movie-playback"}

	if err := writePlayStatus(testAPIClient(t, api.handler(t)), play, desired); err != nil {
		t.Fatal(err)
	}
	if len(api.writes) != 1 {
		t.Fatalf("writes = %d, want one accepted write: %v", len(api.writes), api.requests)
	}
	if got := api.writes[0].Metadata.ResourceVersion; got != "13" {
		t.Errorf("the retry carried resourceVersion %q, want the fresh 13", got)
	}
	want := []string{
		"PUT /apis/media.liken.sh/v1alpha1/namespaces/house/plays/movie/status",
		"GET /apis/media.liken.sh/v1alpha1/namespaces/house/plays/movie",
		"PUT /apis/media.liken.sh/v1alpha1/namespaces/house/plays/movie/status",
	}
	if !reflect.DeepEqual(api.requests, want) {
		t.Errorf("requests = %v, want %v", api.requests, want)
	}
}

// The writer that won wrote the same status, so the retry has
// nothing left to say.
func TestAConflictWhoseWinnerWroteTheSameStatusStopsThere(t *testing.T) {
	desired := PlayStatus{Phase: phaseRunning, Pod: "movie-playback"}
	api := &statusAPI{conflicts: 1}
	api.stored = *statusTestPlay()
	api.stored.Metadata.ResourceVersion = "13"
	api.stored.Status = desired

	play := statusTestPlay()
	if err := writePlayStatus(testAPIClient(t, api.handler(t)), play, desired); err != nil {
		t.Fatal(err)
	}
	if len(api.writes) != 0 {
		t.Errorf("the retry wrote a status that was already there: %+v", api.writes)
	}
}

// Two conflicts in a row are not a race any more; the pass gives up
// and the next one starts from a fresh read.
func TestASecondConflictIsReported(t *testing.T) {
	api := &statusAPI{conflicts: 2}
	api.stored = *statusTestPlay()
	api.stored.Metadata.ResourceVersion = "13"

	err := writePlayStatus(testAPIClient(t, api.handler(t)), statusTestPlay(), PlayStatus{Phase: phaseRunning})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want %v", err, ErrConflict)
	}
}
