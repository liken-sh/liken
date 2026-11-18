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
	"golang.org/x/sys/unix"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd/api"
)

// fakeClusterEvents builds the driver's clients against a fake API server and
// returns the fake, so a test can read the Events it stored.
func fakeClusterEvents(t *testing.T, logger *slog.Logger) (*events, *eventstest.Events) {
	t.Helper()
	posted := &eventstest.Events{}
	server := apiservertest.Start(t, posted.Around(http.NotFoundHandler()))
	e := eventsFrom(t.Context(), "node-1", logger,
		func() (*rest.Config, error) { return server.Config(), nil })
	return e, posted
}

func TestAnEventIsPostedOnThePodTheKubeletNamed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, posted := fakeClusterEvents(t, quietLogger())
		e.warn(aPod("reader", "pod-uid-1"), reasonRefused, "The volume handle cannot be a directory name in the store: volume_id: \"Bad/Handle\" is not a DNS-1123 subdomain name")
		synctest.Wait()

		got := posted.About("Pod", "reader")
		if len(got) != 1 {
			t.Fatalf("the pod has %+v, want one Event", posted.List())
		}
		event := got[0]
		if event.InvolvedObject.Namespace != "example" || event.InvolvedObject.UID != "pod-uid-1" {
			t.Errorf("the Event is about %+v, want the pod example/reader", event.InvolvedObject)
		}
		if event.Type != kevents.TypeWarning || event.Reason != reasonRefused ||
			event.Message != "The volume handle cannot be a directory name in the store: volume_id: \"Bad/Handle\" is not a DNS-1123 subdomain name" {
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

func TestAPodTheKubeletDidNotNameGetsNoEvent(t *testing.T) {
	for _, c := range []struct {
		name string
		pod  podReference
	}{
		{name: "no pod name", pod: podReference{namespace: "example"}},
		{name: "no namespace", pod: podReference{name: "reader"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, posted := fakeClusterEvents(t, quietLogger())
				e.warn(c.pod, reasonRefused, "no")
				synctest.Wait()
				if got := posted.List(); len(got) != 0 {
					t.Errorf("the API server has %+v, want no Event", got)
				}
			})
		})
	}
}

// The kubelet retries a failed publish with a backoff that grows to
// about two minutes, so a publish that fails for an hour is retried
// about 37 times.
func TestAnHourOfRefusalsIsOneEventWithACount(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := testDriver(t)
		d.mounts.mountErr = unix.EPERM
		retried := publishing("example-store", filepath.Join(t.TempDir(), "mount"),
			aPod("reader", "pod-uid-1"))
		for range 37 {
			d.mounts.failAt = len(d.mounts.mounts) + 1
			if _, err := d.NodePublishVolume(t.Context(), retried); err == nil {
				t.Fatal("NodePublishVolume returned no error for a bind that failed")
			}
			time.Sleep(97 * time.Second)
		}
		synctest.Wait()

		refused := d.posted.About("Pod", "reader")
		if len(refused) != 1 || refused[0].Reason != reasonMountFailed || refused[0].Count != 37 {
			t.Errorf("the pod has %+v, want one %s Event with a count of 37", refused, reasonMountFailed)
		}
	})
}

func TestADriverOutsideAClusterPostsNoEvent(t *testing.T) {
	written := &bytes.Buffer{}
	e := eventsFrom(t.Context(), "node-1", slog.New(slog.NewTextHandler(written, nil)),
		func() (*rest.Config, error) { return nil, errors.New("no cluster") })
	if e.client != nil || e.recorder != nil {
		t.Error("the driver built a client, want none")
	}
	e.warn(aPod("reader", "pod-uid-1"), reasonRefused, "no")
	if !strings.Contains(written.String(), "posts no Events") {
		t.Errorf("the log reads %q, want it to say the driver posts no Events", written)
	}
}

func TestAClusterCredentialTheDriverCannotUsePostsNoEvent(t *testing.T) {
	for _, c := range []struct {
		name   string
		config *rest.Config
	}{
		{
			// The client builder rejects a QPS limit with a burst of 0.
			name:   "a rate with no burst",
			config: &rest.Config{Host: "https://127.0.0.1:6443", QPS: 1, Burst: 0},
		},
		{
			// client-go cannot build a transport when both an exec
			// provider and an auth provider supply the token.
			name: "two token sources",
			config: &rest.Config{Host: "https://127.0.0.1:6443", ExecProvider: &api.ExecConfig{},
				AuthProvider: &api.AuthProviderConfig{Name: "gcp"}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			written := &bytes.Buffer{}
			e := eventsFrom(t.Context(), "node-1", slog.New(slog.NewTextHandler(written, nil)),
				func() (*rest.Config, error) { return c.config, nil })
			if e.client != nil || e.recorder != nil {
				t.Error("the driver built a client, want none")
			}
			if !strings.Contains(written.String(), "posts no Events") {
				t.Errorf("the log reads %q, want it to say the driver posts no Events", written)
			}
		})
	}
}

// The recorder tries to post an Event three times, 10 seconds apart,
// and then logs it as a warning in the driver's log.
func TestAnEventTheAPIServerRejectsIsLoggedAsAWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		written := &bytes.Buffer{}
		e, posted := fakeClusterEvents(t, slog.New(slog.NewTextHandler(written, nil)))
		posted.Refuse(3)
		e.warn(aPod("reader", "pod-uid-1"), reasonRefused, "no")
		time.Sleep(time.Minute)
		synctest.Wait()

		if !strings.Contains(written.String(), "level=WARN") ||
			!strings.Contains(written.String(), "the API server is restarting") {
			t.Errorf("the log reads %q, want a warning with the refusal", written)
		}
		if got := posted.List(); len(got) != 0 {
			t.Errorf("the API server has %+v, want no Event", got)
		}
	})
}

func TestADriverWithNoClusterCredentialLogsThatItPostsNoEvents(t *testing.T) {
	written := &bytes.Buffer{}
	e := newEvents(t.Context(), "node-1", slog.New(slog.NewTextHandler(written, nil)))
	if e.client != nil {
		t.Error("a test process built a cluster client, want none")
	}
	if !strings.Contains(written.String(), "posts no Events") {
		t.Errorf("the log reads %q, want it to say the driver posts no Events", written)
	}
}
