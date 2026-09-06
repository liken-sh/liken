package main

// arming.go finds the claim a writeable volume is bound to and reads
// the class that arms it. Plan 04 records the answer, and plan 05 acts
// on it.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// defaultResync is how long the driver waits before it reads the claim
// again. It covers a watch that ended and a claim that arrived after
// the volume did.
const defaultResync = 30 * time.Second

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
	resync time.Duration
}

func newArming(answering *node, client kubernetes.Interface, logger *slog.Logger) *arming {
	return &arming{node: answering, client: client, logger: logger, resync: defaultResync}
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

// follow reads the claim, then waits for the claim to change or for the
// resync, until the driver stops.
func (a *arming) follow(ctx context.Context, staged *volume) {
	for ctx.Err() == nil {
		a.pass(ctx, staged)
	}
}

// pass finds the claim, reads the class, and holds a watch open until
// the claim changes or the resync says to start again.
func (a *arming) pass(ctx context.Context, staged *volume) {
	claim, err := a.claimOf(ctx, staged.id)
	if err != nil {
		a.logger.WarnContext(ctx, "the claim was not found",
			"volume", staged.id, "error", err)
		a.rest(ctx)
		return
	}
	a.read(ctx, staged, claim)

	watching, err := a.client.CoreV1().PersistentVolumeClaims(claim.namespace).
		Watch(ctx, metav1.ListOptions{FieldSelector: "metadata.name=" + claim.name})
	if err != nil {
		a.logger.WarnContext(ctx, "the claim is not watched",
			"claim", claim.namespace+"/"+claim.name, "error", err)
		a.rest(ctx)
		return
	}
	defer watching.Stop()

	resync := time.NewTimer(a.resync)
	defer resync.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-resync.C:
			return
		case _, open := <-watching.ResultChan():
			if !open {
				return
			}
			a.read(ctx, staged, claim)
		}
	}
}

// rest waits out the resync after a read that failed, so a claim that
// is not there yet costs one call per resync.
func (a *arming) rest(ctx context.Context) {
	timer := time.NewTimer(a.resync)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// claimOf finds the claim through the PersistentVolume that carries the
// handle. The kubelet passes the handle and never the object's name, so
// the driver lists the volumes and matches on the handle.
func (a *arming) claimOf(ctx context.Context, handle string) (claimReference, error) {
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
// class belongs to this driver.
func (a *arming) read(ctx context.Context, staged *volume, claim claimReference) {
	held, err := a.client.CoreV1().PersistentVolumeClaims(claim.namespace).
		Get(ctx, claim.name, metav1.GetOptions{})
	if err != nil {
		a.logger.WarnContext(ctx, "the claim was not read",
			"claim", claim.namespace+"/"+claim.name, "error", err)
		return
	}

	name := className(held)
	var rules *policy
	invalid := ""
	if name != "" {
		class, err := a.client.StorageV1().VolumeAttributesClasses().
			Get(ctx, name, metav1.GetOptions{})
		switch {
		case err != nil:
			a.logger.WarnContext(ctx, "the class was not read",
				"class", name, "error", err)
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
