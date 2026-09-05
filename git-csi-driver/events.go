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
	// The three a writeable volume adds: a class armed it, the class left
	// it, and the tree holds work the driver has not committed.
	reasonArmed   = "GitVolumeArmed"
	reasonUnarmed = "GitVolumeUnarmed"
	reasonPending = "GitVolumePending"
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
	if pod.name == "" || pod.namespace == "" {
		return
	}
	e.create(ctx, corev1.ObjectReference{
		Kind:       "Pod",
		APIVersion: "v1",
		Name:       pod.name,
		Namespace:  pod.namespace,
		UID:        types.UID(pod.uid),
	}, kind, reason, message)
}

// postClaim creates the same Event on the claim, where a person who
// describes the claim learns whether the volume is armed.
func (e *events) postClaim(ctx context.Context, claim claimReference, kind, reason, message string) {
	if claim.name == "" || claim.namespace == "" {
		return
	}
	e.create(ctx, corev1.ObjectReference{
		Kind:       "PersistentVolumeClaim",
		APIVersion: "v1",
		Name:       claim.name,
		Namespace:  claim.namespace,
	}, kind, reason, message)
}

// create posts one Event on the object it names.
func (e *events) create(
	ctx context.Context, involved corev1.ObjectReference, kind, reason, message string,
) {
	if e == nil || e.client == nil {
		return
	}
	now := metav1.NewTime(e.now())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: involved.Name + ".",
			Namespace:    involved.Namespace,
		},
		InvolvedObject: involved,
		Reason:         reason,
		Message:        message,
		Type:           kind,
		Source:         corev1.EventSource{Component: driverName, Host: e.node},
		FirstTimestamp: now,
		LastTimestamp:  now,
		Count:          1,
	}
	if _, err := e.client.CoreV1().Events(involved.Namespace).
		Create(ctx, event, metav1.CreateOptions{}); err != nil {
		e.logger.WarnContext(ctx, "the event was not posted",
			"object", involved.Namespace+"/"+involved.Name, "reason", reason, "error", err)
	}
}

// report posts one fact in both places a person looks: on the pod that
// mounts the volume and on the claim that binds it.
func (n *node) report(
	ctx context.Context, held *volume, claim claimReference, kind, reason, message string,
) {
	n.events.post(ctx, held.podRef(), kind, reason, message)
	n.events.postClaim(ctx, claim, kind, reason, message)
}
