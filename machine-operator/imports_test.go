package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// osPod builds a pod whose one container runs a liken OS image, ready
// or not, for exercising the promotion proof.
func osPod(name, image string, ready bool) kubernetes.Pod {
	p := kubernetes.Pod{}
	p.Metadata.Name = name
	p.Metadata.Namespace = "liken-system"
	p.Status.Phase = "Running"
	p.Status.ContainerStatuses = []kubernetes.ContainerStatus{
		{Name: "main", Image: image, Ready: ready},
	}
	return p
}

func importsFacts(source machine.ManifestSource, hash string) *machine.MachineStatus {
	return &machine.MachineStatus{Boot: machine.BootStatus{ImportsSource: source, ImportsHash: hash}}
}

func TestImportsPromotionNeedsFacts(t *testing.T) {
	v := decideImportsPromotion(importsInputs{}, nil)
	if v.promote || v.condition.Status != api.ConditionUnknown || v.condition.Reason != "FactsIncomplete" {
		t.Fatalf("no facts, no verdict: %+v", v.condition)
	}
}

func TestImportsPromotionWithNothingTracked(t *testing.T) {
	v := decideImportsPromotion(importsInputs{}, importsFacts("", ""))
	if v.promote || v.condition.Status != api.ConditionTrue || v.condition.Reason != "NotTracked" {
		t.Fatalf("an untracked boot has nothing to prove: %+v", v.condition)
	}
}

func TestImportsPromotionProvenBootIsConverged(t *testing.T) {
	v := decideImportsPromotion(importsInputs{}, importsFacts(machine.ManifestSourceProven, "abc"))
	if v.promote || v.condition.Status != api.ConditionTrue || v.condition.Reason != "Converged" {
		t.Fatalf("a proven boot is already converged: %+v", v.condition)
	}
}

func TestImportsPromotionAlreadyPromotedThisBoot(t *testing.T) {
	// Facts say Staged for the whole boot, but an earlier pass
	// promoted: the store's proven record now matches the boot.
	in := importsInputs{provenHash: "abc"}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if v.promote || v.condition.Status != api.ConditionTrue || v.condition.Reason != "Converged" {
		t.Fatalf("an already-promoted trial is converged: %+v", v.condition)
	}
}

func TestImportsPromotionMissingTrialIsUnknown(t *testing.T) {
	// Nothing staged, and the proven record isn't this boot's either:
	// the store no longer holds the trial the facts describe.
	in := importsInputs{provenHash: "something else"}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if v.promote || v.condition.Status != api.ConditionUnknown || v.condition.Reason != "MachineStateUnavailable" {
		t.Fatalf("a vanished trial can't be judged: %+v", v.condition)
	}
}

func TestImportsPromotionStoreErrorIsUnknown(t *testing.T) {
	in := importsInputs{storeErr: errors.New("machineState is on fire")}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if v.promote || v.condition.Status != api.ConditionUnknown || v.condition.Reason != "MachineStateUnavailable" {
		t.Fatalf("an unreadable store can't be judged: %+v", v.condition)
	}
}

func TestImportsPromotionStaleFactsAreUnknown(t *testing.T) {
	in := importsInputs{stagedHash: "def"}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if v.promote || v.condition.Status != api.ConditionUnknown || v.condition.Reason != "FactsIncomplete" {
		t.Fatalf("a staged record the facts don't describe can't be promoted: %+v", v.condition)
	}
}

func TestImportsPromotionPodsErrorIsUnknown(t *testing.T) {
	in := importsInputs{stagedHash: "abc", podsErr: errors.New("apiserver away")}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if v.promote || v.condition.Status != api.ConditionUnknown || v.condition.Reason != "ClusterUnavailable" {
		t.Fatalf("no pods listing, no proof: %+v", v.condition)
	}
}

func TestImportsPromotionWaitsForOSPods(t *testing.T) {
	in := importsInputs{stagedHash: "abc"}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if v.promote || v.condition.Status != api.ConditionFalse || v.condition.Reason != "Proving" {
		t.Fatalf("no OS pods on the node yet means the trial is still proving: %+v", v.condition)
	}
}

func TestImportsPromotionWaitsForEveryOSContainer(t *testing.T) {
	in := importsInputs{
		stagedHash: "abc",
		pods: []kubernetes.Pod{
			osPod("liken-machine-operator-x", "liken.sh/machine-operator:installed", true),
			osPod("liken-iscsid-y", "liken.sh/iscsid:installed", false),
		},
	}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if v.promote || v.condition.Status != api.ConditionFalse || v.condition.Reason != "Proving" {
		t.Fatalf("one torn OS image must hold the whole promotion: %+v", v.condition)
	}
	if !strings.Contains(v.condition.Message, "liken-iscsid-y") {
		t.Fatalf("the message should name what's still proving: %s", v.condition.Message)
	}
}

func TestImportsPromotionIgnoresWorkloadImages(t *testing.T) {
	crashing := osPod("some-app", "docker.io/library/nginx:latest", false)
	in := importsInputs{
		stagedHash: "abc",
		pods: []kubernetes.Pod{
			osPod("liken-machine-operator-x", "liken.sh/machine-operator:installed", true),
			crashing,
		},
	}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if !v.promote {
		t.Fatalf("a crashing workload is not the imports' problem: %+v", v.condition)
	}
}

func TestImportsPromotionIgnoresCompletedPods(t *testing.T) {
	done := osPod("liken-job", "liken.sh/machine-operator:installed", false)
	done.Status.Phase = "Succeeded"
	in := importsInputs{
		stagedHash: "abc",
		pods: []kubernetes.Pod{
			osPod("liken-machine-operator-x", "liken.sh/machine-operator:installed", true),
			done,
		},
	}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if !v.promote {
		t.Fatalf("a completed pod's containers are legitimately not ready: %+v", v.condition)
	}
}

func TestImportsPromotionPromotesWhenTheOSServes(t *testing.T) {
	in := importsInputs{
		stagedHash: "abc",
		pods: []kubernetes.Pod{
			osPod("liken-machine-operator-x", "liken.sh/machine-operator:installed", true),
			osPod("machine-logs-y", "liken.sh/logs:installed", true),
		},
	}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if !v.promote {
		t.Fatalf("every OS container serves; the trial is proven: %+v", v.condition)
	}
	if v.condition.Status != api.ConditionTrue || v.condition.Reason != "Converged" {
		t.Fatalf("promotion converges the condition: %+v", v.condition)
	}
}

// The observing half, settleImportsLifecycle, gathers the store and
// pod evidence the decision judges. These tests stop short of an
// actual promotion, because promotion's syncfs barrier needs the real
// container store; the decision tests above already cover that
// verdict.

func TestSettleImportsLifecycleUntrackedBootNeedsNoStore(t *testing.T) {
	client := testClient(t, (&drainAPI{}).handler())
	c := settleImportsLifecycle(client, t.TempDir(), "node-1", importsFacts("", ""))
	if c.Status != api.ConditionTrue || c.Reason != "NotTracked" {
		t.Errorf("got %+v", c)
	}
}

func TestSettleImportsLifecycleReportsAProvingTrial(t *testing.T) {
	root := t.TempDir()
	raw := []byte("kind: ImportedImages\n")
	if err := machine.ImportedImagesStore(root).WriteStaged(raw); err != nil {
		t.Fatal(err)
	}
	client := testClient(t, (&drainAPI{}).handler())
	c := settleImportsLifecycle(client, root, "node-1",
		importsFacts(machine.ManifestSourceStaged, machine.ManifestHash(raw)))
	if c.Status != api.ConditionFalse || c.Reason != "Proving" {
		t.Errorf("no OS pods listed yet means the trial is still proving: %+v", c)
	}
}

func TestSettleImportsLifecycleSeesAnEarlierPromotion(t *testing.T) {
	root := t.TempDir()
	raw := []byte("kind: ImportedImages\n")
	if err := machine.ImportedImagesStore(root).WriteProven(raw); err != nil {
		t.Fatal(err)
	}
	client := testClient(t, (&drainAPI{}).handler())
	c := settleImportsLifecycle(client, root, "node-1",
		importsFacts(machine.ManifestSourceStaged, machine.ManifestHash(raw)))
	if c.Status != api.ConditionTrue || c.Reason != "Converged" {
		t.Errorf("an already-promoted trial is converged: %+v", c)
	}
}

func TestSettleImportsLifecycleIgnoresAStaleStagedRecord(t *testing.T) {
	root := t.TempDir()
	if err := machine.ImportedImagesStore(root).WriteStaged([]byte("newer trial\n")); err != nil {
		t.Fatal(err)
	}
	client := testClient(t, (&drainAPI{}).handler())
	c := settleImportsLifecycle(client, root, "node-1",
		importsFacts(machine.ManifestSourceStaged, "hash-of-what-this-boot-ran"))
	if c.Status != api.ConditionUnknown || c.Reason != "FactsIncomplete" {
		t.Errorf("a staged record the facts don't describe can't be judged: %+v", c)
	}
}
