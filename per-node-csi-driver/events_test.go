package main

import (
	"bytes"
	"errors"
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
)

// postingTo builds an events client on the fake cluster, with a fixed
// clock so a test can read the timestamps it wrote.
func postingTo(client *fake.Clientset, logger *slog.Logger) *events {
	return &events{
		client: client,
		node:   "node-1",
		logger: logger,
		now:    func() time.Time { return time.Unix(1757000000, 0).UTC() },
	}
}

func TestAnEventLandsOnThePodTheKubeletNamed(t *testing.T) {
	client := fake.NewClientset()
	postingTo(client, quietLogger()).post(t.Context(), aPod("reader", "pod-uid-1"),
		corev1.EventTypeWarning, reasonHeld, "some-cache is held by pod example/other")

	posted, err := client.CoreV1().Events("example").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing the events: %v", err)
	}
	if len(posted.Items) != 1 {
		t.Fatalf("the cluster carries %d events, want one", len(posted.Items))
	}
	event := posted.Items[0]
	if event.InvolvedObject.Kind != "Pod" || event.InvolvedObject.Name != "reader" {
		t.Errorf("the event names %+v, want the pod reader", event.InvolvedObject)
	}
	if event.Reason != reasonHeld || event.Source.Component != driverName {
		t.Errorf("the event reads %s from %s, want %s from %s",
			event.Reason, event.Source.Component, reasonHeld, driverName)
	}
	if event.Source.Host != "node-1" {
		t.Errorf("the event names host %q, want node-1", event.Source.Host)
	}
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
			client := fake.NewClientset()
			postingTo(client, quietLogger()).post(t.Context(), c.pod,
				corev1.EventTypeWarning, reasonRefused, "no")

			posted, err := client.CoreV1().Events("example").List(t.Context(), metav1.ListOptions{})
			if err != nil {
				t.Fatalf("listing the events: %v", err)
			}
			if len(posted.Items) != 0 {
				t.Errorf("the cluster carries %d events, want none", len(posted.Items))
			}
		})
	}
}

func TestADriverOutsideAClusterPostsNoEvent(t *testing.T) {
	written := &bytes.Buffer{}
	posting := eventsFrom("node-1", slog.New(slog.NewTextHandler(written, nil)),
		func() (*rest.Config, error) { return nil, errors.New("no cluster") })
	if posting.client != nil {
		t.Error("the driver built a client, want none")
	}
	posting.post(t.Context(), aPod("reader", "pod-uid-1"),
		corev1.EventTypeWarning, reasonRefused, "no")
	if !strings.Contains(written.String(), "no events") {
		t.Errorf("the log reads %q, want it to say there are no events", written)
	}
}

func TestADriverInAClusterPostsThroughItsOwnCredential(t *testing.T) {
	posting := eventsFrom("node-1", quietLogger(), func() (*rest.Config, error) {
		return &rest.Config{Host: "https://127.0.0.1:6443"}, nil
	})
	if posting.client == nil {
		t.Error("the driver built no client, want one")
	}
}

func TestAClusterCredentialTheDriverCannotUsePostsNoEvent(t *testing.T) {
	written := &bytes.Buffer{}
	posting := eventsFrom("node-1", slog.New(slog.NewTextHandler(written, nil)),
		func() (*rest.Config, error) {
			// A rate the client builder cannot honour: it takes a
			// queries-per-second with no burst to spend it from.
			return &rest.Config{Host: "https://127.0.0.1:6443", QPS: 1, Burst: 0}, nil
		})
	if posting.client != nil {
		t.Error("the driver built a client, want none")
	}
	if !strings.Contains(written.String(), "no events") {
		t.Errorf("the log reads %q, want it to say there are no events", written)
	}
}

func TestAnEventTheClusterRefusesIsLoggedAndTheMountStands(t *testing.T) {
	client := fake.NewClientset()
	client.PrependReactor("create", "events",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("the API server refused it")
		})
	written := &bytes.Buffer{}
	postingTo(client, slog.New(slog.NewTextHandler(written, nil))).post(t.Context(),
		aPod("reader", "pod-uid-1"), corev1.EventTypeWarning, reasonRefused, "no")

	if !strings.Contains(written.String(), "the event was not posted") {
		t.Errorf("the log reads %q, want it to say the event was not posted", written)
	}
}

func TestADriverThatFindsNoClusterCredentialSaysSoOnce(t *testing.T) {
	written := &bytes.Buffer{}
	posting := newEvents("node-1", slog.New(slog.NewTextHandler(written, nil)))
	if posting.client != nil {
		t.Error("a test process built a cluster client, want none")
	}
	if !strings.Contains(written.String(), "no events") {
		t.Errorf("the log reads %q, want it to say there are no events", written)
	}
}
