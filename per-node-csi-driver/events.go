package main

// events.go posts an Event on the pod that mounts a volume. Events are
// what kubectl describe shows, so a refused mount is explained where a
// person looks first.

import (
	"context"
	"io"
	"log/slog"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	kevents "github.com/liken-sh/liken/kubernetes/events"
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

// events holds the driver's two ways into the cluster: the typed
// clientset that the sweep watches PersistentVolumes through, and the
// recorder that posts Events. Both are nil when the driver runs
// outside a cluster.
//
// The recorder is the shared writer of kubernetes/events. It folds a
// repeat of the same Event into the Event already posted, so a pod
// that the kubelet tries to mount again and again carries one line,
// such as "(x37 over 1h)", and not one Event for each attempt.
type events struct {
	client   kubernetes.Interface
	recorder *kevents.Recorder
}

// newEvents reads the driver's own credentials from the pod it runs in.
// The recorder writes until ctx ends.
func newEvents(ctx context.Context, nodeID string, logger *slog.Logger) *events {
	return eventsFrom(ctx, nodeID, logger, rest.InClusterConfig)
}

// eventsFrom builds the clients from the configuration load returns. A
// driver that finds no cluster still serves volumes and says so once,
// because a mount is worth more than an Event.
//
// The clientset and the recorder share one HTTP client, so they share
// its connections and its way of reading the ServiceAccount token.
func eventsFrom(
	ctx context.Context, nodeID string, logger *slog.Logger, load func() (*rest.Config, error),
) *events {
	config, err := load()
	if err != nil {
		logger.Warn("no events", "reason", err)
		return &events{}
	}
	httpClient, err := rest.HTTPClientFor(config)
	if err != nil {
		logger.Warn("no events", "reason", err)
		return &events{}
	}
	client, err := kubernetes.NewForConfigAndClient(config, httpClient)
	if err != nil {
		logger.Warn("no events", "reason", err)
		return &events{}
	}
	return &events{
		client: client,
		recorder: kevents.New(ctx, apiclient.New(config.Host, httpClient, ""), driverName,
			kevents.Options{Instance: nodeID, Log: logTo(logger)}),
	}
}

// logTo answers a writer that logs each line the recorder writes, an
// Event it could not post, as one warning of the driver's own log.
func logTo(logger *slog.Logger) io.Writer {
	return slog.NewLogLogger(logger.Handler(), slog.LevelWarn).Writer()
}

// refuse posts one Warning on the pod. The recorder queues it and
// returns at once, so a mount never waits on the API server and never
// fails because of it.
func (e *events) refuse(pod podReference, reason, message string) {
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
