package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// osPod builds a pod with one container that runs the given image,
// Ready or not.
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
		t.Fatalf("the condition is %+v, want Unknown with reason FactsIncomplete when there are no facts", v.condition)
	}
}

func TestImportsPromotionWithNothingTracked(t *testing.T) {
	v := decideImportsPromotion(importsInputs{}, importsFacts("", ""))
	if v.promote || v.condition.Status != api.ConditionTrue || v.condition.Reason != "NotTracked" {
		t.Fatalf("the condition is %+v, want True with reason NotTracked for an untracked boot", v.condition)
	}
}

func TestImportsPromotionProvenBootIsConverged(t *testing.T) {
	v := decideImportsPromotion(importsInputs{}, importsFacts(machine.ManifestSourceProven, "abc"))
	if v.promote || v.condition.Status != api.ConditionTrue || v.condition.Reason != "Converged" {
		t.Fatalf("the condition is %+v, want True with reason Converged for a proven boot and no promotion", v.condition)
	}
}

func TestImportsPromotionAlreadyPromotedThisBoot(t *testing.T) {
	// The facts say Staged for the whole boot, but an earlier pass
	// promoted the record, so the proven record matches the boot.
	in := importsInputs{provenHash: "abc"}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if v.promote || v.condition.Status != api.ConditionTrue || v.condition.Reason != "Converged" {
		t.Fatalf("the condition is %+v, want True with reason Converged and no promotion for a promoted trial", v.condition)
	}
}

func TestImportsPromotionMissingTrialIsUnknown(t *testing.T) {
	// Nothing is staged, and the proven record is not this boot's.
	in := importsInputs{provenHash: "something else"}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if v.promote || v.condition.Status != api.ConditionUnknown || v.condition.Reason != "MachineStateUnavailable" {
		t.Fatalf("the condition is %+v, want Unknown with reason MachineStateUnavailable when no record is staged", v.condition)
	}
}

func TestImportsPromotionStoreErrorIsUnknown(t *testing.T) {
	in := importsInputs{storeErr: errors.New("machineState is on fire")}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if v.promote || v.condition.Status != api.ConditionUnknown || v.condition.Reason != "MachineStateUnavailable" {
		t.Fatalf("the condition is %+v, want Unknown when the store cannot be read", v.condition)
	}
}

func TestImportsPromotionStaleFactsAreUnknown(t *testing.T) {
	in := importsInputs{stagedHash: "def"}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if v.promote || v.condition.Status != api.ConditionUnknown || v.condition.Reason != "FactsIncomplete" {
		t.Fatalf("the condition is %+v, want no promotion when the staged record does not match the facts", v.condition)
	}
}

func TestImportsPromotionPodsErrorIsUnknown(t *testing.T) {
	in := importsInputs{stagedHash: "abc", podsErr: errors.New("apiserver away")}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if v.promote || v.condition.Status != api.ConditionUnknown || v.condition.Reason != "ClusterUnavailable" {
		t.Fatalf("the condition is %+v, want Unknown and no promotion when the pod list fails", v.condition)
	}
}

func TestImportsPromotionWaitsForOSPods(t *testing.T) {
	in := importsInputs{stagedHash: "abc"}
	v := decideImportsPromotion(in, importsFacts(machine.ManifestSourceStaged, "abc"))
	if v.promote || v.condition.Status != api.ConditionFalse || v.condition.Reason != "Proving" {
		t.Fatalf("the condition is %+v, want False with reason Proving when the node has no OS pods", v.condition)
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
		t.Fatalf("the condition is %+v, want False with reason Proving while one OS container is not ready", v.condition)
	}
	if !strings.Contains(v.condition.Message, "liken-iscsid-y") {
		t.Fatalf("the message is %q, want it to name liken-iscsid-y", v.condition.Message)
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
		t.Fatalf("the condition is %+v, want promotion although a workload pod is not ready", v.condition)
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
		t.Fatalf("the condition is %+v, want promotion although a completed pod has no ready containers", v.condition)
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
		t.Fatalf("the condition is %+v, want promotion when every OS container is ready", v.condition)
	}
	if v.condition.Status != api.ConditionTrue || v.condition.Reason != "Converged" {
		t.Fatalf("the condition is %+v, want True with reason Converged after promotion", v.condition)
	}
}

// A test of settleImportsLifecycle that reaches the promotion points
// containerStoreDir at a temporary directory, so syncfs does not touch
// a host filesystem.

func TestSettleImportsLifecycleUntrackedBootNeedsNoStore(t *testing.T) {
	client := testClient(t, (&drainAPI{}).handler())
	c := settleImportsLifecycle(&reader{client: client}, t.TempDir(), "node-1", importsFacts("", ""), nil)
	if c.Status != api.ConditionTrue || c.Reason != "NotTracked" {
		t.Errorf("the condition is %+v, want True with reason NotTracked", c)
	}
}

func TestSettleImportsLifecycleReportsAProvingTrial(t *testing.T) {
	root := t.TempDir()
	raw := []byte("kind: ImportedImages\n")
	if err := machine.ImportedImagesStore(root).WriteStaged(raw); err != nil {
		t.Fatalf("writing the staged record: %v", err)
	}
	client := testClient(t, (&drainAPI{}).handler())
	c := settleImportsLifecycle(&reader{client: client}, root, "node-1",
		importsFacts(machine.ManifestSourceStaged, machine.ManifestHash(raw)), nil)
	if c.Status != api.ConditionFalse || c.Reason != "Proving" {
		t.Errorf("the condition is %+v, want Proving when no OS pods are listed", c)
	}
}

func TestSettleImportsLifecycleSeesAnEarlierPromotion(t *testing.T) {
	root := t.TempDir()
	raw := []byte("kind: ImportedImages\n")
	if err := machine.ImportedImagesStore(root).WriteProven(raw); err != nil {
		t.Fatalf("writing the proven record: %v", err)
	}
	client := testClient(t, (&drainAPI{}).handler())
	c := settleImportsLifecycle(&reader{client: client}, root, "node-1",
		importsFacts(machine.ManifestSourceStaged, machine.ManifestHash(raw)), nil)
	if c.Status != api.ConditionTrue || c.Reason != "Converged" {
		t.Errorf("the condition is %+v, want Converged for a promoted trial", c)
	}
}

func TestSettleImportsLifecycleIgnoresAStaleStagedRecord(t *testing.T) {
	root := t.TempDir()
	if err := machine.ImportedImagesStore(root).WriteStaged([]byte("newer trial\n")); err != nil {
		t.Fatalf("writing the staged record: %v", err)
	}
	client := testClient(t, (&drainAPI{}).handler())
	c := settleImportsLifecycle(&reader{client: client}, root, "node-1",
		importsFacts(machine.ManifestSourceStaged, "hash-of-what-this-boot-ran"), nil)
	if c.Status != api.ConditionUnknown || c.Reason != "FactsIncomplete" {
		t.Errorf("the condition is %+v, want Unknown when the staged record does not match the facts", c)
	}
}

// stagedImportsBoot writes a staged imports record into a new state
// root, and returns the root and the facts of a boot that ran that
// record.
func stagedImportsBoot(t *testing.T) (string, *machine.MachineStatus) {
	t.Helper()
	root := t.TempDir()
	raw := []byte("kind: ImportedImages\n")
	if err := machine.ImportedImagesStore(root).WriteStaged(raw); err != nil {
		t.Fatalf("writing the staged record: %v", err)
	}
	return root, importsFacts(machine.ManifestSourceStaged, machine.ManifestHash(raw))
}

// When every OS container serves, the pass syncs the container store
// and promotes the staged record, so the next boot trusts the store.
func TestSettleImportsLifecyclePromotesWhenTheOSServes(t *testing.T) {
	containerStoreDir = t.TempDir()
	t.Cleanup(func() { containerStoreDir = machine.K3sAgentDir })
	root, facts := stagedImportsBoot(t)
	server := &drainAPI{pods: []kubernetes.Pod{osPod("liken-dns-abc", osImagePrefix+"dns:1", true)}}
	out := &passOutcome{}

	c := settleImportsLifecycle(&reader{client: testClient(t, server.handler())}, root, "node-1", facts, out)

	proven, _ := machine.ImportedImagesStore(root).LoadProven()
	if c.Reason != "Converged" || proven == nil || len(out.writes) != 1 {
		t.Errorf("condition %+v, proven record %q, writes %q; want the record promoted", c, proven, out.writes)
	}
}

// When the container store cannot be synced, the record is not
// promoted.
func TestSettleImportsLifecycleDoesNotPromoteWhenTheSyncFails(t *testing.T) {
	containerStoreDir = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { containerStoreDir = machine.K3sAgentDir })
	root, facts := stagedImportsBoot(t)
	server := &drainAPI{pods: []kubernetes.Pod{osPod("liken-dns-abc", osImagePrefix+"dns:1", true)}}
	out := &passOutcome{}

	c := settleImportsLifecycle(&reader{client: testClient(t, server.handler())}, root, "node-1", facts, out)

	proven, _ := machine.ImportedImagesStore(root).LoadProven()
	if c.Reason != "PromotionFailed" || proven != nil || len(out.failures) != 1 {
		t.Errorf("condition %+v, proven record %q, failures %v; want the promotion held and one failure", c, proven, out.failures)
	}
}
