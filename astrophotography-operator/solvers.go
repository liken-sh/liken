package main

// The Solver loop. For each Solver, the operator creates its
// Deployment and Service, and writes the solver's endpoint, its index
// series, and the Ready condition to the status. solverindexes.go reads
// each ready pod's list of index files.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/solver"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const solversPath = "/apis/" + astrophotography.Group + "/" + astrophotography.Version + "/solvers"

func solverPath(namespace, name string) string {
	return "/apis/" + astrophotography.Group + "/" + astrophotography.Version + "/namespaces/" + namespace + "/solvers/" + name
}

// solverLoop holds the memo of status writes and the index list of each
// solver pod, keyed by the pod's UID. Both are caches, and a new leader
// fills them again from the API server and the pods.
type solverLoop struct {
	versions *memo.Versions
	mu       sync.Mutex
	follows  map[string]*indexFollow
	// running counts the goroutines that read a pod's list, so the
	// operator can wait for them to stop before it releases the Lease.
	running sync.WaitGroup
}

func newSolverLoop() *solverLoop {
	return &solverLoop{versions: memo.New(), follows: map[string]*indexFollow{}}
}

// solverPass reconciles every Solver once, and stops reading the list
// of each pod that no Solver reports.
func (o *operator) solverPass(ctx context.Context, loop *solverLoop) error {
	store := informer.Held{View: o.stores.collections[astrophotography.SolverKind.Name].View(), Versions: loop.versions}
	solvers, err := informer.List[astrophotography.Solver](o.client, store, solversPath, memo.NamespacedPath(solverPath))
	if err != nil {
		return err
	}
	var errs []error
	pods := map[string]bool{}
	for i := range solvers {
		s := &solvers[i]
		if s.Metadata.DeletionTimestamp != nil {
			continue
		}
		uid, err := o.reconcileSolver(ctx, loop, s)
		pods[uid] = true
		if err != nil {
			errs = append(errs, fmt.Errorf("reconciling Solver %s/%s: %w", s.Metadata.Namespace, s.Metadata.Name, err))
		}
	}
	loop.forget(pods)
	return errors.Join(errs...)
}

// reconcileSolver creates the Solver's workload and writes its status.
// It returns the UID of the pod that the status reports, so that
// solverPass keeps reading that pod's index list.
func (o *operator) reconcileSolver(ctx context.Context, loop *solverLoop, s *astrophotography.Solver) (string, error) {
	var failed error
	if o.workerImage == "" {
		failed = errNoWorkerImage
	} else {
		failed = o.ensure(solverWorkload(s, o.workerImage))
	}
	p := reportedPod(o.podsOf(s.Metadata.Namespace, astrophotography.Group+"/"+roleSolver, s.Metadata.Name))
	report := solverReport{claim: s.Spec.IndexFiles.ClaimName, pod: p, endpoint: solverEndpoint(s.Metadata.Namespace, solverName(s.Metadata.Name))}
	switch {
	case failed != nil:
		report.failure = failed
	case p != nil && p.ready():
		report.indexes, report.failure = loop.indexes(ctx, o, p)
	}
	uid := ""
	if p != nil {
		uid = p.Metadata.UID
	}
	return uid, errors.Join(failed, o.writeSolverStatus(s, loop, report))
}

// errNoWorkerImage is the Ready message when workerImage in images.go
// failed at start. The operator's log has that error.
var errNoWorkerImage = errors.New("the operator has no image for the solver's pod, and its log has the reason")

type solverReport struct {
	claim    string
	pod      *podObject
	endpoint observatory.Endpoint
	indexes  solver.Indexes
	failure  error
}

// ready returns the Ready condition, and the status for which
// events.Recorder.Transition posts a Warning. A Ready that is False only
// because the pod is starting returns "", so its Event is Normal.
func (r solverReport) ready() (conditions.Condition, conditions.Status) {
	waits := ""
	if r.pod != nil {
		waits = r.pod.waiting()
	}
	switch {
	case r.failure != nil:
		return notReady(astrophotography.ReasonPodNotReady, r.failure.Error()), conditions.False
	case claimMissing(waits, r.claim):
		return notReady(astrophotography.ReasonClaimMissing, "The PersistentVolumeClaim "+r.claim+" does not exist: "+waits), conditions.False
	case r.pod == nil:
		return notReady(astrophotography.ReasonPodNotReady, "The solver's Deployment has no pod yet"), ""
	case waits != "":
		return notReady(astrophotography.ReasonPodNotReady, "The solver's pod cannot start: "+waits), conditions.False
	case !r.pod.ready():
		return notReady(astrophotography.ReasonPodNotReady, "The solver's pod "+r.pod.Metadata.Name+" is not ready"), ""
	case r.indexes == nil:
		return notReady(astrophotography.ReasonPodNotReady, "The operator is reading the index list of the solver's pod "+r.pod.Metadata.Name), ""
	case len(r.indexes) == 0:
		return notReady(astrophotography.ReasonNoIndexFiles, "The claim "+r.claim+" has no index-*.fits file in the directory that spec.indexFiles.path sets"), conditions.False
	}
	files, series := int32(0), make([]string, 0, len(r.indexes))
	for _, s := range r.indexes {
		files += s.Files
		series = append(series, fmt.Sprint(s.ID))
	}
	return conditions.Condition{Type: astrophotography.ConditionReady, Status: conditions.True, Reason: astrophotography.ReasonLoaded,
		Message: fmt.Sprintf("%d index files of the series %s", files, strings.Join(series, ", "))}, conditions.False
}

func notReady(reason, message string) conditions.Condition {
	return conditions.Condition{Type: astrophotography.ConditionReady, Status: conditions.False, Reason: reason, Message: message}
}

// claimMissing reports whether the message of the pod's PodScheduled
// condition says that the claim does not exist. The scheduler writes
// persistentvolumeclaim "<name>" not found there.
func claimMissing(message, claim string) bool {
	return strings.Contains(message, `persistentvolumeclaim "`+claim+`" not found`)
}

// writeSolverStatus writes the status when it differs from the stored
// one, and posts an Event when the write changed Ready.
func (o *operator) writeSolverStatus(s *astrophotography.Solver, loop *solverLoop, r solverReport) error {
	next, bad := r.ready()
	transitioned := false
	apply := func(copied *astrophotography.Solver) bool {
		before := copied.Status
		before.Conditions = slices.Clone(before.Conditions)
		status := &copied.Status
		status.ObservedGeneration = copied.Metadata.Generation
		status.Endpoint = nil
		if r.pod != nil && r.pod.ready() {
			endpoint := r.endpoint
			status.Endpoint = &endpoint
		}
		if r.indexes != nil {
			// The API server drops an empty list, so write nil, or the
			// comparison with the stored status always differs.
			status.Indexes = nil
			if len(r.indexes) > 0 {
				status.Indexes = r.indexes
			}
			series := make([]string, 0, len(r.indexes))
			for _, s := range r.indexes {
				series = append(series, fmt.Sprint(s.ID))
			}
			status.Series = strings.Join(series, ",")
		}
		next.ObservedGeneration = copied.Metadata.Generation
		transitioned = conditions.Set(&status.Conditions, next)
		return !reflect.DeepEqual(before, copied.Status)
	}
	wrote, err := informer.SettleStatus(o.client, loop.versions, solverPath(s.Metadata.Namespace, s.Metadata.Name), s, apply)
	if wrote && transitioned {
		o.recorder.Transition(events.ObjectReference{
			APIVersion: astrophotography.APIVersion, Kind: astrophotography.SolverKind.Name,
			Namespace: s.Metadata.Namespace, Name: s.Metadata.Name, UID: s.Metadata.UID,
		}, next, bad)
	}
	return err
}
