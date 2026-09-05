package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd/api"
)

// fakeEvents posts to a client-go fake instead of a cluster.
func fakeEvents(t *testing.T, logs io.Writer) *events {
	t.Helper()
	client := fake.NewClientset()
	nameEvents(client)
	return &events{
		client: client,
		node:   "node-1",
		logger: slog.New(slog.NewTextHandler(logs, nil)),
		now:    func() time.Time { return time.Unix(1757000000, 0).UTC() },
	}
}

// The fake makes no name out of generateName, and an API server does, so
// two Events of one volume would collide in the fixture and never in a
// cluster.
func nameEvents(client *fake.Clientset) {
	named := 0
	client.PrependReactor("create", "events",
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			created, ok := action.(k8stesting.CreateAction).GetObject().(*corev1.Event)
			if ok && created.Name == "" {
				named++
				created.Name = fmt.Sprintf("%s%04d", created.GenerateName, named)
			}
			return false, nil, nil
		})
}

// postedEvents is every Event the fake took, in the order posted.
func postedEvents(t *testing.T, posting *events) []corev1.Event {
	t.Helper()
	list, err := posting.client.CoreV1().Events("").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing the events: %v", err)
	}
	return list.Items
}

func TestPostNamesThePodAndTheDriver(t *testing.T) {
	posting := fakeEvents(t, io.Discard)
	pod := podReference{name: "reader", namespace: "home", uid: "9b1c"}
	posting.post(t.Context(), pod, corev1.EventTypeWarning, reasonRefused, "the forge is not there")

	posted := postedEvents(t, posting)
	if len(posted) != 1 {
		t.Fatalf("post made %d events, want 1", len(posted))
	}
	event := posted[0]
	if event.InvolvedObject.Kind != "Pod" || event.InvolvedObject.Name != "reader" ||
		event.InvolvedObject.Namespace != "home" || string(event.InvolvedObject.UID) != "9b1c" {
		t.Errorf("the event is on %+v, want the pod", event.InvolvedObject)
	}
	if event.Reason != reasonRefused || event.Message != "the forge is not there" {
		t.Errorf("the event says %q: %q", event.Reason, event.Message)
	}
	if event.Type != corev1.EventTypeWarning {
		t.Errorf("the event is a %q, want %q", event.Type, corev1.EventTypeWarning)
	}
	if event.Source.Component != driverName || event.Source.Host != "node-1" {
		t.Errorf("the event came from %+v, want the driver on node-1", event.Source)
	}
	if event.ObjectMeta.GenerateName != "reader." {
		t.Errorf("the event is named %q, want a name generated from the pod", event.ObjectMeta.GenerateName)
	}
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
			posting := fakeEvents(t, io.Discard)
			posting.post(t.Context(), c.pod, corev1.EventTypeNormal, reasonStale, "stale")
			if posted := postedEvents(t, posting); len(posted) != 0 {
				t.Errorf("post made %v", posted)
			}
		})
	}

	var missing *events
	missing.post(t.Context(), podReference{name: "reader", namespace: "home"},
		corev1.EventTypeNormal, reasonStale, "stale")
	outside := &events{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	outside.post(t.Context(), podReference{name: "reader", namespace: "home"},
		corev1.EventTypeNormal, reasonStale, "stale")
}

func TestPostLogsAnEventTheAPIServerRefused(t *testing.T) {
	logs := &strings.Builder{}
	posting := fakeEvents(t, logs)
	client := posting.client.(*fake.Clientset)
	client.PrependReactor("create", "events",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("the api server said no")
		})

	posting.post(t.Context(), podReference{name: "reader", namespace: "home"},
		corev1.EventTypeWarning, reasonFailed, "the fetch failed")
	if !strings.Contains(logs.String(), "the api server said no") {
		t.Errorf("the log is %q, want the refusal in it", logs)
	}
}

func TestNewEventsRunsOnWithNoCluster(t *testing.T) {
	logs := &strings.Builder{}
	posting := newEvents("node-1", slog.New(slog.NewTextHandler(logs, nil)))
	if posting.client != nil {
		t.Error("newEvents found a cluster in a test process")
	}
	if !strings.Contains(logs.String(), "no events") {
		t.Errorf("the log is %q, want the missing cluster in it", logs)
	}
	posting.post(t.Context(), podReference{name: "reader", namespace: "home"},
		corev1.EventTypeNormal, reasonStale, "stale")
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
			name: "a configuration client-go refuses",
			load: func() (*rest.Config, error) {
				return &rest.Config{Host: "https://10.43.0.1:443", ExecProvider: &api.ExecConfig{},
					AuthProvider: &api.AuthProviderConfig{Name: "gcp"}}, nil
			},
			wantClient: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := &strings.Builder{}
			posting := eventsFrom("node-1", slog.New(slog.NewTextHandler(logs, nil)), c.load)
			if (posting.client != nil) != c.wantClient {
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
	posting := fakeEvents(t, io.Discard)
	claim := claimReference{namespace: "home", name: "config"}
	posting.postClaim(t.Context(), claim, corev1.EventTypeNormal, reasonArmed, "armed by the class config-eager")

	posted := postedEvents(t, posting)
	if len(posted) != 1 {
		t.Fatalf("postClaim made %d events, want 1", len(posted))
	}
	involved := posted[0].InvolvedObject
	if involved.Kind != "PersistentVolumeClaim" || involved.Name != "config" || involved.Namespace != "home" {
		t.Errorf("the event is on %+v, want the claim", involved)
	}
}

func TestPostClaimSaysNothingWithoutAClaim(t *testing.T) {
	for _, claim := range []claimReference{
		{namespace: "home"},
		{name: "config"},
	} {
		posting := fakeEvents(t, io.Discard)
		posting.postClaim(t.Context(), claim, corev1.EventTypeNormal, reasonArmed, "armed")
		if posted := postedEvents(t, posting); len(posted) != 0 {
			t.Errorf("postClaim made %v", posted)
		}
	}
}
