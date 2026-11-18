package main

// Each Reservation gets a worker: a Deployment with one pod and a
// Service, both named <Reservation>-astrophotography, in the
// Reservation's namespace and owned by it. The pod mounts the outbox
// claim at /outbox and runs this binary in its worker mode, from the
// same image as the solver. A local-path outbox is bound to the node
// where the camera is attached, so the scheduler starts the worker on
// that node, and the frames never cross the network.
//
// A worker exists while its Reservation is Activating or Ready and its
// session records an outbox. workerPass deletes every other worker. That
// includes the worker of a Reservation that another operator now holds,
// because the Reservation watch reads only the Reservations this
// operator holds.

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/outbox"
	"github.com/liken-sh/liken/astrophotography-operator/worker"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const roleWorker = "worker"

func workerName(reservation string) string { return reservation + "-astrophotography" }

// maxServiceName is the longest Service name, because a Service name
// is a DNS label.
const maxServiceName = 63

// workerLoop caches, by pod UID, the last scheduling message it posted
// an Event for, so a pod that stays Pending gets one Event for each
// message instead of one for each pass. A new leader starts with an
// empty cache and posts each message again.
type workerLoop struct {
	mu      sync.Mutex
	pending map[string]string
}

func newWorkerLoop() *workerLoop { return &workerLoop{pending: map[string]string{}} }

// workerPass creates or updates the worker of each Reservation that
// needs one, and deletes every other worker Deployment.
func (o *operator) workerPass(_ context.Context, loop *workerLoop) error {
	wanted, seen := map[string]bool{}, map[string]bool{}
	var errs []error
	for _, r := range informer.CachedList[observatory.Reservation](o.stores.collections[observatory.ReservationKind.Name].View()) {
		claim := workerClaim(&r)
		if claim == "" {
			continue
		}
		name := workerName(r.Metadata.Name)
		if len(name) > maxServiceName {
			fmt.Fprintf(o.logs, "%s: no worker is created for Reservation %s/%s, because the name %s of its worker's Service is %d characters and a Service name allows %d\n",
				managedBy, r.Metadata.Namespace, r.Metadata.Name, name, len(name), maxServiceName)
			continue
		}
		wanted[r.Metadata.Namespace+"/"+name] = true
		if o.workerImage == "" {
			errs = append(errs, errNoWorkerImage)
			continue
		}
		if err := o.ensure(workerWorkload(&r, claim, o.workerImage)); err != nil {
			errs = append(errs, fmt.Errorf("creating or updating the worker of Reservation %s/%s: %w", r.Metadata.Namespace, r.Metadata.Name, err))
		}
		loop.warnPending(o, &r, seen)
	}
	loop.forget(seen)
	for _, d := range informer.CachedList[deployment](o.stores.collections["Deployment"].View()) {
		key := d.Metadata.Namespace + "/" + d.Metadata.Name
		if _, isWorker := d.Metadata.Labels[workerLabel]; isWorker && !wanted[key] {
			if err := o.remove(d.Metadata.Namespace, d.Metadata.Name); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

const workerLabel = astrophotography.Group + "/" + roleWorker

// workerClaim returns the outbox claim that the camera's pod mounts, or
// "" when the Reservation needs no worker.
func workerClaim(r *observatory.Reservation) string {
	session := r.Status.Session
	switch {
	case r.Metadata.DeletionTimestamp != nil, session == nil, session.Outbox == nil:
		return ""
	case r.Status.Phase == observatory.ReservationActivating, r.Status.Phase == observatory.ReservationReady:
		return session.Outbox.ClaimName
	}
	return ""
}

// warnPending posts a Warning on the Reservation with the scheduler's
// message when the worker's pod cannot be scheduled, once for each pod
// and message. A common cause is a ReadWriteOncePod outbox claim that
// another pod already mounts. It adds each pod it reads to seen.
func (loop *workerLoop) warnPending(o *operator, r *observatory.Reservation, seen map[string]bool) {
	for _, p := range o.podsOf(r.Metadata.Namespace, workerLabel, r.Metadata.Name) {
		seen[p.Metadata.UID] = true
		message := p.waiting()
		loop.mu.Lock()
		posted := loop.pending[p.Metadata.UID]
		loop.pending[p.Metadata.UID] = message
		loop.mu.Unlock()
		if message == "" || message == posted {
			continue
		}
		o.recorder.Warning(events.ObjectReference{
			APIVersion: observatory.APIVersion, Kind: observatory.ReservationKind.Name,
			Namespace: r.Metadata.Namespace, Name: r.Metadata.Name, UID: r.Metadata.UID,
		}, reasonWorkerPending, "The worker pod "+p.Metadata.Name+" cannot start: "+message)
	}
}

// forget drops the cached message of each pod that is not in seen.
func (loop *workerLoop) forget(seen map[string]bool) {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	for uid := range loop.pending {
		if !seen[uid] {
			delete(loop.pending, uid)
		}
	}
}

func workerWorkload(r *observatory.Reservation, claim, image string) workload {
	name, namespace := workerName(r.Metadata.Name), r.Metadata.Namespace
	labels := workloadLabels(roleWorker, r.Metadata.Name)
	owners := owner(observatory.APIVersion, observatory.ReservationKind.Name, r.Metadata.Name, r.Metadata.UID)
	extract := container{
		Name:            "worker",
		Image:           image,
		Args:            []string{"worker"},
		Ports:           []containerPort{{Name: "http", ContainerPort: worker.Port}},
		SecurityContext: restricted(),
		VolumeMounts: []volumeMount{
			{Name: "outbox", MountPath: outbox.Root},
			{Name: "work", MountPath: workDir},
			// solve-field writes temporary files to /tmp, and the root
			// filesystem is read-only.
			{Name: "tmp", MountPath: "/tmp"},
		},
		ReadinessProbe: &probe{HTTPGet: &httpGet{Path: worker.PathOutbox, Port: "http"}, PeriodSeconds: 10},
		Resources:      &resources{Requests: map[string]string{"cpu": "50m", "memory": "32Mi"}},
	}
	d := &deployment{
		APIVersion: "apps/v1", Kind: "Deployment",
		Metadata: metaOf(name, namespace, labels, owners),
		Spec: deploymentSpec{
			Replicas: int32Pointer(1),
			Selector: labelSelector{MatchLabels: labels},
			Strategy: strategy{Type: "Recreate"},
			Template: podTemplate{
				Metadata: metaOf("", "", labels, nil),
				Spec: podSpec{
					// The worker keeps no state. Stopping it loses only the
					// extraction in flight, and the operator requests that
					// extraction again.
					TerminationGracePeriodSeconds: int64Pointer(5),
					EnableServiceLinks:            boolPointer(false),
					AutomountServiceAccountToken:  boolPointer(false),
					Containers:                    []container{extract},
					Volumes: []volume{
						{Name: "outbox", PersistentVolumeClaim: &claimName{ClaimName: claim}},
						{Name: "work", EmptyDir: &emptyDir{}},
						{Name: "tmp", EmptyDir: &emptyDir{}},
					},
					SecurityContext: workloadSecurity(),
				},
			},
		},
	}
	svc := &service{
		APIVersion: "v1", Kind: "Service",
		Metadata: metaOf(name, namespace, labels, owners),
		Spec:     serviceSpec{Selector: labels, Ports: []servicePort{{Name: "http", Port: worker.Port, TargetPort: "http"}}},
	}
	return workload{deployment: d, service: svc}
}
