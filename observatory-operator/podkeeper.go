package main

// The operator's pods have restartPolicy Always, so a pod reaches the
// final phase Failed or Succeeded only when the kubelet ended it: for
// an eviction, a rejection at admission, or the node's shutdown, where
// containers that exit zero leave the pod Succeeded. No kubelet starts
// such a pod again. So the operator deletes it and creates it again,
// in ensure and in each wait of an activation step for a pod.
//
// A device's claim, or the guide camera's affinity to its server's
// node, keeps the new pod on the same node. So the operator replaces an
// ended pod after the wait of kubernetes/endedpods, with the count in
// the pod's annotation annotationTries. The ended pod stays during the
// wait, so a person can read its status.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/endedpods"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// annotationTries counts the earlier pods of the same name that the
// kubelet ended. A pod with no count reads as zero. The spec digest
// leaves out the metadata (stampedWith), so the count does not make a
// pod differ from the one that the operator would build.
const annotationTries = Group + "/tries"

// ended reports whether the kubelet ended the pod (see above).
func (p *pod) ended() bool {
	return endedpods.Ended(p.Status.Phase)
}

func (p *pod) tries() int {
	return endedpods.Tries(p.Metadata.Annotations, annotationTries)
}

// end returns what the rule of kubernetes/endedpods reads of a pod.
func (p *pod) end() endedpods.Pod {
	end := endedpods.Pod{
		Phase:   p.Status.Phase,
		Reason:  p.Status.Reason,
		Created: p.created(),
		Tries:   p.tries(),
	}
	for _, c := range p.Status.Conditions {
		end.Conditions = append(end.Conditions, endedpods.Condition{Type: c.Type, Status: c.Status, LastTransitionTime: timeOf(c.LastTransitionTime)})
	}
	for _, c := range p.Status.ContainerStatuses {
		if c.State.Terminated != nil {
			end.Finished = append(end.Finished, timeOf(c.State.Terminated.FinishedAt))
		}
	}
	return end
}

// timeOf returns the time that a decoded timestamp holds, and the zero
// time for a timestamp that the API server did not write.
func timeOf(stamp *time.Time) time.Time {
	if stamp == nil {
		return time.Time{}
	}
	return *stamp
}

func (p *pod) created() time.Time {
	return timeOf(p.Metadata.CreationTimestamp)
}

// replacements holds what a caller of keepPod carries from the pass
// that deletes an ended pod to the pass that creates its replacement,
// because the replacement has the ended pod's name and the deleted pod
// is gone by then. The caller keeps it for as long as its wait or its
// loop runs. An operator restart between the delete and the create
// loses the count, so the replacement has none and its own wait starts
// again at endedpods.FirstWait.
type replacements struct {
	pods map[string]replacement
	// wake is notified when a pod's wait ends, so the caller's wait or
	// loop runs its next pass then.
	wake *changeNotifier
	// later makes ensure return errReplaceLater while a pod waits, in
	// place of waiting for the pod's deadline. The runner of a Ready
	// reservation sets it, so its loop holds no lock during the wait,
	// and the end of the reservation does not wait for the pod.
	later bool
}

func newReplacements(wake *changeNotifier, later bool) *replacements {
	return &replacements{pods: map[string]replacement{}, wake: wake, later: later}
}

// errReplaceLater is the error of ensure for a pod whose replacement
// waits for its deadline, when the caller set replacements.later.
var errReplaceLater = errors.New("the replacement waits")

type replacement struct {
	// deleted is true after the caller deleted the ended pod.
	deleted bool
	// tries is the count of the replacement.
	tries int
	// announce is true when the replacement gets a PodCreated Event
	// (endedpods.Pod.FirstOfLoop).
	announce bool
	// armed is the time of the timer that wakes the caller.
	armed time.Time
}

// keepResult is the result of one pass of keepPod. done reports that
// the pod exists, is not being deleted, and has not ended, or that
// keepPod created it. created reports whether the API server created
// the pod and the creation gets a PodCreated Event. due is set while an
// ended pod waits for its replacement.
type keepResult struct {
	done    bool
	waiting string
	created bool
	due     time.Time
}

// keepPod runs one pass of keeping a pod. It creates the pod when no
// pod has its name. It deletes a pod of that name that the kubelet
// ended once its wait is over, so a later pass creates the
// replacement. An ended pod whose spec differs from built is replaced
// at once, and its replacement has no count, because a person's change
// can fix what the kubelet refused.
func (o *operator) keepPod(ctx context.Context, report func(string), t *tree, built *pod, carried *replacements) (keepResult, error) {
	name := built.Metadata.Name
	running, ok := t.pods[name]
	switch {
	case !ok:
		next := carried.pods[name]
		if next.tries > 0 {
			if built.Metadata.Annotations == nil {
				built.Metadata.Annotations = map[string]string{}
			}
			endedpods.SetTries(built.Metadata.Annotations, annotationTries, next.tries)
		}
		var made bool
		err := o.send(ctx, report, fmt.Sprintf("creating pod %s", name), func() (err error) {
			made, err = o.post("/api/v1/namespaces/"+o.namespace+"/pods", built)
			return err
		})
		if err != nil {
			return keepResult{}, err
		}
		delete(carried.pods, name)
		return keepResult{done: true, created: made && (!next.deleted || next.announce)}, nil
	case running.Metadata.DeletionTimestamp != nil:
		return keepResult{waiting: "waiting for the earlier pod " + name + " to stop"}, nil
	case running.ended():
		why := firstNonEmpty(running.Status.Reason, running.Status.Phase)
		now := time.Now()
		end := running.end()
		next := replacement{deleted: true, tries: end.Tries + 1, announce: end.FirstOfLoop()}
		if !current(running, built) {
			next = replacement{deleted: true, announce: true}
		} else if due := end.ReplaceAt(); now.Before(due) {
			// The wait is a deadline: the timer wakes the caller at due.
			if armed := carried.pods[name]; !armed.armed.Equal(due) {
				armed.armed = due
				carried.pods[name] = armed
				time.AfterFunc(due.Sub(now), carried.wake.notify)
			}
			return keepResult{
				waiting: fmt.Sprintf("waiting until %s to replace the pod %s, which the kubelet ended (%s)", due.UTC().Format(time.RFC3339), name, why),
				due:     due,
			}, nil
		}
		if err := o.send(ctx, report, fmt.Sprintf("replacing pod %s: the kubelet ended it (%s)", name, why), func() error { return o.deleteObject(podPath(o.namespace, name)) }); err != nil {
			return keepResult{}, err
		}
		carried.pods[name] = next
		return keepResult{waiting: "replacing the pod " + name + ", which the kubelet ended (" + why + ")"}, nil
	}
	return keepResult{done: true}, nil
}

// podKeeper creates a pod again when an activation step waits for the
// pod and the pod is gone or the kubelet ended it, so the step goes on
// waiting for the new pod and does not fail at its deadline. A nil
// podKeeper keeps nothing, for a wait that must not create a pod: a
// step that stops a server, and an action of a procedure, which runs on
// the pods that the steps started.
type podKeeper struct {
	o *operator
	// reservation is the name of the reservation whose step waits, for
	// the Event.
	reservation string
	carried     *replacements
}

// keeper returns the podKeeper of the runner's steps.
func (r *runner) keeper() *podKeeper {
	return &podKeeper{o: r.o, reservation: r.name, carried: newReplacements(r.o.changed, false)}
}

// keep runs keepPod for the pod name when it is gone or ended, with the
// pod that build returns, and posts a PodCreated Event about the object
// when keepPod reports one. acted reports that the wait must go on.
// build runs only then, so a wait that wakes on each INDI event builds
// no pod while the pod runs.
func (k *podKeeper) keep(ctx context.Context, report func(string), t *tree, about events.ObjectReference, name string, build func() (*pod, error)) (acted bool, waiting string, err error) {
	if k == nil {
		return false, "", nil
	}
	if p, ok := t.pods[name]; ok && !p.ended() {
		return false, "", nil
	}
	built, err := build()
	if err != nil {
		return true, "", err
	}
	result, err := k.o.keepPod(ctx, report, t, built, k.carried)
	waiting = result.waiting
	if result.created {
		k.o.recorder.Normal(about, reasonPodCreated, fmt.Sprintf("Created pod %s again during the activation of Reservation %s", name, k.reservation))
		k.o.logf("Reservation %s: created pod %s again for %s %s", k.reservation, name, about.Kind, about.Name)
	}
	if _, ok := t.pods[name]; !ok && err == nil && waiting == "" {
		waiting = "waiting for pod " + name + ", which the operator created again"
	}
	return true, waiting, err
}

// keepDevice keeps the pod of a device that runs a driver on ref.
func (k *podKeeper) keepDevice(ctx context.Context, report func(string), t *tree, ref serverRef, d *device, name string) (bool, string, error) {
	// A device that a person deleted, or that left the server, during
	// the wait gets no pod back. Its pod waits for the release
	// (deletes.go).
	if !slices.ContainsFunc(t.devicesOn(ref), func(in *device) bool {
		return in.key() == d.key() && in.object.Metadata.UID == d.object.Metadata.UID && in.object.Metadata.DeletionTimestamp == nil
	}) {
		return false, "", nil
	}
	return k.keep(ctx, report, t, reference(d.kind, d.object.Metadata), name, func() (*pod, error) {
		built, _, _, err := devicePod(t.namespace, ref, d, t.guides(ref, d), t.outbox(ref, d))
		return built, err
	})
}

// keepServer keeps the pod of an INDI server, with the devices that the
// tree places on it, as ensureServer builds it.
func (k *podKeeper) keepServer(ctx context.Context, report func(string), t *tree, ref serverRef) (bool, string, error) {
	var owner observatory.ObjectMeta
	switch ref.kind.Name {
	case observatory.TelescopeKind.Name:
		telescope, ok := t.telescopes[ref.name]
		if !ok {
			return false, "", nil
		}
		owner = telescope.Metadata
	default:
		site, ok := t.observatories[ref.name]
		if !ok {
			return false, "", nil
		}
		owner = site.Metadata
	}
	return k.keep(ctx, report, t, reference(ref.kind, owner), ref.String(), func() (*pod, error) {
		built, _, err := serverPod(t.namespace, ref, owner.UID, t.devicesOn(ref))
		return built, err
	})
}

// keepGuider keeps the pod of a guider, as startGuiderPod builds it.
func (k *podKeeper) keepGuider(ctx context.Context, report func(string), t *tree, ref serverRef, guider *observatory.Guider, gear guiderEquipment) (bool, string, error) {
	return k.keep(ctx, report, t, reference(observatory.GuiderKind, guider.Metadata), guiderName(guider), func() (*pod, error) {
		_, built, _, err := guiderPod(t.namespace, ref, guider, gear)
		return built, err
	})
}
