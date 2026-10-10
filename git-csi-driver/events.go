package main

// events.go posts an Event on the pod that mounts a volume. Events are
// what kubectl describe shows, so a refused or stale mount is explained
// where a person looks first.

import (
	"context"
	"io"
	"log/slog"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	kevents "github.com/liken-sh/liken/kubernetes/events"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// The reasons identify state changes a person has to see. This list is
// every reason the driver posts.
const (
	reasonRefused = "GitVolumeRefused"
	reasonStale   = "GitVolumeStale"
	reasonFailed  = "GitFetchFailed"
	// The first fetch that works after a failed one, which closes the
	// GitFetchFailed or the GitVolumeStale before it.
	reasonRecovered = "GitFetchRecovered"
	// The three a writeable volume adds: a class armed it, the class left
	// it, and the tree holds work the driver has not committed.
	reasonArmed   = "GitVolumeArmed"
	reasonUnarmed = "GitVolumeUnarmed"
	reasonPending = "GitVolumePending"
	// The three an armed volume adds: a push worked, a push
	// failed, and the size guard left a file out.
	reasonPushed     = "GitVolumePushed"
	reasonPushFailed = "GitVolumePushFailed"
	reasonSkipped    = "GitVolumeFileSkipped"
	// The two the side branch adds: the volume moved to its side
	// branch, and a person merged it back.
	reasonDiverged = "GitVolumeDiverged"
	reasonHealed   = "GitVolumeHealed"
	// A push the remote rejected was rebased onto what the remote
	// holds now, and landed on the ref.
	reasonRebased = "GitVolumeRebased"
	// The two faults a stage finds in a writeable volume: upstream
	// moved while the tree held uncommitted writes, and the remote
	// deleted the ref.
	reasonUpstreamMoved = "GitVolumeUpstreamMoved"
	reasonRefDeleted    = "GitVolumeRefDeleted"
	// The PersistentVolume names a stage Secret and no publish Secret.
	// Such a volume works until the driver restarts, and then fetches
	// and pushes nothing until the kubelet stages it again.
	reasonNoPublishSecret = "GitVolumeNoPublishSecret"
)

// events holds the node plugin's two ways into the cluster: the typed
// clientset that the arming and the demand watch read through, and the
// recorder that posts Events. Both are nil when the driver runs outside
// a cluster.
//
// The recorder is the shared writer of kubernetes/events. It folds a
// repeat of the same Event into the Event already posted, so a pod that
// the kubelet tries to mount again and again carries one line, such as
// "(x37 over 1h)", and not one Event for each attempt.
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

// post queues one Event on the pod. The recorder returns at once, so a
// mount never waits on the API server and never fails because of it.
func (e *events) post(pod podReference, kind, reason, message string) {
	if pod.name == "" || pod.namespace == "" {
		return
	}
	e.create(kevents.ObjectReference{
		APIVersion: "v1",
		Kind:       "Pod",
		Namespace:  pod.namespace,
		Name:       pod.name,
		UID:        pod.uid,
	}, kind, reason, message)
}

// postClaim queues the same Event on the claim, where a person who
// describes the claim can check whether the volume is armed.
func (e *events) postClaim(claim claimReference, kind, reason, message string) {
	if claim.name == "" || claim.namespace == "" {
		return
	}
	e.create(kevents.ObjectReference{
		APIVersion: "v1",
		Kind:       "PersistentVolumeClaim",
		Namespace:  claim.namespace,
		Name:       claim.name,
	}, kind, reason, message)
}

// create queues one Event of the kind on the object it names. A nil
// recorder, outside a cluster, posts nothing.
func (e *events) create(involved kevents.ObjectReference, kind, reason, message string) {
	if kind == kevents.TypeWarning {
		e.recorder.Warning(involved, reason, message)
		return
	}
	e.recorder.Normal(involved, reason, message)
}

// tell posts one fact about a volume where its kind says a person
// reads it.
//
// An inline volume and a writeable volume report on the one pod that
// holds them. A read-only claim reports on every pod it is published to
// on this node, and on the claim the handle is bound to.
func (n *node) tell(held *volume, kind, reason, message string) {
	if held.kind != readOnlyClaim {
		n.events.post(held.podRef(), kind, reason, message)
		return
	}
	for _, pod := range held.boundPods() {
		n.events.post(pod, kind, reason, message)
	}
	n.events.postClaim(held.claimNow(), kind, reason, message)
}

// report posts one fact in both places a person looks: on the pod that
// mounts the volume and on the claim that binds it.
func (n *node) report(held *volume, claim claimReference, kind, reason, message string) {
	n.events.post(held.podRef(), kind, reason, message)
	n.events.postClaim(claim, kind, reason, message)
}
