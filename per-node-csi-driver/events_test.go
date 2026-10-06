package main

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	kevents "github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd/api"
)

// postingTo builds the driver's clients on a fake API server that
// holds the Events they post, through the same eventsFrom a driver in
// a cluster runs. In a synctest bubble the recorder's writes take no
// real time.
func postingTo(t *testing.T, logger *slog.Logger) (*events, *eventstest.Events) {
	t.Helper()
	posted := &eventstest.Events{}
	server := apiservertest.Start(t, posted.Around(http.NotFoundHandler()))
	posting := eventsFrom(t.Context(), "node-1", logger,
		func() (*rest.Config, error) { return server.Config(), nil })
	return posting, posted
}

func TestAnEventLandsOnThePodTheKubeletNamed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		posting, posted := postingTo(t, quietLogger())
		posting.refuse(aPod("reader", "pod-uid-1"), reasonHeld, "some-cache is held by pod example/other")
		synctest.Wait()

		held := posted.About("Pod", "reader")
		if len(held) != 1 {
			t.Fatalf("the pod carries %+v, want one Event", posted.List())
		}
		event := held[0]
		if event.InvolvedObject.Namespace != "example" || event.InvolvedObject.UID != "pod-uid-1" {
			t.Errorf("the Event names %+v, want the pod example/reader", event.InvolvedObject)
		}
		if event.Type != kevents.TypeWarning || event.Reason != reasonHeld ||
			event.Message != "some-cache is held by pod example/other" {
			t.Errorf("the Event reads %s %s %q, want the Warning as posted",
				event.Type, event.Reason, event.Message)
		}
		if event.Source.Component != driverName || event.Source.Host != "node-1" ||
			event.ReportingInstance != "node-1" {
			t.Errorf("the Event came from %+v on %q, want %s on node-1",
				event.Source, event.ReportingInstance, driverName)
		}
	})
}

func TestAPodTheKubeletDidNotNameCarriesNoEvent(t *testing.T) {
	for _, c := range []struct {
		name string
		pod  podReference
	}{
		{name: "no pod name", pod: podReference{namespace: "example"}},
		{name: "no namespace", pod: podReference{name: "reader"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				posting, posted := postingTo(t, quietLogger())
				posting.refuse(c.pod, reasonRefused, "no")
				synctest.Wait()
				if held := posted.List(); len(held) != 0 {
					t.Errorf("the API server holds %+v, want no Event", held)
				}
			})
		})
	}
}

// The kubelet retries a refused mount with a backoff that grows to
// about two minutes, so a pod that waits an hour for a handle another
// pod holds is refused about 37 times. The recorder folds each refusal
// into the first Event, so the pod carries one Event with the count.
func TestAnHourOfRefusalsIsOneEventWithACount(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering := testDriver(t)
		if _, err := answering.NodePublishVolume(t.Context(),
			publishing("example-store", filepath.Join(t.TempDir(), "first"),
				aPod("first", "pod-uid-1"))); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
		waiting := publishing("example-store", filepath.Join(t.TempDir(), "second"),
			aPod("second", "pod-uid-2"))
		for range 37 {
			if _, err := answering.NodePublishVolume(t.Context(), waiting); err == nil {
				t.Fatal("NodePublishVolume mounted a handle another pod holds")
			}
			time.Sleep(97 * time.Second)
		}
		synctest.Wait()

		held := answering.posted.About("Pod", "second")
		if len(held) != 1 || held[0].Reason != reasonHeld || held[0].Count != 37 {
			t.Errorf("the pod carries %+v, want one %s Event with a count of 37", held, reasonHeld)
		}
	})
}

func TestADriverOutsideAClusterPostsNoEvent(t *testing.T) {
	written := &bytes.Buffer{}
	posting := eventsFrom(t.Context(), "node-1", slog.New(slog.NewTextHandler(written, nil)),
		func() (*rest.Config, error) { return nil, errors.New("no cluster") })
	if posting.client != nil || posting.recorder != nil {
		t.Error("the driver built a client, want none")
	}
	posting.refuse(aPod("reader", "pod-uid-1"), reasonRefused, "no")
	if !strings.Contains(written.String(), "no events") {
		t.Errorf("the log reads %q, want it to say there are no events", written)
	}
}

func TestAClusterCredentialTheDriverCannotUsePostsNoEvent(t *testing.T) {
	for _, c := range []struct {
		name   string
		config *rest.Config
	}{
		{
			// A rate the client builder cannot honour: it takes a
			// queries-per-second with no burst to spend it from.
			name:   "a rate with no burst",
			config: &rest.Config{Host: "https://127.0.0.1:6443", QPS: 1, Burst: 0},
		},
		{
			// client-go builds no transport for two ways of finding a
			// token at once.
			name: "two token sources",
			config: &rest.Config{Host: "https://127.0.0.1:6443", ExecProvider: &api.ExecConfig{},
				AuthProvider: &api.AuthProviderConfig{Name: "gcp"}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			written := &bytes.Buffer{}
			posting := eventsFrom(t.Context(), "node-1", slog.New(slog.NewTextHandler(written, nil)),
				func() (*rest.Config, error) { return c.config, nil })
			if posting.client != nil || posting.recorder != nil {
				t.Error("the driver built a client, want none")
			}
			if !strings.Contains(written.String(), "no events") {
				t.Errorf("the log reads %q, want it to say there are no events", written)
			}
		})
	}
}

// The recorder sends a refused Event three times, 10 seconds apart,
// and then logs it through the driver's own log.
func TestAnEventTheClusterRefusesIsLoggedAndTheMountStands(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		written := &bytes.Buffer{}
		posting, posted := postingTo(t, slog.New(slog.NewTextHandler(written, nil)))
		posted.Refuse(3)
		posting.refuse(aPod("reader", "pod-uid-1"), reasonRefused, "no")
		time.Sleep(time.Minute)
		synctest.Wait()

		if !strings.Contains(written.String(), "level=WARN") ||
			!strings.Contains(written.String(), "the API server is restarting") {
			t.Errorf("the log reads %q, want a warning with the refusal", written)
		}
		if held := posted.List(); len(held) != 0 {
			t.Errorf("the API server holds %+v, want no Event", held)
		}
	})
}

func TestADriverThatFindsNoClusterCredentialSaysSoOnce(t *testing.T) {
	written := &bytes.Buffer{}
	posting := newEvents(t.Context(), "node-1", slog.New(slog.NewTextHandler(written, nil)))
	if posting.client != nil {
		t.Error("a test process built a cluster client, want none")
	}
	if !strings.Contains(written.String(), "no events") {
		t.Errorf("the log reads %q, want it to say there are no events", written)
	}
}
