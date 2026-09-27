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
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
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
// arrives as an event.
func (a *arming) follow(ctx context.Context, staged *volume) {
	claim, found := a.find(ctx, staged)
	if !found {
		return
	}
	claims := a.client.CoreV1().PersistentVolumeClaims(claim.namespace)
	selector := "metadata.name=" + claim.name
	(&listWatch{
		kind: persistentVolumeClaimKind,
		list: func(ctx context.Context) (string, error) {
			held, err := claims.List(ctx, metav1.ListOptions{FieldSelector: selector})
			if err != nil {
				return "", err
			}
			for i := range held.Items {
				if err := a.read(ctx, staged, claim, &held.Items[i]); err != nil {
					return "", err
				}
			}
			return held.ResourceVersion, nil
		},
		watch: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = selector
			return claims.Watch(ctx, options)
		},
		act: func(ctx context.Context, event watch.Event) error {
			// A deleted claim arms nothing new. A bookmark is a claim
			// with no name, and read passes over it.
			held, isClaim := event.Object.(*corev1.PersistentVolumeClaim)
			if !isClaim || event.Type == watch.Deleted {
				return nil
			}
			return a.read(ctx, staged, claim, held)
		},
		retry:    a.retry,
		logger:   a.logger,
		readings: a.node.readings,
	}).follow(ctx)
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
// class belongs to this driver. A class the node could not read is an
// error, so the loop reads the claim and the class again after the
// retry. A class that arrives after the claim names it sends no event
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
