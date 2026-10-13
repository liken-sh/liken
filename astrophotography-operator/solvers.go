package main

// The Solver loop (plan 02). For each Solver, the operator keeps its
// Deployment and Service, and reports in the status where the solver
// listens, the index series it read, and the condition Ready. It reads
// the index list from the solver's pod once for each pod, when the pod
// turns Ready, because the pod reads the claim only when it starts.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strconv"
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

// solverLoop is the memory of the Solver loop: the memo of the
// Solvers' status writes, and the index list of each solver pod by the
// pod's UID. Both are caches: a new lead reads each list once more.
type solverLoop struct {
	versions *memo.Versions
	mu       sync.Mutex
	listed   map[string]solver.Indexes
}

func newSolverLoop() *solverLoop {
	return &solverLoop{versions: memo.New(), listed: map[string]solver.Indexes{}}
}

// solverPass serves every Solver once.
func (o *operator) solverPass(ctx context.Context, loop *solverLoop) error {
	held := informer.Held{View: o.stores.collections[astrophotography.SolverKind.Name].View(), Versions: loop.versions}
	solvers, err := informer.List[astrophotography.Solver](o.client, held, solversPath, memo.NamespacedPath(solverPath))
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
		uid, err := o.serveSolver(ctx, loop, s)
		pods[uid] = true
		if err != nil {
			errs = append(errs, fmt.Errorf("Solver %s/%s: %w", s.Metadata.Namespace, s.Metadata.Name, err))
		}
	}
	loop.forget(pods)
	return errors.Join(errs...)
}

// forget drops the index list of each pod that no Solver reports any
// more.
func (loop *solverLoop) forget(current map[string]bool) {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	for uid := range loop.listed {
		if !current[uid] {
			delete(loop.listed, uid)
		}
	}
}

// serveSolver keeps one Solver's workload and writes its status. It
// answers the UID of the pod it reported, so the loop keeps that pod's
// index list.
func (o *operator) serveSolver(ctx context.Context, loop *solverLoop, s *astrophotography.Solver) (string, error) {
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
		failed = report.failure
	}
	uid := ""
	if p != nil {
		uid = p.Metadata.UID
	}
	return uid, errors.Join(failed, o.writeSolverStatus(s, loop, report))
}

// errNoWorkerImage is the failure of a lead that could not name the
// worker image, which the Ready condition reports.
var errNoWorkerImage = errors.New("the operator could not name the image of the solver's pod; see its log")

// indexes answers the index list of a ready pod, from the cache or from
// the pod. It reads the pod's own address, not the Service: the cache
// keeps the list by the pod's UID, and the Service routes to a new pod
// only after the endpoints controller adds it, some time after the pod
// turns Ready. Until then a read through the Service is refused, or
// times out.
func (loop *solverLoop) indexes(ctx context.Context, o *operator, p *podObject) (solver.Indexes, error) {
	loop.mu.Lock()
	listed, held := loop.listed[p.Metadata.UID]
	loop.mu.Unlock()
	if held {
		return listed, nil
	}
	var indexes solver.Indexes
	url := "http://" + net.JoinHostPort(p.Status.PodIP, strconv.Itoa(solver.Port)) + solver.PathIndexes
	if err := o.peer(ctx, http.MethodGet, url, nil, &indexes); err != nil {
		return nil, fmt.Errorf("reading the index list of pod %s: %w", p.Metadata.Name, err)
	}
	loop.mu.Lock()
	loop.listed[p.Metadata.UID] = indexes
	loop.mu.Unlock()
	return indexes, nil
}

// solverReport is what the pass learned of one Solver.
type solverReport struct {
	claim    string
	pod      *podObject
	endpoint observatory.Endpoint
	indexes  solver.Indexes
	failure  error
}

// ready composes the condition Ready, and answers whether its status
// needs a person.
func (r solverReport) ready() (conditions.Condition, conditions.Status) {
	waits := ""
	if r.pod != nil {
		waits = r.pod.waiting()
	}
	switch {
	case r.failure != nil:
		return notReady(astrophotography.ReasonPodNotReady, r.failure.Error()), conditions.False
	case claimMissing(waits, r.claim):
		return notReady(astrophotography.ReasonClaimMissing, "the PersistentVolumeClaim "+r.claim+" does not exist: "+waits), conditions.False
	case r.pod == nil:
		return notReady(astrophotography.ReasonPodNotReady, "the solver's Deployment has no pod yet"), ""
	case waits != "":
		return notReady(astrophotography.ReasonPodNotReady, "the solver's pod cannot start: "+waits), conditions.False
	case !r.pod.ready():
		return notReady(astrophotography.ReasonPodNotReady, "the solver's pod "+r.pod.Metadata.Name+" is not ready"), ""
	case len(r.indexes) == 0:
		return notReady(astrophotography.ReasonNoIndexFiles, "the claim "+r.claim+" holds no index-*.fits file in the directory spec.indexFiles.path names"), conditions.False
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

// claimMissing reads the scheduler's message for a pod whose claim does
// not exist. The operator watches no claims, so the scheduler, which
// does, says so in the pod's PodScheduled condition, in the form
// persistentvolumeclaim "<name>" not found.
func claimMissing(message, claim string) bool {
	return strings.Contains(message, `persistentvolumeclaim "`+claim+`" not found`)
}

// writeSolverStatus writes the status that the report composes, when
// it differs from the stored one, and posts the Event of a transition
// of Ready after the write lands.
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
			status.Indexes = r.indexes
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
