package main

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/conditions"
)

// A Solver gets a Deployment of one pod that runs the solver mode in
// the worker image, with the claim read-only, and a Service in front of
// it, both owned by the Solver.
func TestASolverGetsItsDeploymentAndService(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.api.put(solversPath, solverObject("lab", map[string]any{"indexFiles": map[string]any{"claimName": "astrometry-indexes", "path": "2mass"}}))
		settle()
		d, held := objectOf[deployment](t, w.api, deploymentsPath, "observatory", "lab-solver")
		if !held {
			t.Fatal("no Deployment lab-solver")
		}
		pod := d.Spec.Template.Spec
		cases := []struct {
			name      string
			got, want any
		}{
			{"strategy", d.Spec.Strategy.Type, "Recreate"},
			{"replicas", *d.Spec.Replicas, int32(1)},
			{"image", pod.Containers[0].Image, testWorkerImage},
			{"args", strings.Join(pod.Containers[0].Args, " "), "solver --indexes /indexes/2mass --work /work"},
			{"claim", *pod.Volumes[0].PersistentVolumeClaim, claimName{ClaimName: "astrometry-indexes", ReadOnly: true}},
			{"owner", d.Metadata.OwnerReferences[0].Kind + "/" + d.Metadata.OwnerReferences[0].Name, "Solver/lab"},
			{"pod label", d.Spec.Template.Metadata.Labels[labelManagedBy], managedBy},
		}
		for _, c := range cases {
			if c.got != c.want {
				t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
			}
		}
		if _, held := objectOf[service](t, w.api, servicesPath, "observatory", "lab-solver"); !held {
			t.Error("no Service lab-solver")
		}
	})
}

// Ready follows the pod: no pod yet, then a pod whose claim does not
// exist, then a ready pod with the lab's index files. Each transition
// posts one Event, a Warning for the missing claim.
func TestReadyFollowsTheSolversPod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.api.put(solversPath, solverObject("lab", nil))
		settle()
		waiting := readyOf(w.solver(t, "lab"))
		w.api.put(podsPath, podFixture("lab-solver-0", "pod-0", workloadLabels(roleSolver, "lab"), false,
			`0/1 nodes are available: persistentvolumeclaim "astrometry-indexes" not found. preemption: 0/1 nodes are available.`))
		settle()
		missing := readyOf(w.solver(t, "lab"))
		w.api.remove(podsPath, "observatory", "lab-solver-0")
		w.readySolverPod("lab-solver-1", "pod-1")
		settle()
		lab := w.solver(t, "lab")
		cases := []struct {
			name           string
			got            astrophotographyCondition
			status, reason string
		}{
			{"no pod", waiting, "False", astrophotography.ReasonPodNotReady},
			{"no claim", missing, "False", astrophotography.ReasonClaimMissing},
			{"loaded", readyOf(lab), "True", astrophotography.ReasonLoaded},
		}
		for _, c := range cases {
			if string(c.got.Status) != c.status || c.got.Reason != c.reason {
				t.Errorf("%s: Ready %s %s (%s), want %s %s", c.name, c.got.Status, c.got.Reason, c.got.Message, c.status, c.reason)
			}
		}
		if lab.Status.Endpoint == nil || lab.Status.Endpoint.Host != "lab-solver.observatory.svc" || !slices.Equal(lab.Status.Indexes, labIndexes) {
			t.Errorf("status = %+v, want the endpoint and the lab's indexes", lab.Status)
		}
		if got := readyOf(lab).Message; got != "13 index files of the series 4207, 4208" {
			t.Errorf("message %q", got)
		}
		var posted []string
		for _, e := range w.api.recorded.About("Solver", "lab") {
			posted = append(posted, e.Type+" "+e.Reason)
		}
		want := []string{"Normal PodNotReady", "Warning ClaimMissing", "Normal Loaded"}
		if !slices.Equal(posted, want) {
			t.Errorf("Events %q, want %q", posted, want)
		}
	})
}

type astrophotographyCondition = conditions.Condition

// The solver reads its claim only when its pod starts, so the operator
// reads the index list once for each pod, and again for a new pod. It
// reads the list from the pod's own address, because the Service routes
// to a new pod only some time after the pod turns Ready.
func TestTheIndexListIsReadOnceFromEachPod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.api.put(solversPath, solverObject("lab", nil))
		w.readySolverPod("lab-solver-0", "pod-0")
		settle()
		w.api.put(targetsPath, object("observatory", "m42", nil, map[string]any{}))
		settle()
		w.api.remove(podsPath, "observatory", "lab-solver-0")
		w.peers.set(func(f *fakePeers) { f.indexes = labIndexes[:1] })
		w.readySolverPod("lab-solver-1", "pod-1")
		settle()
		first, second := w.peers.count("GET 10.42.0.10:8080/indexes"), w.peers.count("GET 10.42.0.11:8080/indexes")
		if first != 1 || second != 1 {
			t.Errorf("the index list was read %d times from the first pod and %d from the second, want 1 and 1", first, second)
		}
		if got := w.solver(t, "lab").Status.Indexes; !slices.Equal(got, labIndexes[:1]) {
			t.Errorf("indexes %+v, want the new pod's", got)
		}
	})
}

// A claim with no index file starts a pod that reports none.
func TestASolverWithNoIndexFilesIsNotReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.peers.set(func(f *fakePeers) { f.indexes = nil })
		w.api.put(solversPath, solverObject("lab", nil))
		w.readySolverPod("lab-solver-0", "pod-0")
		settle()
		if ready := readyOf(w.solver(t, "lab")); ready.Reason != astrophotography.ReasonNoIndexFiles {
			t.Errorf("Ready %s %s, want NoIndexFiles", ready.Status, ready.Reason)
		}
	})
}

// A pod that does not answer its index list yet is read again after
// the backoff, with no change in the cluster to wake the loop.
func TestAnIndexListThatFailsIsReadAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.peers.set(func(f *fakePeers) { f.refuseIndexes = 1000 })
		w.api.put(solversPath, solverObject("lab", nil))
		w.readySolverPod("lab-solver-0", "pod-0")
		settle()
		failing := readyOf(w.solver(t, "lab"))
		w.peers.set(func(f *fakePeers) { f.refuseIndexes = 0 })
		time.Sleep(retryAfter)
		synctest.Wait()
		if failing.Reason != astrophotography.ReasonPodNotReady || readyOf(w.solver(t, "lab")).Reason != astrophotography.ReasonLoaded {
			t.Errorf("Ready was %s (%s) and then %s, want PodNotReady and then Loaded",
				failing.Reason, failing.Message, readyOf(w.solver(t, "lab")).Reason)
		}
	})
}

// A pod that no node fits, for a reason other than a missing claim,
// says why in Ready's message.
func TestAPodThatCannotStartSaysWhy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.api.put(solversPath, solverObject("lab", nil))
		w.api.put(podsPath, podFixture("lab-solver-0", "pod-0", workloadLabels(roleSolver, "lab"), false, "0/1 nodes are available: 1 Insufficient memory."))
		settle()
		ready := readyOf(w.solver(t, "lab"))
		if ready.Reason != astrophotography.ReasonPodNotReady || !strings.Contains(ready.Message, "Insufficient memory") {
			t.Errorf("Ready %s: %s", ready.Reason, ready.Message)
		}
	})
}

// A pod that is scheduled and starts is not ready until its probe
// passes.
func TestAPodThatStartsIsNotReadyYet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.api.put(solversPath, solverObject("lab", nil))
		w.api.put(podsPath, podFixture("lab-solver-0", "pod-0", workloadLabels(roleSolver, "lab"), false, ""))
		settle()
		ready := readyOf(w.solver(t, "lab"))
		if ready.Reason != astrophotography.ReasonPodNotReady || !strings.Contains(ready.Message, "is not ready") {
			t.Errorf("Ready %s: %s", ready.Reason, ready.Message)
		}
	})
}

// An edit of the spec changes the template, so the operator replaces
// the Deployment, and the Deployment controller starts a new pod.
func TestAnEditOfTheIndexFilesReplacesTheDeployment(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.api.put(solversPath, solverObject("lab", nil))
		settle()
		w.api.put(solversPath, solverObject("lab", map[string]any{"indexFiles": map[string]any{"claimName": "indexes-2"}}))
		settle()
		d, _ := objectOf[deployment](t, w.api, deploymentsPath, "observatory", "lab-solver")
		if claim := d.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName; claim != "indexes-2" {
			t.Errorf("the Deployment mounts %s, want indexes-2", claim)
		}
		if writes := w.api.writes(deploymentsPath); len(writes) != 2 {
			t.Errorf("writes %q, want a create and a replace", writes)
		}
	})
}

// A lead that could not name the worker image starts no pod, and says
// so in Ready.
func TestASolverWithNoWorkerImageSaysWhy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, func(o *operator) { o.workerImage = "" })
		w.api.put(solversPath, solverObject("lab", nil))
		settle()
		ready := readyOf(w.solver(t, "lab"))
		if ready.Reason != astrophotography.ReasonPodNotReady || ready.Message != errNoWorkerImage.Error() {
			t.Errorf("Ready %s: %s", ready.Reason, ready.Message)
		}
		if _, held := objectOf[deployment](t, w.api, deploymentsPath, "observatory", "lab-solver"); held {
			t.Error("a Deployment with no image")
		}
	})
}

// A pass that finds nothing new writes nothing, so its own status write
// does not wake it into another write.
func TestASettledSolverIsNotWrittenAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.api.put(solversPath, solverObject("lab", nil))
		w.readySolverPod("lab-solver-0", "pod-0")
		settle()
		before := len(w.api.writes(solversPath))
		w.api.put(targetsPath, object("observatory", "m42", nil, map[string]any{}))
		settle()
		if after := len(w.api.writes(solversPath)); after != before {
			t.Errorf("%d status writes after a change elsewhere, want none", after-before)
		}
	})
}
