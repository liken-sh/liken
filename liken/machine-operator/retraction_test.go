package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// clusterWithFeatures builds a Cluster document that declares only the
// named features. A feature also enables the features it requires, so
// a document that declares traefik runs helm too.
func clusterWithFeatures(slugs ...string) *cluster.Cluster {
	doc := decisionCluster()
	doc.Spec.Features = map[string]*cluster.FeatureConfig{}
	for _, slug := range slugs {
		doc.Spec.Features[slug] = &cluster.FeatureConfig{}
	}
	return doc
}

// stubEvaluator replaces the live cluster. It looks up each
// precondition in satisfied, records the preconditions it evaluated,
// and fails every evaluation when fail is set.
type stubEvaluator struct {
	satisfied map[cluster.Precondition]bool
	fail      error
	evaluated []cluster.Precondition
}

func (s *stubEvaluator) evaluate(p cluster.Precondition) (bool, string, error) {
	s.evaluated = append(s.evaluated, p)
	if s.fail != nil {
		return false, "", s.fail
	}
	return s.satisfied[p], "the cluster still holds Services of type LoadBalancer (default/whoami)", nil
}

func declared(doc *cluster.Cluster, slug string) bool {
	_, ok := doc.Spec.Features[slug]
	return ok
}

func TestARetractionWaitsForItsPrecondition(t *testing.T) {
	// Removing traefik also retracts helm, because nothing else
	// requires helm. The HelmCharts still exist, so helm stays and
	// traefik stops.
	boot := clusterWithFeatures("traefik")
	desired := clusterWithFeatures()
	stub := &stubEvaluator{}

	reduced, held := reduceRetraction(boot, desired, stub.evaluate)

	if !declared(reduced, "helm") || declared(reduced, "traefik") {
		t.Errorf("the staged document keeps helm and drops traefik: %+v", reduced.Spec.Features)
	}
	if len(held) != 1 || held[0].slug != "helm" {
		t.Errorf("held features are %+v, want only helm", held)
	}
	if declared(desired, "helm") {
		t.Error("the reduction changed the live document, want it unchanged because the rest of the pass reads it")
	}
}

func TestARetractionProceedsOnceItsPreconditionIsSatisfied(t *testing.T) {
	// Traefik has stopped, k3s has deleted the HelmChart, and the Helm
	// controller has uninstalled the release.
	boot := clusterWithFeatures("helm")
	desired := clusterWithFeatures()
	stub := &stubEvaluator{satisfied: map[cluster.Precondition]bool{cluster.NoHelmCharts: true}}

	reduced, held := reduceRetraction(boot, desired, stub.evaluate)

	if declared(reduced, "helm") || len(held) != 0 {
		t.Errorf("features are %+v and held is %+v, want helm stopped and nothing held because nothing depends on it", reduced.Spec.Features, held)
	}
}

func TestAFeatureWithNoPreconditionStopsWithoutAnyReads(t *testing.T) {
	// metrics-server and network-policy have no retraction
	// precondition.
	boot := clusterWithFeatures("metrics-server", "network-policy")
	desired := clusterWithFeatures()
	stub := &stubEvaluator{}

	reduced, held := reduceRetraction(boot, desired, stub.evaluate)

	if len(reduced.Spec.Features) != 0 || len(held) != 0 {
		t.Errorf("features are %+v and held is %+v, want both empty", reduced.Spec.Features, held)
	}
	if len(stub.evaluated) != 0 {
		t.Errorf("an edit with no precondition makes no reads: %v", stub.evaluated)
	}
}

func TestAnUnchangedDocumentEvaluatesNothing(t *testing.T) {
	boot := clusterWithFeatures("traefik", "servicelb")
	stub := &stubEvaluator{}

	reduceRetraction(boot, clusterWithFeatures("traefik", "servicelb"), stub.evaluate)

	if len(stub.evaluated) != 0 {
		t.Errorf("an unchanged document retracts no feature, so it makes no reads: %v", stub.evaluated)
	}
}

func TestAFailedPreconditionCheckKeepsTheFeatureRunning(t *testing.T) {
	boot := clusterWithFeatures("servicelb")
	desired := clusterWithFeatures()
	stub := &stubEvaluator{fail: errors.New("connection refused")}

	reduced, held := reduceRetraction(boot, desired, stub.evaluate)

	if !declared(reduced, "servicelb") || len(held) != 1 {
		t.Fatalf("features are %+v and held is %+v, want servicelb running and held", reduced.Spec.Features, held)
	}
	if !strings.Contains(held[0].blocker, "connection refused") {
		t.Errorf("the blocker is %q, want the reason for the failed check", held[0].blocker)
	}
}

func TestAnUnreadableBootDocumentHoldsNoFeature(t *testing.T) {
	desired := clusterWithFeatures()
	stub := &stubEvaluator{}

	reduced, held := reduceRetraction(nil, desired, stub.evaluate)

	if reduced != desired || len(held) != 0 || len(stub.evaluated) != 0 {
		t.Errorf("held features are %+v and evaluations %v, want none because there is no boot document to compare", held, stub.evaluated)
	}
}

// blockedRetraction runs the convergence decision for a document whose
// only change is a retraction that the cluster blocks, so the reduced
// document equals the boot document.
func blockedRetraction(t *testing.T) convergence {
	t.Helper()
	boot := clusterWithFeatures("servicelb")
	_, bootHash, err := renderCluster(boot.Metadata.Name, boot.Spec)
	if err != nil {
		t.Fatalf("rendering the boot document: %v", err)
	}
	reduced, held := reduceRetraction(boot, clusterWithFeatures(), (&stubEvaluator{}).evaluate)
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-raw-hash")
	return decideClusterConvergence(reduced, held, machineWithPolicy(machine.RebootAuto), facts, nil,
		boot, bootHash, "", "", turnGranted)
}

func TestABlockedRetractionNamesTheBlockingObjects(t *testing.T) {
	conv := blockedRetraction(t)

	if conv.condition.Reason != "RetractionBlocked" || conv.condition.Status != "False" {
		t.Fatalf("the condition is %+v, want RetractionBlocked with status False", conv.condition)
	}
	if !strings.Contains(conv.condition.Message, "servicelb") {
		t.Errorf("the message is %q, want it to name the held feature", conv.condition.Message)
	}
	if !strings.Contains(conv.condition.Message, "default/whoami") {
		t.Errorf("the message names the objects that block the retraction: %q", conv.condition.Message)
	}
}

func TestABlockedRetractionDisruptsNothing(t *testing.T) {
	conv := blockedRetraction(t)

	if conv.stage || conv.requestReboot || conv.requestRestart {
		t.Errorf("the machine already runs the reduced document, so nothing needs a reboot: %+v", conv)
	}
}

func TestABlockedRetractionSetsPhaseBlocked(t *testing.T) {
	phase := decidePhase([]api.Condition{blockedRetraction(t).condition})

	if phase != api.PhaseBlocked {
		t.Errorf("only a change to the cluster's objects clears this phase: %s", phase)
	}
}

func TestTheReducedDocumentIsWhatGetsStaged(t *testing.T) {
	// The machine stages the document with helm added back, not the
	// one the deployment wrote.
	boot := clusterWithFeatures("traefik")
	_, bootHash, err := renderCluster(boot.Metadata.Name, boot.Spec)
	if err != nil {
		t.Fatalf("rendering the boot document: %v", err)
	}
	reduced, held := reduceRetraction(boot, clusterWithFeatures(), (&stubEvaluator{}).evaluate)
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-raw-hash")

	conv := decideClusterConvergence(reduced, held, machineWithPolicy(machine.RebootAuto), facts, nil,
		boot, bootHash, "", "", turnGranted)

	if !conv.stage {
		t.Fatalf("stopping traefik alone changes the document, so it stages: %+v", conv)
	}
	_, want, _ := renderCluster(reduced.Metadata.Name, reduced.Spec)
	if conv.hash != want {
		t.Errorf("the staged bytes are the reduced document: %s, want %s", conv.hash, want)
	}
	staged := string(conv.manifest)
	if !strings.Contains(staged, "helm") || strings.Contains(staged, "traefik") {
		t.Errorf("the staged document declares helm and not traefik:\n%s", staged)
	}
}

func TestAHeldFeatureKeepsTheConfigurationItRunsUnder(t *testing.T) {
	configured := &cluster.FeatureConfig{"repository": "ssh://git@forge.example/fleet.git"}
	reduced := withFeature(clusterWithFeatures(), "flux", configured)
	if got := reduced.Spec.Features["flux"]; got != configured {
		t.Errorf("the flux configuration is %v, want the boot document's own declaration", got)
	}
}

func TestAHeldRequirementNobodyDeclaredGetsAnEmptyConfiguration(t *testing.T) {
	reduced := withFeature(clusterWithFeatures(), "helm", nil)
	if got := reduced.Spec.Features["helm"]; got == nil || len(*got) != 0 {
		t.Errorf("the helm configuration is %v, want an empty configuration", got)
	}
}

func TestNameListShortensALongList(t *testing.T) {
	names := []string{"a/1", "a/2", "a/3", "a/4", "a/5"}

	if got := nameList(names); got != "a/1, a/2, a/3 and 2 more" {
		t.Errorf("nameList of 5 names is %q, want \"a/1, a/2, a/3 and 2 more\"", got)
	}
	if got := nameList(names[:2]); got != "a/1, a/2" {
		t.Errorf("nameList of 2 names is %q, want \"a/1, a/2\" because a short list shows every name", got)
	}
}

// collectionServer returns the given objects for a list request at
// path and 404 for every other path, as an API server does for a kind
// it does not serve.
func collectionServer(t *testing.T, path string, objects ...map[string]any) *apiclient.Client {
	t.Helper()
	return testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.Error(w, "the server could not find the requested resource", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": objects})
	}))
}

func namedObject(namespace, name string, spec map[string]any) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"namespace": namespace, "name": name},
		"spec":     spec,
	}
}

func TestEvaluatePreconditionNamesTheRemainingObjects(t *testing.T) {
	cases := map[string]struct {
		precondition cluster.Precondition
		client       func(t *testing.T) *apiclient.Client
		satisfied    bool
		names        string
	}{
		"a remaining chart blocks the helm precondition": {
			precondition: cluster.NoHelmCharts,
			client: func(t *testing.T) *apiclient.Client {
				return collectionServer(t, kubernetes.HelmChartsPath,
					namedObject("kube-system", "traefik", nil))
			},
			names: "kube-system/traefik",
		},
		"no charts satisfy the helm precondition": {
			precondition: cluster.NoHelmCharts,
			client: func(t *testing.T) *apiclient.Client {
				return collectionServer(t, kubernetes.HelmChartsPath)
			},
			satisfied: true,
		},
		"a LoadBalancer Service blocks the servicelb precondition": {
			precondition: cluster.NoLoadBalancerServices,
			client: func(t *testing.T) *apiclient.Client {
				return collectionServer(t, "/api/v1/services",
					namedObject("default", "whoami", map[string]any{"type": "LoadBalancer"}))
			},
			names: "default/whoami",
		},
		"other Services satisfy the servicelb precondition": {
			precondition: cluster.NoLoadBalancerServices,
			client: func(t *testing.T) *apiclient.Client {
				return collectionServer(t, "/api/v1/services",
					namedObject("default", "kubernetes", map[string]any{"type": "ClusterIP"}))
			},
			satisfied: true,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			satisfied, blocker, err := evaluatePrecondition(&reader{client: c.client(t)}, c.precondition)
			if err != nil {
				t.Fatalf("evaluating the precondition: %v", err)
			}
			if satisfied != c.satisfied {
				t.Fatalf("satisfied = %v, want %v (%s)", satisfied, c.satisfied, blocker)
			}
			if !strings.Contains(blocker, c.names) {
				t.Errorf("the blocker names the remaining objects: %q", blocker)
			}
		})
	}
}

func TestEvaluatePreconditionReturnsAFailedRead(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))

	if _, _, err := evaluatePrecondition(&reader{client: client}, cluster.NoLoadBalancerServices); err == nil {
		t.Error("evaluatePrecondition returned no error, want one because the read was refused")
	}
}

func TestAnUnservedHelmChartKindSatisfiesTheHelmPrecondition(t *testing.T) {
	// A cluster that never ran the Helm controller does not serve the
	// HelmChart kind, so there are no charts to wait for.
	satisfied, _, err := evaluatePrecondition(&reader{client: collectionServer(t, "/served/nowhere")}, cluster.NoHelmCharts)

	if err != nil || !satisfied {
		t.Errorf("satisfied is %v and the error is %v, want satisfied and no error", satisfied, err)
	}
}
