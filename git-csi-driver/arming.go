package main

// arming.go finds the PersistentVolumeClaim that a writeable volume is
// bound to and reads its VolumeAttributesClass. The class's parameters
// are the rules that the volume commits and pushes under.

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// persistentVolumeClaimKind is the kind label that
// git_csi_watch_restarts_total has for the watch on a volume's
// claim.
const persistentVolumeClaimKind = "PersistentVolumeClaim"

// claimReference names the claim that a PersistentVolume is bound to.
// The volume's metrics use it as labels, and the node posts the
// volume's Events on it.
type claimReference struct {
	namespace string
	name      string
}

// arming reads the claims and classes of the node's volumes from the
// API server. When the driver runs outside a cluster, client is nil and
// no volume arms.
type arming struct {
	node   *node
	client kubernetes.Interface
	logger *slog.Logger
	retry  time.Duration
}

// defaultRetry is how long arming waits after a failed read of a claim
// or a class before it reads again. So a failed claim or class read
// retries at most once per interval. One read can send up to eleven
// requests, because client-go retries a request up to ten times when
// the connection resets or the server returns Retry-After. The typed
// clientset gives the caller no way to lower that count.
const defaultRetry = 30 * time.Second

func newArming(owner *node, client kubernetes.Interface, logger *slog.Logger) *arming {
	return &arming{node: owner, client: client, logger: logger, retry: defaultRetry}
}

// arm starts a goroutine that watches the volume's claim until disarm
// or the driver's shutdown. The caller holds the node's lock.
func (n *node) arm(staged *volume) {
	if n.arming.client == nil {
		return
	}
	ctx, cancel := context.WithCancel(n.base)
	n.armingCancels[staged.id] = cancel
	go n.arming.watchClaim(ctx, staged)
}

// disarm stops the volume's goroutine and deletes the volume's gauge
// series. The caller holds the node's lock.
func (n *node) disarm(staged *volume) {
	cancel, found := n.armingCancels[staged.id]
	if !found {
		return
	}
	delete(n.armingCancels, staged.id)
	cancel()
	n.metrics.forget(staged)
}

// watchClaim finds the volume's claim, posts the Events for the faults the
// stage found, then lists and watches the claim until ctx ends. It
// returns after the watch and the reads have stopped.
//
// The list and the watch select the claim by name, so a claim that
// does not exist yet arrives later as a watch event. The handlers pass
// on every add and update, status updates too, because the external
// resizer writes the class in force to the claim's status. They ignore
// deletes, because a deleted claim has no class.
func (a *arming) watchClaim(ctx context.Context, staged *volume) {
	claim, found := a.find(ctx, staged)
	if !found {
		return
	}
	a.node.postStageFaults(staged, claim)
	claims := a.client.CoreV1().PersistentVolumeClaims(claim.namespace)
	selector := "metadata.name=" + claim.name
	// latest buffers only the newest copy of the claim. offer drops a
	// copy that armFromClaims has not received yet, so a slow read does not work
	// through stale copies. Only the informer's handler goroutine calls
	// offer, so the send after the drain never blocks.
	latest := make(chan *corev1.PersistentVolumeClaim, 1)
	offer := func(object any) {
		select {
		case <-latest:
		default:
		}
		latest <- object.(*corev1.PersistentVolumeClaim)
	}
	var reading sync.WaitGroup
	reading.Go(func() { a.armFromClaims(ctx, staged, claim, latest) })
	collection{
		kind:   persistentVolumeClaimKind,
		client: a.client,
		object: &corev1.PersistentVolumeClaim{},
		list: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector = selector
			return claims.List(ctx, options)
		},
		watch: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = selector
			return claims.Watch(ctx, options)
		},
		handler: cache.ResourceEventHandlerFuncs{
			AddFunc:    offer,
			UpdateFunc: func(_, object any) { offer(object) },
		},
		metrics: a.node.metrics,
	}.run(ctx)
	reading.Wait()
}

// armFromClaims arms the volume from each copy of the claim that arrives on
// latest, until ctx ends. When a read fails, it waits for the retry and
// reads again with the newest copy that arrived meanwhile. Creating a
// class that the claim already names does not change the claim, so no
// event arrives, and only this retry finds the new class.
func (a *arming) armFromClaims(
	ctx context.Context, staged *volume, claim claimReference, latest <-chan *corev1.PersistentVolumeClaim,
) {
	for {
		var pvc *corev1.PersistentVolumeClaim
		select {
		case <-ctx.Done():
			return
		case pvc = <-latest:
		}
		for {
			err := a.read(ctx, staged, claim, pvc)
			if err == nil {
				break
			}
			a.logger.WarnContext(ctx, "The claim and its VolumeAttributesClass were not read",
				"volume", staged.id, "claim", claim.namespace+"/"+claim.name, "error", err)
			waitOut(ctx, a.retry)
			if ctx.Err() != nil {
				return
			}
			select {
			case newer := <-latest:
				pvc = newer
			default:
			}
		}
	}
}

// find looks up the claim that the volume is bound to, and retries
// after each failure. The kubelet stages a volume only after its
// PersistentVolume is bound, so a lookup fails only when the API server
// refuses the list or the PersistentVolume is gone. It returns false
// when ctx ends first.
func (a *arming) find(ctx context.Context, staged *volume) (claimReference, bool) {
	for ctx.Err() == nil {
		claim, err := a.claimOf(ctx, staged.id)
		if err == nil {
			return claim, true
		}
		a.logger.WarnContext(ctx, "The PersistentVolumeClaim of the volume was not found",
			"volume", staged.id, "error", err)
		waitOut(ctx, a.retry)
	}
	return claimReference{}, false
}

// claimOf finds the claim through the PersistentVolume whose CSI volume
// handle matches, because the kubelet passes the handle and not the
// PersistentVolume's name.
//
// claimOf first reads the store of the node's PersistentVolume watch in
// demand.go, which sends no request. The store is empty until the
// watch's first list, and it can lag the API server. So when the store
// has no Bound PersistentVolume for the handle, claimOf lists the
// PersistentVolumes from the API server. A PersistentVolume that moves
// to another claim passes through Released or Available, so a Bound
// copy in the store names an old claim only when the watch missed
// both changes.
func (a *arming) claimOf(ctx context.Context, handle string) (claimReference, error) {
	if pv, found := a.node.demands.cachedVolume(handle); found &&
		pv.Status.Phase == corev1.VolumeBound && pv.Spec.ClaimRef != nil {
		return claimReference{
			namespace: pv.Spec.ClaimRef.Namespace,
			name:      pv.Spec.ClaimRef.Name,
		}, nil
	}
	volumes, err := a.client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return claimReference{}, err
	}
	for _, pv := range volumes.Items {
		source := pv.Spec.CSI
		if source == nil || source.Driver != driverName || source.VolumeHandle != handle {
			continue
		}
		if pv.Spec.ClaimRef == nil {
			return claimReference{}, fmt.Errorf("the PersistentVolume %s is bound to no claim", pv.Name)
		}
		return claimReference{
			namespace: pv.Spec.ClaimRef.Namespace,
			name:      pv.Spec.ClaimRef.Name,
		}, nil
	}
	return claimReference{}, fmt.Errorf("no PersistentVolume of %s has the volume handle %q", driverName, handle)
}

// read arms the volume from the class in force on the claim. It unarms
// the volume when the claim names no class of this driver, or names an
// invalid one. It returns an error only when the class cannot be read,
// and armFromClaims then retries.
func (a *arming) read(
	ctx context.Context, staged *volume, claim claimReference, pvc *corev1.PersistentVolumeClaim,
) error {
	// The list and the watch already select the claim by name. This
	// check keeps a copy of any other claim from arming the volume.
	if pvc.Name != claim.name {
		return nil
	}
	name := className(pvc)
	var rules *policy
	invalid := ""
	if name != "" {
		// The node has no watch on VolumeAttributesClass, so each read
		// of a claim gets the class from the API server.
		class, err := a.client.StorageV1().VolumeAttributesClasses().
			Get(ctx, name, metav1.GetOptions{})
		switch {
		case err != nil:
			return fmt.Errorf("reading the VolumeAttributesClass %s: %w", name, err)
		case class.DriverName != driverName:
			// A class of another driver does not apply to this volume,
			// and is not an error.
		default:
			// When the external resizer runs, ControllerModifyVolume
			// rejects an invalid class. Without a resizer nothing else
			// validates it, so read parses the parameters too.
			rules, err = parsePolicy(class.Parameters)
			if err != nil {
				invalid = fmt.Sprintf("the VolumeAttributesClass %s is not valid: %s",
					name, status.Convert(err).Message())
				a.logger.WarnContext(ctx, "The VolumeAttributesClass is not valid",
					"class", name, "error", err)
			}
		}
	}
	a.node.armed(ctx, staged, claim, name, rules, invalid)
	return nil
}

// className returns the class in force for the claim: the class in
// status.currentVolumeAttributesClassName once the external resizer has
// applied it, and otherwise the class in spec. Without a resizer the
// status is never set, so no volume would arm without the spec
// fallback.
func className(pvc *corev1.PersistentVolumeClaim) string {
	if current := pvc.Status.CurrentVolumeAttributesClassName; current != nil && *current != "" {
		return *current
	}
	if requested := pvc.Spec.VolumeAttributesClassName; requested != nil {
		return *requested
	}
	return ""
}

// armed records the claim, the class, and the parsed rules on the
// volume. It posts an Event on the pod and the claim when the volume
// changes between armed and unarmed.
func (n *node) armed(
	ctx context.Context,
	staged *volume,
	claim claimReference,
	class string,
	rules *policy,
	invalid string,
) {
	before := staged.policyNow()
	if staged.reportArmed(claim, class, rules, invalid) {
		reason, message := reasonArmed, fmt.Sprintf("Armed by the VolumeAttributesClass %s", class)
		if rules == nil {
			reason, message = reasonUnarmed, "Unarmed because the claim sets no VolumeAttributesClass of "+driverName
			if invalid != "" {
				message = "Unarmed because " + invalid
			}
		}
		n.report(staged, claim, corev1.EventTypeNormal, reason, message)
	}
	if rules != nil && (before == nil || before.quiesce != rules.quiesce) {
		n.classChanged(staged)
	}
	n.metrics.record(staged)
	n.recordHealth(ctx, staged)
}

// waitOut returns early when the context ends.
func waitOut(ctx context.Context, retry time.Duration) {
	timer := time.NewTimer(retry)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
