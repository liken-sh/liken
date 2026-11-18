package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

func statusTestPlay() *Play {
	return &Play{
		Metadata: ObjectMeta{Name: "movie", Namespace: "house", ResourceVersion: "12"},
		Spec:     PlaySpec{Players: []string{"theater"}, Items: []PlayItem{{URI: "https://nas/film.mkv"}}},
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

	missingVolume := playbackPod(podPending)
	missingVolume.Status.Conditions = []PodCondition{{
		Type:    "PodScheduled",
		Status:  "False",
		Message: `0/2 nodes are available: persistentvolumeclaim "films" not found.`,
	}}

	cases := []struct {
		name     string
		player   *Player
		buildErr error
		pod      *Pod
		report   *playReport
		want     PlayStatus
	}{{
		name:     "a refused URI fails the play before anything exists",
		buildErr: errors.New("the scheme rtsp:// is not one the operator resolves, which are https:// and nfs://"),
		want: PlayStatus{
			Phase:   phaseFailed,
			Message: "the scheme rtsp:// is not one the operator resolves, which are https:// and nfs://",
		},
	}, {
		name:     "a missing Remote fails the play the same way",
		buildErr: errors.New("the Player theater lists the Remote ghost in spec.remotes, and no Remote of that name exists in this namespace"),
		want: PlayStatus{
			Phase:   phaseFailed,
			Message: "the Player theater lists the Remote ghost in spec.remotes, and no Remote of that name exists in this namespace",
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
		name:   "a pod the scheduler cannot place reports the condition's message",
		player: player,
		pod:    missingVolume,
		want: PlayStatus{
			Phase:   phasePending,
			Pod:     "movie-playback",
			Message: `0/2 nodes are available: persistentvolumeclaim "films" not found.`,
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
		// The phase comes from the pod alone. If Ended changed it, the
		// Play would show Finished during the seconds its pod takes to
		// terminate.
		name:   "a report that carries the ending leaves the phase Running",
		player: player,
		pod:    playbackPod(podRunning),
		report: &playReport{Item: 2, Position: "0:12:30", Duration: "1:45:00", Ended: true},
		want: PlayStatus{
			Phase:    phaseRunning,
			Pod:      "movie-playback",
			Item:     2,
			Position: "0:12:30",
			Duration: "1:45:00",
		},
	}, {
		name:   "a pod phase this operator does not map is Pending",
		player: player,
		pod:    playbackPod("Unknown"),
		want:   PlayStatus{Phase: phasePending, Pod: "movie-playback"},
	}}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			// TestPlayActivity tests the activity.
			one.want.Activity = playActivity(one.want.Phase, one.want.Paused)
			got := derivePlayStatus(statusTestPlay(), one.player, one.buildErr, one.pod, one.report, resolvedPreferences{})
			if !reflect.DeepEqual(got, one.want) {
				t.Errorf("status = %+v, want %+v", got, one.want)
			}
		})
	}
}

func TestDerivePlayStatusCarriesTheLastPositionForward(t *testing.T) {
	player := &Player{Metadata: ObjectMeta{Name: "theater", Namespace: "house"}}
	play := statusTestPlay()
	play.Status = PlayStatus{
		Phase:    phaseRunning,
		Pod:      "movie-playback",
		Item:     2,
		Position: "0:12:30",
		Duration: "1:45:00",
	}

	cases := []struct {
		name string
		pod  *Pod
	}{
		{name: "a running pod whose report is gone", pod: playbackPod(podRunning)},
		{name: "a failed pod keeps the place for a resume", pod: playbackPod(podFailed)},
		{name: "the pod is gone while the recreate waits out its backoff", pod: nil},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := derivePlayStatus(play, player, nil, one.pod, nil, resolvedPreferences{})
			if got.Position != "0:12:30" {
				t.Errorf("position = %q, want it carried forward as 0:12:30", got.Position)
			}
		})
	}
}

func TestDerivePlayStatusReportsPreferencesAndChosenTracks(t *testing.T) {
	player := &Player{Metadata: ObjectMeta{Name: "theater", Namespace: "house"}}
	prefs := resolvedPreferences{
		AudioLanguages:    []string{"de"},
		SubtitleLanguages: []string{"en"},
		Subtitles:         subtitlesAuto,
		SDH:               sdhAvoid,
	}
	report := &playReport{Item: 1, Position: "0:01:00", AudioLanguage: "eng", SubtitleLanguage: "eng"}

	got := derivePlayStatus(statusTestPlay(), player, nil, playbackPod(podRunning), report, prefs)

	if !reflect.DeepEqual(got.AudioLanguages, []string{"de"}) ||
		!reflect.DeepEqual(got.SubtitleLanguages, []string{"en"}) ||
		got.Subtitles != subtitlesAuto ||
		got.SDH != sdhAvoid {
		t.Errorf("resolved preferences = %+v, want audio de, subtitles en, auto subtitles, and SDH avoided", got)
	}
	// The preference was German audio, and mpv played English.
	if got.AudioLanguage != "eng" || got.SubtitleLanguage != "eng" {
		t.Errorf("chosen tracks = audio %q, subtitle %q, want eng and eng",
			got.AudioLanguage, got.SubtitleLanguage)
	}
}

func TestDerivePlayStatusCarriesTheChosenLanguagesForward(t *testing.T) {
	player := &Player{Metadata: ObjectMeta{Name: "theater", Namespace: "house"}}
	play := statusTestPlay()
	play.Status = PlayStatus{
		Phase:            phaseRunning,
		Pod:              "movie-playback",
		AudioLanguage:    "jpn",
		SubtitleLanguage: "eng",
	}

	got := derivePlayStatus(play, player, nil, playbackPod(podRunning), nil, resolvedPreferences{})
	if got.AudioLanguage != "jpn" || got.SubtitleLanguage != "eng" {
		t.Errorf("chosen tracks = audio %q, subtitle %q, want them carried forward",
			got.AudioLanguage, got.SubtitleLanguage)
	}
}

func TestPlayActivity(t *testing.T) {
	cases := []struct {
		name   string
		phase  string
		paused bool
		want   string
	}{
		{name: "pending is starting", phase: phasePending, want: activityStarting},
		{name: "running and not paused is playing", phase: phaseRunning, want: activityPlaying},
		{name: "running and paused is paused", phase: phaseRunning, paused: true, want: activityPaused},
		{name: "finished stays finished", phase: phaseFinished, want: activityFinished},
		{name: "failed stays failed", phase: phaseFailed, want: activityFailed},
		{name: "an unwritten phase has no activity", phase: "", want: ""},
		{name: "paused matters only while running", phase: phaseFinished, paused: true, want: activityFinished},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := playActivity(one.phase, one.paused); got != one.want {
				t.Errorf("playActivity(%q, %v) = %q, want %q", one.phase, one.paused, got, one.want)
			}
		})
	}
}

// statusAPI is a fake API server that stores one Play and returns 409
// for the first conflicts status writes.
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
			t.Errorf("the API server received %s %s, which the test does not serve", r.Method, r.URL.Path)
		}
	})
}

func TestAnUnchangedStatusIsNotWritten(t *testing.T) {
	api := &statusAPI{}
	play := statusTestPlay()
	play.Status = PlayStatus{Phase: phaseRunning, Pod: "movie-playback", Position: "0:01:00"}

	err := writePlayStatus(testAPIClient(t, api.handler(t)), nil, play, play.Status)
	if err != nil {
		t.Fatalf("writing an unchanged status: %v", err)
	}
	if len(api.requests) != 0 {
		t.Errorf("the API server received %v, want no request for an unchanged status", api.requests)
	}
}

func TestAChangedStatusIsWrittenOnce(t *testing.T) {
	api := &statusAPI{}
	play := statusTestPlay()
	play.Status = PlayStatus{Phase: phasePending}
	desired := PlayStatus{Phase: phaseRunning, Pod: "movie-playback", Item: 1}

	if err := writePlayStatus(testAPIClient(t, api.handler(t)), nil, play, desired); err != nil {
		t.Fatalf("writing the changed status: %v", err)
	}
	if len(api.writes) != 1 {
		t.Fatalf("writes = %d, want one: %v", len(api.writes), api.requests)
	}
	if !reflect.DeepEqual(api.writes[0].Status, desired) {
		t.Errorf("status = %+v, want %+v", api.writes[0].Status, desired)
	}
}

func TestAConflictIsRetriedOnceWithTheFreshVersion(t *testing.T) {
	api := &statusAPI{conflicts: 1}
	api.stored = *statusTestPlay()
	api.stored.Metadata.ResourceVersion = "13"

	play := statusTestPlay()
	desired := PlayStatus{Phase: phaseRunning, Pod: "movie-playback"}

	if err := writePlayStatus(testAPIClient(t, api.handler(t)), nil, play, desired); err != nil {
		t.Fatalf("writing the changed status: %v", err)
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

func TestAConflictWhoseWinnerWroteTheSameStatusStopsThere(t *testing.T) {
	desired := PlayStatus{Phase: phaseRunning, Pod: "movie-playback"}
	api := &statusAPI{conflicts: 1}
	api.stored = *statusTestPlay()
	api.stored.Metadata.ResourceVersion = "13"
	api.stored.Status = desired

	play := statusTestPlay()
	if err := writePlayStatus(testAPIClient(t, api.handler(t)), nil, play, desired); err != nil {
		t.Fatalf("writing the status after a conflict: %v", err)
	}
	if len(api.writes) != 0 {
		t.Errorf("the retry wrote a status that was already there: %+v", api.writes)
	}
}

func TestASecondConflictIsReported(t *testing.T) {
	api := &statusAPI{conflicts: 2}
	api.stored = *statusTestPlay()
	api.stored.Metadata.ResourceVersion = "13"

	err := writePlayStatus(testAPIClient(t, api.handler(t)), nil, statusTestPlay(), PlayStatus{Phase: phaseRunning})
	if !errors.Is(err, apiclient.ErrConflict) {
		t.Fatalf("err = %v, want %v", err, apiclient.ErrConflict)
	}
}
