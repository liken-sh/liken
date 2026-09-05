package main

// events.go posts an Event on the pod that mounts a volume. Events are
// what kubectl describe shows, so a refused or stale mount is explained
// where a person looks first.

import (
	"context"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// The reasons, one per state change a person has to see.
const (
	reasonRefused = "GitVolumeRefused"
	reasonStale   = "GitVolumeStale"
	reasonFailed  = "GitFetchFailed"
)

// events posts Events through the cluster's API, or posts nothing when
// the driver runs outside a cluster.
type events struct {
	client kubernetes.Interface
	node   string
	logger *slog.Logger
	now    func() time.Time
}

// newEvents reads the driver's own credentials from the pod it runs in.
func newEvents(nodeID string, logger *slog.Logger) *events {
	return eventsFrom(nodeID, logger, rest.InClusterConfig)
}

// eventsFrom builds the client from the configuration load returns. A
// driver that finds no cluster still serves volumes and says so once,
// because a mount is worth more than an Event.
func eventsFrom(nodeID string, logger *slog.Logger, load func() (*rest.Config, error)) *events {
	posting := &events{node: nodeID, logger: logger, now: time.Now}
	config, err := load()
	if err != nil {
		logger.Warn("no events", "reason", err)
		return posting
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Warn("no events", "reason", err)
		return posting
	}
	posting.client = client
	return posting
}

// post creates one Event on the pod. A failure to post is logged and
// nothing more, because a mount must never fail on the API server.
func (e *events) post(ctx context.Context, pod podReference, kind, reason, message string) {
	if e == nil || e.client == nil || pod.name == "" || pod.namespace == "" {
		return
	}
	now := metav1.NewTime(e.now())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: pod.name + ".",
			Namespace:    pod.namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "Pod",
			APIVersion: "v1",
			Name:       pod.name,
			Namespace:  pod.namespace,
			UID:        types.UID(pod.uid),
		},
		Reason:         reason,
		Message:        message,
		Type:           kind,
		Source:         corev1.EventSource{Component: driverName, Host: e.node},
		FirstTimestamp: now,
		LastTimestamp:  now,
		Count:          1,
	}
	if _, err := e.client.CoreV1().Events(pod.namespace).
		Create(ctx, event, metav1.CreateOptions{}); err != nil {
		e.logger.WarnContext(ctx, "the event was not posted",
			"pod", pod.namespace+"/"+pod.name, "reason", reason, "error", err)
	}
}
