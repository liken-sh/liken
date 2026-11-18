package main

// events.go posts Events on the pod that mounts a volume, so that
// kubectl describe explains a refused or stale mount.

import (
	"context"
	"io"
	"log/slog"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	kevents "github.com/liken-sh/liken/kubernetes/events"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	reasonRefused = "GitVolumeRefused"
	reasonStale   = "GitVolumeStale"
	reasonFailed  = "GitFetchFailed"
	// reasonRecovered marks the first fetch that succeeds after
	// GitFetchFailed or GitVolumeStale.
	reasonRecovered = "GitFetchRecovered"
	reasonArmed     = "GitVolumeArmed"
	reasonUnarmed   = "GitVolumeUnarmed"
	// reasonPending reports changes in the tree that the driver has not
	// committed yet.
	reasonPending    = "GitVolumePending"
	reasonPushed     = "GitVolumePushed"
	reasonPushFailed = "GitVolumePushFailed"
	// reasonSkipped reports a file that the size limit left out of a
	// commit.
	reasonSkipped = "GitVolumeFileSkipped"
	// reasonDiverged reports that the volume pushes to its side branch.
	// reasonHealed reports that the ref came to contain the volume's
	// commits, so the volume pushes to the ref again.
	reasonDiverged = "GitVolumeDiverged"
	reasonHealed   = "GitVolumeHealed"
	// reasonRebased reports that the remote rejected a push, and the
	// driver rebased its commits onto the remote's ref and pushed again.
	reasonRebased = "GitVolumeRebased"
	// The stage posts these two: upstream moved while the tree had
	// uncommitted changes, and the remote deleted the ref.
	reasonUpstreamMoved = "GitVolumeUpstreamMoved"
	reasonRefDeleted    = "GitVolumeRefDeleted"
	// reasonNoPublishSecret reports a PersistentVolume with a stage
	// Secret and no publish Secret. After a driver restart, such a volume
	// does not fetch or push until the kubelet stages it again.
	reasonNoPublishSecret = "GitVolumeNoPublishSecret"
)

// events has the node plugin's clients for the cluster: the typed
// clientset that arming and the PersistentVolume watch use, and the
// kubernetes/events recorder. Both are nil when the driver runs outside
// a cluster.
//
// The recorder folds a repeated Event into the one it already posted,
// so when the kubelet retries a mount, kubectl describe shows one line
// such as "(x37 over 1h)" and not one Event for each attempt.
type events struct {
	client   kubernetes.Interface
	recorder *kevents.Recorder
}

// newEvents builds the clients from the driver pod's in-cluster
// credentials. The recorder writes until ctx ends.
func newEvents(ctx context.Context, nodeID string, logger *slog.Logger) *events {
	return eventsFrom(ctx, nodeID, logger, rest.InClusterConfig)
}

// eventsFrom builds the clients from the configuration that load
// returns. When load or a client fails, it logs a warning and returns
// no clients, so the driver still mounts volumes and posts no Events.
// The clientset and the recorder share one HTTP client, and so share
// its connections and its ServiceAccount token.
func eventsFrom(
	ctx context.Context, nodeID string, logger *slog.Logger, load func() (*rest.Config, error),
) *events {
	config, err := load()
	if err != nil {
		logger.Warn("The driver has no cluster client and posts no Events", "error", err)
		return &events{}
	}
	httpClient, err := rest.HTTPClientFor(config)
	if err != nil {
		logger.Warn("The driver has no cluster client and posts no Events", "error", err)
		return &events{}
	}
	client, err := kubernetes.NewForConfigAndClient(config, httpClient)
	if err != nil {
		logger.Warn("The driver has no cluster client and posts no Events", "error", err)
		return &events{}
	}
	return &events{
		client: client,
		recorder: kevents.New(ctx, apiclient.New(config.Host, httpClient, ""), driverName,
			kevents.Options{Instance: nodeID, Log: logTo(logger)}),
	}
}

// logTo returns a writer that logs each line as a warning. The
// recorder writes each Event that it could not post to it.
func logTo(logger *slog.Logger) io.Writer {
	return slog.NewLogLogger(logger.Handler(), slog.LevelWarn).Writer()
}

// post queues an Event on the pod. The recorder returns at once, so a
// mount never waits for the API server or fails because of it.
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

// postClaim queues an Event on the PersistentVolumeClaim.
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

// create queues an Event. Outside a cluster the recorder is nil, and
// its methods post nothing.
func (e *events) create(involved kevents.ObjectReference, kind, reason, message string) {
	if kind == kevents.TypeWarning {
		e.recorder.Warning(involved, reason, message)
		return
	}
	e.recorder.Normal(involved, reason, message)
}

// postVolumeEvent posts an Event for a volume. An inline volume and a writeable
// volume post on their pod. A read-only claim posts on every pod on
// this node that mounts it, and on the claim.
func (n *node) postVolumeEvent(vol *volume, kind, reason, message string) {
	if vol.kind != readOnlyClaim {
		n.events.post(vol.podRef(), kind, reason, message)
		return
	}
	for _, pod := range vol.boundPods() {
		n.events.post(pod, kind, reason, message)
	}
	n.events.postClaim(vol.claimNow(), kind, reason, message)
}

// report posts an Event on the volume's pod and on the given claim.
func (n *node) report(vol *volume, claim claimReference, kind, reason, message string) {
	n.events.post(vol.podRef(), kind, reason, message)
	n.events.postClaim(claim, kind, reason, message)
}
