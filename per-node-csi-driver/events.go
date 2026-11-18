package main

// events.go posts a Warning Event on the pod whose mount failed, so the
// reason shows in kubectl describe pod.

import (
	"context"
	"io"
	"log/slog"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	kevents "github.com/liken-sh/liken/kubernetes/events"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// reasonRefused is for a volume handle that the driver cannot use as a
// directory name. reasonMountFailed is for a copy that the driver could
// not create or bind mount onto the target.
const (
	reasonRefused     = "PerNodeVolumeRefused"
	reasonMountFailed = "PerNodeMountFailed"
)

type podReference struct {
	name      string
	namespace string
	uid       string
}

// The kubelet adds these keys to the volume context of NodePublishVolume
// when the CSIDriver object sets podInfoOnMount.
const (
	podNameKey      = "csi.storage.k8s.io/pod.name"
	podNamespaceKey = "csi.storage.k8s.io/pod.namespace"
	podUIDKey       = "csi.storage.k8s.io/pod.uid"
)

// podOf reads the pod from the volume context, with no API call, so a
// publish that fails before anything else still has a pod to post on.
func podOf(context map[string]string) podReference {
	return podReference{
		name:      context[podNameKey],
		namespace: context[podNamespaceKey],
		uid:       context[podUIDKey],
	}
}

// events holds the driver's Kubernetes clients: the clientset that the
// sweep watches PersistentVolumes with, and the kubernetes/events
// recorder. Both are nil when the driver runs outside a cluster. The
// recorder merges a repeat of an Event into the Event it already
// posted, so a publish that the kubelet retries for an hour shows as
// one Event with a count.
type events struct {
	client   kubernetes.Interface
	recorder *kevents.Recorder
}

// newEvents builds the clients from the pod's ServiceAccount. The
// recorder runs until ctx ends.
func newEvents(ctx context.Context, nodeID string, logger *slog.Logger) *events {
	return eventsFrom(ctx, nodeID, logger, rest.InClusterConfig)
}

// eventsFrom returns events with nil clients, and logs a warning, when
// it cannot build them. The driver still mounts volumes without them,
// because a mount matters more than an Event. The clientset and the
// recorder share one HTTP client, so they share its connections and
// its token refresh.
func eventsFrom(
	ctx context.Context, nodeID string, logger *slog.Logger, load func() (*rest.Config, error),
) *events {
	config, err := load()
	if err != nil {
		logger.Warn("The cluster client is unavailable, so the driver posts no Events", "error", err)
		return &events{}
	}
	httpClient, err := rest.HTTPClientFor(config)
	if err != nil {
		logger.Warn("The cluster client is unavailable, so the driver posts no Events", "error", err)
		return &events{}
	}
	client, err := kubernetes.NewForConfigAndClient(config, httpClient)
	if err != nil {
		logger.Warn("The cluster client is unavailable, so the driver posts no Events", "error", err)
		return &events{}
	}
	return &events{
		client: client,
		recorder: kevents.New(ctx, apiclient.New(config.Host, httpClient, ""), driverName,
			kevents.Options{Instance: nodeID, Log: logTo(logger)}),
	}
}

// logTo returns the writer that the recorder logs each Event it could
// not post to. It writes each line as a warning.
func logTo(logger *slog.Logger) io.Writer {
	return slog.NewLogLogger(logger.Handler(), slog.LevelWarn).Writer()
}

// warn posts a Warning Event on the pod. The recorder queues the
// Event and returns at once, so a mount neither waits for the API
// server nor fails with it. A pod the kubelet did not name gets no
// Event.
func (e *events) warn(pod podReference, reason, message string) {
	if pod.name == "" || pod.namespace == "" {
		return
	}
	e.recorder.Warning(kevents.ObjectReference{
		APIVersion: "v1",
		Kind:       "Pod",
		Namespace:  pod.namespace,
		Name:       pod.name,
		UID:        pod.uid,
	}, reason, message)
}
