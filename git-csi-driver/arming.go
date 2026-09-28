package main

// arming.go finds the claim a writeable volume is bound to and reads
// the class that arms it. Plan 04 records the answer, and plan 05 acts
// on it.

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

// persistentVolumeClaimKind is what git_csi_watch_restarts_total names
// the watch on each writeable volume's claim.
const persistentVolumeClaimKind = "PersistentVolumeClaim"

// claimReference is the claim a PersistentVolume is bound to. It labels
// the volume's metrics and takes its Events.
type claimReference struct {
	namespace string
	name      string
}

// arming reads the cluster for one node's volumes. A driver outside a
// cluster holds no client and arms nothing.
type arming struct {
	node   *node
	client kubernetes.Interface
	logger *slog.Logger
	retry  time.Duration
}

func newArming(answering *node, client kubernetes.Interface, logger *slog.Logger) *arming {
	return &arming{node: answering, client: client, logger: logger, retry: defaultRetry}
}

// arm starts the loop that reads the volume's claim. The caller holds
// the node's lock.
func (n *node) arm(staged *volume) {
	if n.arms.client == nil {
		return
	}
	ctx, cancel := context.WithCancel(n.base)
	n.armings[staged.id] = cancel
	go n.arms.follow(ctx, staged)
}

// disarm ends that loop and takes the volume off the gauges. The caller
// holds the node's lock.
func (n *node) disarm(staged *volume) {
	cancel, found := n.armings[staged.id]
	if !found {
		return
	}
	delete(n.armings, staged.id)
	cancel()
	n.readings.forget(staged)
}

// follow finds the claim, then lists it and watches it until the
// driver stops. The list and the watch select the claim by name, so a
// claim that does not exist yet costs one list, and its creation
// arrives as an event. It returns only after the watch and the reads
// have ended.
//
// Every add and every update is read, status writes included, because
// the resizer records the class in force on the claim's status. A
// deleted claim arms nothing new, so a delete is not read.
func (a *arming) follow(ctx context.Context, staged *volume) {
	claim, found := a.find(ctx, staged)
	if !found {
		return
	}
	claims := a.client.CoreV1().PersistentVolumeClaims(claim.namespace)
	selector := "metadata.name=" + claim.name
	// The slot holds the newest copy of the claim that the reads have
	// not taken. Only the informer's one handler goroutine sends, so a
	// send after the drain always finds the slot empty.
	latest := make(chan *corev1.PersistentVolumeClaim, 1)
	offer := func(object any) {
		select {
		case <-latest:
		default:
		}
		latest <- object.(*corev1.PersistentVolumeClaim)
	}
	var reading sync.WaitGroup
	reading.Go(func() { a.reads(ctx, staged, claim, latest) })
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
		readings: a.node.readings,
	}.follow(ctx)
	reading.Wait()
}

// reads arms the volume from each copy of the claim the watch offers,
// until the driver stops. A read that fails is made again after the
// retry, with the newest copy the watch offered by then. A class that
// arrives after the claim names it sends no event on the claim, so the
// read after the retry is what finds it.
func (a *arming) reads(
	ctx context.Context, staged *volume, claim claimReference, latest <-chan *corev1.PersistentVolumeClaim,
) {
	for {
		var held *corev1.PersistentVolumeClaim
		select {
		case <-ctx.Done():
			return
		case held = <-latest:
		}
		for {
			err := a.read(ctx, staged, claim, held)
			if err == nil {
				break
			}
			a.logger.WarnContext(ctx, "the claim was not read",
				"volume", staged.id, "claim", claim.namespace+"/"+claim.name, "error", err)
			waitOut(ctx, a.retry)
			if ctx.Err() != nil {
				return
			}
			select {
			case newer := <-latest:
				held = newer
			default:
			}
		}
	}
}

// find reads the claim the volume is bound to, and reads it again
// after the retry while the read fails. The kubelet stages a volume
// only after its PersistentVolume is bound, so the read fails only
// when the API server refuses the list or the PersistentVolume is
// gone. It reports false when the driver stops first.
func (a *arming) find(ctx context.Context, staged *volume) (claimReference, bool) {
	for ctx.Err() == nil {
		claim, err := a.claimOf(ctx, staged.id)
		if err == nil {
			return claim, true
		}
		a.logger.WarnContext(ctx, "the claim was not found",
			"volume", staged.id, "error", err)
		waitOut(ctx, a.retry)
	}
	return claimReference{}, false
}

// claimOf finds the claim through the PersistentVolume that carries the
// handle. The kubelet passes the handle and never the object's name, so
// the driver matches on the handle.
//
// The node's watch on PersistentVolumes already holds every one in the
// cluster, so the claim comes from its store, and a lookup sends the
// API server no request. The store can lag the API server by the
// moment the watch takes to deliver a change, and before the watch's
// first read it holds nothing. So when the store holds no
// PersistentVolume of the handle, or holds one whose phase is not
// Bound, the driver lists the PersistentVolumes from the API server,
// which answers what is bound now. A PersistentVolume that moves to
// another claim passes through Released or Available first, so a copy
// in the store names an old claim only when the watch missed each of
// those changes.
func (a *arming) claimOf(ctx context.Context, handle string) (claimReference, error) {
	if held, found := a.node.demands.heldVolume(handle); found &&
		held.Status.Phase == corev1.VolumeBound && held.Spec.ClaimRef != nil {
		return claimReference{
			namespace: held.Spec.ClaimRef.Namespace,
			name:      held.Spec.ClaimRef.Name,
		}, nil
	}
	volumes, err := a.client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return claimReference{}, err
	}
	for _, held := range volumes.Items {
		source := held.Spec.CSI
		if source == nil || source.Driver != driverName || source.VolumeHandle != handle {
			continue
		}
		if held.Spec.ClaimRef == nil {
			return claimReference{}, fmt.Errorf("the PersistentVolume %s is bound to no claim", held.Name)
		}
		return claimReference{
			namespace: held.Spec.ClaimRef.Namespace,
			name:      held.Spec.ClaimRef.Name,
		}, nil
	}
	return claimReference{}, fmt.Errorf("no PersistentVolume of %s carries the handle %s", driverName, handle)
}

// read takes the class the claim names and arms the volume when that
// class belongs to this driver. A class the node could not read is an
// error, so reads reads the class again after the retry, with the
// newest copy of the claim the watch offered by then. A class that arrives after the claim names it sends no event
// on the claim, and that read is what finds it.
func (a *arming) read(
	ctx context.Context, staged *volume, claim claimReference, held *corev1.PersistentVolumeClaim,
) error {
	// The list and the watch select the claim by name, and this check
	// keeps any other claim from arming the volume all the same.
	if held.Name != claim.name {
		return nil
	}
	name := className(held)
	var rules *policy
	invalid := ""
	if name != "" {
		// The node watches no VolumeAttributesClass, so no store holds
		// the class, and the node reads it from the API server: one
		// request for each copy of the claim that reads takes, and one
		// for each retry while the class is missing.
		class, err := a.client.StorageV1().VolumeAttributesClasses().
			Get(ctx, name, metav1.GetOptions{})
		switch {
		case err != nil:
			return fmt.Errorf("the class %s was not read: %w", name, err)
		case class.DriverName != driverName:
			// A class of another driver says nothing about this
			// volume, so it arms nothing and is not a failure.
		default:
			// A class the resizer let through is still read here,
			// because the resizer may not be running, and a class the
			// driver cannot read arms nothing.
			rules, err = parsePolicy(class.Parameters)
			if err != nil {
				invalid = fmt.Sprintf("the class %s is not valid: %s",
					name, status.Convert(err).Message())
				a.logger.WarnContext(ctx, "the class is not valid",
					"class", name, "error", err)
			}
		}
	}
	a.node.armed(ctx, staged, claim, name, rules, invalid)
	return nil
}

// className is the class in force: the one the claim's status carries,
// or the one its spec names until a resizer records the modify. Without
// a resizer the status is never filled, so the spec has to count or
// nothing ever arms.
func className(held *corev1.PersistentVolumeClaim) string {
	if current := held.Status.CurrentVolumeAttributesClassName; current != nil && *current != "" {
		return *current
	}
	if asked := held.Spec.VolumeAttributesClassName; asked != nil {
		return *asked
	}
	return ""
}

// armed records the answer and posts an Event on the pod and the claim
// when the volume moved between armed and unarmed.
func (n *node) armed(
	ctx context.Context,
	staged *volume,
	claim claimReference,
	class string,
	rules *policy,
	invalid string,
) {
	if staged.reportArmed(claim, class, rules, invalid) {
		reason, message := reasonArmed, fmt.Sprintf("armed by the class %s", class)
		if rules == nil {
			reason, message = reasonUnarmed, "unarmed: the claim names no class of "+driverName
			if invalid != "" {
				message = "unarmed: " + invalid
			}
		}
		n.report(ctx, staged, claim, corev1.EventTypeNormal, reason, message)
	}
	n.readings.record(staged)
	n.noteHealth(ctx, staged)
}
