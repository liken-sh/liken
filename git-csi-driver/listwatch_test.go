package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	k8stesting "k8s.io/client-go/testing"
)

// script is an API server that answers a listWatch from the test's
// answers, and records what the loop asked for.
type script struct {
	mu      sync.Mutex
	lists   int
	watches []string
	acted   []string
	answers []func() (watch.Interface, error)
	listErr error
	actErr  error
}

// scripted is a listWatch on the script. Every list answers the
// version "100". Once the answers run out, every watch stays open and
// sends nothing.
func scripted(logs io.Writer, readings *metrics, s *script) *listWatch {
	return &listWatch{
		kind: persistentVolumeKind,
		list: func(context.Context) (string, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.lists++
			return "100", s.listErr
		},
		watch: func(_ context.Context, options metav1.ListOptions) (watch.Interface, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.watches = append(s.watches, options.ResourceVersion)
			if len(s.answers) == 0 {
				return watch.NewFake(), nil
			}
			answer := s.answers[0]
			s.answers = s.answers[1:]
			return answer()
		},
		act: func(_ context.Context, event watch.Event) error {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.acted = append(s.acted, event.Object.(metav1.Object).GetName())
			return s.actErr
		},
		retry:    10 * time.Millisecond,
		logger:   slog.New(slog.NewTextHandler(logs, nil)),
		readings: readings,
	}
}

// ended is a watch that sends the events, then closes.
func ended(events ...watch.Event) func() (watch.Interface, error) {
	return func() (watch.Interface, error) {
		sent := watch.NewFakeWithChanSize(len(events), false)
		for _, event := range events {
			sent.Action(event.Type, event.Object)
		}
		sent.Stop()
		return sent, nil
	}
}

// refusedWatch is a watch the API server refuses with the error.
func refusedWatch(err error) func() (watch.Interface, error) {
	return func() (watch.Interface, error) { return nil, err }
}

// changed is an event for a PersistentVolume at the version.
func changed(name, version string) watch.Event {
	return watch.Event{Type: watch.Modified, Object: &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name, ResourceVersion: version},
	}}
}

// gone is the error event an API server sends when the watch's version
// is older than the changes it still holds.
func gone() watch.Event {
	status := apierrors.NewResourceExpired("too old resource version").Status()
	return watch.Event{Type: watch.Error, Object: &status}
}

// runScript runs the loop until the script has answered the number of
// watches, then stops it, and fails when that takes longer than 30s.
func runScript(t *testing.T, loop *listWatch, s *script, watches int) {
	t.Helper()
	ctx, stop := context.WithCancel(t.Context())
	over := make(chan struct{})
	go func() {
		defer close(over)
		loop.follow(ctx)
	}()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		asked := len(s.watches)
		s.mu.Unlock()
		if asked >= watches {
			break
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	select {
	case <-over:
	case <-time.After(30 * time.Second):
		t.Fatal("the loop did not end with the driver")
	}
	if len(s.watches) < watches {
		t.Fatalf("the loop opened %d watches within 30s, want %d", len(s.watches), watches)
	}
}

func TestTheWatchResumesAfterTheListOrListsAgain(t *testing.T) {
	for _, c := range []struct {
		name    string
		answers []func() (watch.Interface, error)
		lists   int
		watches []string
	}{
		{
			name:    "the first watch opens at the list's version",
			lists:   1,
			watches: []string{"100"},
		},
		{
			name:    "a watch the API server closes resumes at the last version it sent",
			answers: []func() (watch.Interface, error){ended(changed("franchises", "104"))},
			lists:   1,
			watches: []string{"100", "104"},
		},
		{
			name: "a bookmark moves the version the next watch resumes at",
			answers: []func() (watch.Interface, error){ended(watch.Event{
				Type:   watch.Bookmark,
				Object: &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "250"}},
			})},
			lists:   1,
			watches: []string{"100", "250"},
		},
		{
			name:    "an object with no version leaves the version where it was",
			answers: []func() (watch.Interface, error){ended(changed("franchises", ""))},
			lists:   1,
			watches: []string{"100", "100"},
		},
		{
			name:    "a watch that closes before it sends anything resumes at the same version",
			answers: []func() (watch.Interface, error){ended()},
			lists:   1,
			watches: []string{"100", "100"},
		},
		{
			name:    "a watch the API server refuses resumes at the same version",
			answers: []func() (watch.Interface, error){refusedWatch(errors.New("the api server said no"))},
			lists:   1,
			watches: []string{"100", "100"},
		},
		{
			name:    "a 410 Gone on the stream lists again",
			answers: []func() (watch.Interface, error){ended(changed("franchises", "104"), gone())},
			lists:   2,
			watches: []string{"100", "100"},
		},
		{
			name: "a 410 Gone on the watch call lists again",
			answers: []func() (watch.Interface, error){
				refusedWatch(apierrors.NewResourceExpired("too old resource version")),
			},
			lists:   2,
			watches: []string{"100", "100"},
		},
		{
			name: "a 410 Gone with the reason Gone lists again",
			answers: []func() (watch.Interface, error){
				refusedWatch(apierrors.NewGone("too old resource version")),
			},
			lists:   2,
			watches: []string{"100", "100"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := &script{answers: c.answers}
			loop := scripted(io.Discard, newMetrics(), s)

			runScript(t, loop, s, len(c.watches))

			if s.lists != c.lists {
				t.Errorf("the loop listed %d times, want %d", s.lists, c.lists)
			}
			if got := strings.Join(s.watches[:len(c.watches)], " "); got != strings.Join(c.watches, " ") {
				t.Errorf("the watches opened at %q, want %q", got, strings.Join(c.watches, " "))
			}
		})
	}
}

func TestTheLoopListsAgainAfterAnEventItCouldNotRead(t *testing.T) {
	logs := &logbook{}
	s := &script{
		answers: []func() (watch.Interface, error){ended(changed("franchises", "104"))},
		actErr:  errors.New("the class config-eager was not read"),
	}
	loop := scripted(logs, newMetrics(), s)

	runScript(t, loop, s, 2)

	if s.lists != 2 {
		t.Errorf("the loop listed %d times, want 2", s.lists)
	}
	if !strings.Contains(logs.String(), "the class config-eager was not read") {
		t.Errorf("the log is %q, want the event's error in it", logs)
	}
}

func TestTheLoopListsAgainAfterAListTheAPIServerRefused(t *testing.T) {
	logs := &logbook{}
	s := &script{listErr: errors.New("the api server said no")}
	loop := scripted(logs, newMetrics(), s)
	ctx, stop := context.WithCancel(t.Context())
	over := make(chan struct{})
	go func() {
		defer close(over)
		loop.follow(ctx)
	}()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		lists := s.lists
		s.mu.Unlock()
		if lists >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	<-over

	if s.lists < 2 || len(s.watches) != 0 {
		t.Errorf("the loop listed %d times and watched %d times, want two lists and no watch",
			s.lists, len(s.watches))
	}
	if !strings.Contains(logs.String(), "the api server said no") {
		t.Errorf("the log is %q, want the API server's error in it", logs)
	}
}

func TestEveryWatchAfterTheFirstCountsOneRestart(t *testing.T) {
	readings := newMetrics()
	readings.registerNodeFacts(func() map[string]float64 { return nil })
	s := &script{answers: []func() (watch.Interface, error){
		ended(changed("franchises", "104")),
		refusedWatch(errors.New("the api server said no")),
		ended(changed("franchises", "105"), gone()),
	}}
	loop := scripted(io.Discard, readings, s)

	runScript(t, loop, s, 4)

	// Four watch calls: the first opens, the second is refused, and the
	// third and the fourth open again after a watch ended.
	if count, _ := watchRestartsOf(t, readings, persistentVolumeKind); count != 2 {
		t.Errorf("git_csi_watch_restarts_total reads %v, want 2", count)
	}
}

func TestADemandWrittenBetweenTheListAndTheWatchPullsTheTree(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	held := demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")
	want := commitFiles(t, source, map[string]string{"a.txt": "two"})

	// The list answers the state before the demand, and the demand is
	// written before the watch opens. Only a watch that opens at the
	// list's version sends it.
	client := cluster(t, answering)
	resource := schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}
	kind := schema.GroupVersionKind{Version: "v1", Kind: "PersistentVolume"}
	var once sync.Once
	client.PrependReactor("list", "persistentvolumes",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			handled := false
			var listed runtime.Object
			var err error
			once.Do(func() {
				handled = true
				listed, err = client.Tracker().List(resource, kind, "")
				if err != nil {
					return
				}
				stored, _ := client.Tracker().Get(resource, "", "franchises")
				demanded := annotated(stored.(*corev1.PersistentVolume).DeepCopy(), "2026-09-06T14:31:07Z")
				err = client.Tracker().Update(resource, demanded, "")
			})
			return handled, listed, err
		})
	watchDemands(t, answering)

	waitForCommit(t, held, want)
}
