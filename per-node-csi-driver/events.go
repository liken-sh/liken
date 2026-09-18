package main

// events.go posts an Event on the pod that mounts a volume. Events are
// what kubectl describe shows, so a refused mount is explained where a
// person looks first.

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

// These three reasons identify each refusal a person has to
// read: a handle the driver cannot put under the store, a handle
// another pod on the node holds, and a copy or target the driver could
// not make or bind.
const (
	reasonRefused     = "PerNodeVolumeRefused"
	reasonHeld        = "PerNodeVolumeHeld"
	reasonMountFailed = "PerNodeMountFailed"
)

// podReference is the pod the kubelet named in the volume context. An
// Event about the publish goes on this pod.
type podReference struct {
	name      string
	namespace string
	uid       string
}

// The keys the kubelet adds to the volume context itself. podInfoOnMount
// on the CSIDriver object turns them on.
const (
	podNameKey      = "csi.storage.k8s.io/pod.name"
	podNamespaceKey = "csi.storage.k8s.io/pod.namespace"
	podUIDKey       = "csi.storage.k8s.io/pod.uid"
)

// podOf reads the pod straight from the volume context, so a refused
// publish still knows where its Event goes.
func podOf(context map[string]string) podReference {
	return podReference{
		name:      context[podNameKey],
		namespace: context[podNamespaceKey],
		uid:       context[podUIDKey],
	}
}

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
	if e.client == nil || pod.name == "" || pod.namespace == "" {
		return
	}
	involved := corev1.ObjectReference{
		Kind:       "Pod",
		APIVersion: "v1",
		Name:       pod.name,
		Namespace:  pod.namespace,
		UID:        types.UID(pod.uid),
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
			"pod", involved.Namespace+"/"+involved.Name, "reason", reason, "error", err)
	}
}
