package main

// The worker of each reservation (plan 02): a Deployment of one pod and
// a Service named <Reservation>-astrophotography, in the reservation's
// namespace, owned by the Reservation. The pod mounts the outbox claim
// at /outbox and runs this binary in its worker mode, in the same image
// as the solver. A local-path outbox is bound to the pier machine, so
// the scheduler starts the worker on the camera's node, and the frames
// never cross the network.
//
// A worker runs while its reservation is Activating or Ready with an
// outbox in its session. The loop removes every other worker: one whose
// reservation ends, fails, or leaves this operator.

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

// roleWorker labels the objects of a worker, with its reservation's
// name.
const roleWorker = "worker"

// workerName names the Deployment and the Service of a reservation's
// worker.
func workerName(reservation string) string { return reservation + "-astrophotography" }

// maxServiceName is the length of a DNS label, the longest name a
// Service can have.
const maxServiceName = 63

// workerLoop remembers the scheduler's message it posted for each
// worker pod, so a pod that waits posts one Event, not one for each
// pass. A new lead posts each one again.
type workerLoop struct {
	mu      sync.Mutex
	pending map[string]string
}

func newWorkerLoop() *workerLoop { return &workerLoop{pending: map[string]string{}} }

// workerPass runs a worker for each reservation that needs one, and
// removes each other worker.
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
			fmt.Fprintf(o.logs, "%s: the Reservation %s/%s has a name too long for its worker's Service %s\n",
				managedBy, r.Metadata.Namespace, r.Metadata.Name, name)
			continue
		}
		wanted[r.Metadata.Namespace+"/"+name] = true
		if o.workerImage == "" {
			errs = append(errs, errNoWorkerImage)
			continue
		}
		if err := o.ensure(workerWorkload(&r, claim, o.workerImage)); err != nil {
			errs = append(errs, fmt.Errorf("the worker of Reservation %s/%s: %w", r.Metadata.Namespace, r.Metadata.Name, err))
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

// workerLabel is the label of every object of a worker.
const workerLabel = astrophotography.Group + "/" + roleWorker

// workerClaim answers the outbox claim of a reservation that needs a
// worker, and "" for one that does not. The session's record of the
// outbox is what the camera's pod mounts, so the worker mounts the same
// claim.
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

// warnPending posts the Warning AstrophotographyWorkerPending on the
// reservation when its worker's pod cannot start, with the scheduler's
// message, such as for an outbox whose access mode is
// ReadWriteOncePod, once for each pod and message. It adds each pod it
// read to seen.
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
		}, reasonWorkerPending, "the worker's pod "+p.Metadata.Name+" cannot start: "+message)
	}
}

// forget drops the message of each pod that no worker has any more.
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
			// solve-field writes its own temporary files to /tmp, and the
			// root is read-only.
			{Name: "tmp", MountPath: "/tmp"},
		},
		ReadinessProbe: &probe{HTTPGet: &httpGet{Path: worker.PathOutbox, Port: "http"}, PeriodSeconds: 10},
		// SourceExtractor peaked at 28 MB of resident memory on a frame
		// of the CCD simulator in plan 02's probe.
		Resources: &resources{Requests: map[string]string{"cpu": "50m", "memory": "32Mi"}},
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
					// The server holds no state, and a stop loses only the
					// extraction in flight, which the operator asks for
					// again.
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
