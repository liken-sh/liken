package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	kevents "github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd/api"
)

// recorded is the fake API server that holds what one events value
// posted, and a signal for each write it answered, which a test
// outside a synctest bubble waits on.
type recorded struct {
	held    *eventstest.Events
	arrived chan struct{}
}

// recordings finds the fake API server of each events value a test
// built. A test node shares its events with a second node it builds,
// so the key is the events value and not the node.
var recordings sync.Map

// fakeEvents posts through the driver's own recorder to a fake API
// server, and reads and arms through a client-go fake.
func fakeEvents(t testing.TB, logs io.Writer) *events {
	t.Helper()
	target := recorded{held: &eventstest.Events{}, arrived: make(chan struct{}, 1)}
	served := target.held.Around(http.NotFoundHandler())
	server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.ServeHTTP(w, r)
		select {
		case target.arrived <- struct{}{}:
		default:
		}
	}))
	posting := eventsFrom(t.Context(), "node-1", slog.New(slog.NewTextHandler(logs, nil)),
		func() (*rest.Config, error) { return server.Config(), nil })
	posting.client = fake.NewClientset()
	recordings.Store(posting, target)
	t.Cleanup(func() { recordings.Delete(posting) })
	return posting
}

// recordingOf answers the fake API server the events value posts to.
func recordingOf(t testing.TB, posting *events) recorded {
	t.Helper()
	target, found := recordings.Load(posting)
	if !found {
		t.Fatal("the events value posts to no fake API server")
	}
	return target.(recorded)
}

// postedEvents is every Event the fake API server holds, in the order
// posted, once the recorder has written its queue. The test runs in a
// synctest bubble.
func postedEvents(t *testing.T, posting *events) []kevents.Event {
	t.Helper()
	synctest.Wait()
	return recordingOf(t, posting).held.List()
}

func TestPostNamesThePodAndTheDriver(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		posting := fakeEvents(t, io.Discard)
		pod := podReference{name: "reader", namespace: "home", uid: "9b1c"}
		posting.post(pod, kevents.TypeWarning, reasonRefused, "the forge is not there")

		posted := postedEvents(t, posting)
		if len(posted) != 1 {
			t.Fatalf("post made %d events, want 1", len(posted))
		}
		event := posted[0]
		if event.InvolvedObject.Kind != "Pod" || event.InvolvedObject.Name != "reader" ||
			event.InvolvedObject.Namespace != "home" || event.InvolvedObject.UID != "9b1c" {
			t.Errorf("the event is on %+v, want the pod", event.InvolvedObject)
		}
		if event.Reason != reasonRefused || event.Message != "the forge is not there" {
			t.Errorf("the event says %q: %q", event.Reason, event.Message)
		}
		if event.Type != kevents.TypeWarning {
			t.Errorf("the event is a %q, want %q", event.Type, kevents.TypeWarning)
		}
		if event.Source.Component != driverName || event.Source.Host != "node-1" ||
			event.ReportingInstance != "node-1" {
			t.Errorf("the event came from %+v on %q, want the driver on node-1",
				event.Source, event.ReportingInstance)
		}
		if event.Metadata.GenerateName != "reader." {
			t.Errorf("the event is named %q, want a name generated from the pod", event.Metadata.GenerateName)
		}
	})
}

func TestPostSaysNothingWithoutAPodOrAClient(t *testing.T) {
	for _, c := range []struct {
		name string
		pod  podReference
	}{
		{name: "no pod name", pod: podReference{namespace: "home"}},
		{name: "no pod namespace", pod: podReference{name: "reader"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				posting := fakeEvents(t, io.Discard)
				posting.post(c.pod, kevents.TypeNormal, reasonStale, "stale")
				if posted := postedEvents(t, posting); len(posted) != 0 {
					t.Errorf("post made %v", posted)
				}
			})
		})
	}

	outside := &events{}
	outside.post(podReference{name: "reader", namespace: "home"},
		kevents.TypeNormal, reasonStale, "stale")
}

// The recorder sends a refused Event three times, 10 seconds apart,
// and then logs it through the driver's own log.
func TestPostLogsAnEventTheAPIServerRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &bytes.Buffer{}
		posting := fakeEvents(t, logs)
		recordingOf(t, posting).held.Refuse(3)

		posting.post(podReference{name: "reader", namespace: "home"},
			kevents.TypeWarning, reasonFailed, "the fetch failed")
		time.Sleep(time.Minute)
		synctest.Wait()
		if !strings.Contains(logs.String(), "level=WARN") ||
			!strings.Contains(logs.String(), "the API server is restarting") {
			t.Errorf("the log is %q, want a warning with the refusal in it", logs)
		}
	})
}

func TestNewEventsRunsOnWithNoCluster(t *testing.T) {
	logs := &strings.Builder{}
	posting := newEvents(t.Context(), "node-1", slog.New(slog.NewTextHandler(logs, nil)))
	if posting.client != nil || posting.recorder != nil {
		t.Error("newEvents found a cluster in a test process")
	}
	if !strings.Contains(logs.String(), "no events") {
		t.Errorf("the log is %q, want the missing cluster in it", logs)
	}
	posting.post(podReference{name: "reader", namespace: "home"},
		kevents.TypeNormal, reasonStale, "stale")
}

func TestEventsFromACluster(t *testing.T) {
	for _, c := range []struct {
		name       string
		load       func() (*rest.Config, error)
		wantClient bool
	}{
		{
			name:       "a cluster the driver can reach",
			load:       func() (*rest.Config, error) { return &rest.Config{Host: "https://10.43.0.1:443"}, nil },
			wantClient: true,
		},
		{
			name:       "no cluster at all",
			load:       func() (*rest.Config, error) { return nil, errors.New("not in a cluster") },
			wantClient: false,
		},
		{
			name: "a configuration client-go builds no transport for",
			load: func() (*rest.Config, error) {
				return &rest.Config{Host: "https://10.43.0.1:443", ExecProvider: &api.ExecConfig{},
					AuthProvider: &api.AuthProviderConfig{Name: "gcp"}}, nil
			},
			wantClient: false,
		},
		{
			// A queries-per-second with no burst to spend it from.
			name: "a rate client-go refuses",
			load: func() (*rest.Config, error) {
				return &rest.Config{Host: "https://10.43.0.1:443", QPS: 1, Burst: 0}, nil
			},
			wantClient: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := &strings.Builder{}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			posting := eventsFrom(ctx, "node-1", slog.New(slog.NewTextHandler(logs, nil)), c.load)
			if (posting.client != nil) != c.wantClient || (posting.recorder != nil) != c.wantClient {
				t.Errorf("eventsFrom answered a client: %v, want %v (%q)",
					posting.client != nil, c.wantClient, logs)
			}
			if !c.wantClient && !strings.Contains(logs.String(), "no events") {
				t.Errorf("the log is %q, want the missing cluster in it", logs)
			}
		})
	}
}

func TestPostClaimNamesTheClaim(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		posting := fakeEvents(t, io.Discard)
		claim := claimReference{namespace: "home", name: "config"}
		posting.postClaim(claim, kevents.TypeNormal, reasonArmed, "armed by the class config-eager")

		posted := postedEvents(t, posting)
		if len(posted) != 1 {
			t.Fatalf("postClaim made %d events, want 1", len(posted))
		}
		involved := posted[0].InvolvedObject
		if involved.Kind != "PersistentVolumeClaim" || involved.Name != "config" || involved.Namespace != "home" {
			t.Errorf("the event is on %+v, want the claim", involved)
		}
		if posted[0].Type != kevents.TypeNormal {
			t.Errorf("the event is a %q, want %q", posted[0].Type, kevents.TypeNormal)
		}
	})
}

func TestPostClaimSaysNothingWithoutAClaim(t *testing.T) {
	for _, claim := range []claimReference{
		{namespace: "home"},
		{name: "config"},
	} {
		synctest.Test(t, func(t *testing.T) {
			posting := fakeEvents(t, io.Discard)
			posting.postClaim(claim, kevents.TypeNormal, reasonArmed, "armed")
			if posted := postedEvents(t, posting); len(posted) != 0 {
				t.Errorf("postClaim made %v", posted)
			}
		})
	}
}
