package main

// The retraction barrier: which features a pass holds back, what the
// machine stages while it holds them, and what an operator reads
// when nothing can move.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// clusterWithFeatures builds the decision's Cluster document
// declaring the named features and nothing else. Declaring a feature
// also enables what it requires, so a document that declares traefik
// runs helm too, and that implied pair is what the barrier exists
// for.
func clusterWithFeatures(slugs ...string) *cluster.Cluster {
	doc := decisionCluster()
	doc.Spec.Features = map[string]*cluster.FeatureConfig{}
	for _, slug := range slugs {
		doc.Spec.Features[slug] = &cluster.FeatureConfig{}
	}
	return doc
}

// A stubEvaluator stands in for the live cluster. It answers each
// precondition from a table, records every precondition it was given,
// so a test can state that a pass evaluated none, and it can fail
// every evaluation the way an unreachable API server does.
type stubEvaluator struct {
	holds map[cluster.Precondition]bool
	fail  error
	asked []cluster.Precondition
}

func (s *stubEvaluator) evaluate(p cluster.Precondition) (bool, string, error) {
	s.asked = append(s.asked, p)
	if s.fail != nil {
		return false, "", s.fail
	}
	return s.holds[p], "the cluster still holds Services of type LoadBalancer (default/whoami)", nil
}

func declared(doc *cluster.Cluster, slug string) bool {
	_, ok := doc.Spec.Features[slug]
	return ok
}

func TestARetractionWaitsForItsPrecondition(t *testing.T) {
	// An edit that removes traefik retracts helm with it, because
	// nothing else requires helm. The HelmCharts still exist at this
	// moment, so helm stays and traefik goes.
	boot := clusterWithFeatures("traefik")
	desired := clusterWithFeatures()
	stub := &stubEvaluator{}

	reduced, held := reduceRetraction(boot, desired, stub.evaluate)

	if !declared(reduced, "helm") || declared(reduced, "traefik") {
		t.Errorf("the staged document keeps helm and drops traefik: %+v", reduced.Spec.Features)
	}
	if len(held) != 1 || held[0].slug != "helm" {
		t.Errorf("got %+v", held)
	}
	if declared(desired, "helm") {
		t.Error("the reduction must not edit the live document the rest of the pass reads")
	}
}

func TestARetractionProceedsOnceItsPreconditionHolds(t *testing.T) {
	// One convergence later: traefik stopped, k3s deleted the
	// HelmChart, and the Helm controller uninstalled the release.
	boot := clusterWithFeatures("helm")
	desired := clusterWithFeatures()
	stub := &stubEvaluator{holds: map[cluster.Precondition]bool{cluster.NoHelmCharts: true}}

	reduced, held := reduceRetraction(boot, desired, stub.evaluate)

	if declared(reduced, "helm") || len(held) != 0 {
		t.Errorf("nothing depends on helm now, so it stops: %+v %+v", reduced.Spec.Features, held)
	}
}

func TestAFeatureWithNoPreconditionStopsWithoutAnyReads(t *testing.T) {
	// metrics-server and network-policy leave behind no object that
	// another one depends on, so their retraction states no
	// precondition and the pass reads nothing.
	boot := clusterWithFeatures("metrics-server", "network-policy")
	desired := clusterWithFeatures()
	stub := &stubEvaluator{}

	reduced, held := reduceRetraction(boot, desired, stub.evaluate)

	if len(reduced.Spec.Features) != 0 || len(held) != 0 {
		t.Errorf("got %+v %+v", reduced.Spec.Features, held)
	}
	if len(stub.asked) != 0 {
		t.Errorf("an edit with no precondition to satisfy reads nothing: %v", stub.asked)
	}
}

func TestAnUnchangedDocumentEvaluatesNothing(t *testing.T) {
	boot := clusterWithFeatures("traefik", "servicelb")
	stub := &stubEvaluator{}

	reduceRetraction(boot, clusterWithFeatures("traefik", "servicelb"), stub.evaluate)

	if len(stub.asked) != 0 {
		t.Errorf("the ordinary pass stops no feature, so it costs no reads: %v", stub.asked)
	}
}

func TestAnUnansweredPreconditionHoldsTheFeature(t *testing.T) {
	// The safe direction: a feature held on a failed read costs a
	// delayed retraction, and the next pass evaluates it again. A
	// feature stopped on a failed read costs the stranded objects the
	// barrier prevents.
	boot := clusterWithFeatures("servicelb")
	desired := clusterWithFeatures()
	stub := &stubEvaluator{fail: errors.New("connection refused")}

	reduced, held := reduceRetraction(boot, desired, stub.evaluate)

	if !declared(reduced, "servicelb") || len(held) != 1 {
		t.Fatalf("got %+v %+v", reduced.Spec.Features, held)
	}
	if !strings.Contains(held[0].blocker, "connection refused") {
		t.Errorf("the hold says why it could not decide: %q", held[0].blocker)
	}
}

func TestAnUnreadableBootDocumentRecognizesNoRetraction(t *testing.T) {
	desired := clusterWithFeatures()
	stub := &stubEvaluator{}

	reduced, held := reduceRetraction(nil, desired, stub.evaluate)

	if reduced != desired || len(held) != 0 || len(stub.asked) != 0 {
		t.Errorf("with nothing to compare against there is no retraction to hold: %+v %v", held, stub.asked)
	}
}

// blockedRetraction runs a whole convergence decision for a document
// whose only edit is a retraction the cluster refuses: the reduction
// hands back exactly the document this boot runs.
func blockedRetraction(t *testing.T) convergence {
	t.Helper()
	boot := clusterWithFeatures("servicelb")
	_, bootHash, err := renderCluster(boot.Metadata.Name, boot.Spec)
	if err != nil {
		t.Fatal(err)
	}
	reduced, held := reduceRetraction(boot, clusterWithFeatures(), (&stubEvaluator{}).evaluate)
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-raw-hash")
	return decideClusterConvergence(reduced, held, machineWithPolicy(machine.RebootAuto), facts, nil,
		boot, bootHash, "", turnGranted)
}

func TestABlockedRetractionNamesWhatMustGo(t *testing.T) {
	conv := blockedRetraction(t)

	if conv.condition.Reason != "RetractionBlocked" || conv.condition.Status != "False" {
		t.Fatalf("got %+v", conv.condition)
	}
	if !strings.Contains(conv.condition.Message, "servicelb") {
		t.Errorf("the message names the feature that cannot stop: %q", conv.condition.Message)
	}
	if !strings.Contains(conv.condition.Message, "default/whoami") {
		t.Errorf("the message names what the deployment must remove: %q", conv.condition.Message)
	}
}

func TestABlockedRetractionDisruptsNothing(t *testing.T) {
	conv := blockedRetraction(t)

	if conv.stage || conv.requestReboot || conv.requestRestart {
		t.Errorf("no reboot moves a machine that already runs the reduced document: %+v", conv)
	}
}

func TestABlockedRetractionReadsAsBlocked(t *testing.T) {
	phase := decidePhase([]api.Condition{blockedRetraction(t).condition})

	if phase != api.PhaseBlocked {
		t.Errorf("only a change to the cluster's objects clears this: %s", phase)
	}
}

func TestTheReducedDocumentIsWhatGetsStaged(t *testing.T) {
	// The machine stages the document with helm put back, not the
	// one the deployment wrote. This is what makes a reboot in the
	// middle of the retraction safe: the machine comes back up still
	// running the held feature.
	boot := clusterWithFeatures("traefik")
	_, bootHash, err := renderCluster(boot.Metadata.Name, boot.Spec)
	if err != nil {
		t.Fatal(err)
	}
	reduced, held := reduceRetraction(boot, clusterWithFeatures(), (&stubEvaluator{}).evaluate)
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-raw-hash")

	conv := decideClusterConvergence(reduced, held, machineWithPolicy(machine.RebootAuto), facts, nil,
		boot, bootHash, "", turnGranted)

	if !conv.stage {
		t.Fatalf("stopping traefik alone is progress, so it stages: %+v", conv)
	}
	_, want, _ := renderCluster(reduced.Metadata.Name, reduced.Spec)
	if conv.hash != want {
		t.Errorf("the staged bytes are the reduced document's: %s, want %s", conv.hash, want)
	}
	staged := string(conv.manifest)
	if !strings.Contains(staged, "helm") || strings.Contains(staged, "traefik") {
		t.Errorf("the staged document holds helm and drops traefik:\n%s", staged)
	}
}

func TestAHeldFeatureKeepsTheConfigurationItRunsUnder(t *testing.T) {
	// A held feature goes on running, so it must go on running as
	// itself. Rewriting its declaration to {} would hold a different
	// feature from the one the machine has up.
	configured := &cluster.FeatureConfig{"repository": "ssh://git@forge.example/fleet.git"}
	reduced := withFeature(clusterWithFeatures(), "flux", configured)
	if got := reduced.Spec.Features["flux"]; got != configured {
		t.Errorf("got %v, want the boot document's own declaration", got)
	}
}

func TestAHeldRequirementNobodyDeclaredGetsTheZeroConfiguration(t *testing.T) {
	reduced := withFeature(clusterWithFeatures(), "helm", nil)
	if got := reduced.Spec.Features["helm"]; got == nil || len(*got) != 0 {
		t.Errorf("got %v, want an empty configuration", got)
	}
}

func TestNameListStopsCounting(t *testing.T) {
	names := []string{"a/1", "a/2", "a/3", "a/4", "a/5"}

	if got := nameList(names); got != "a/1, a/2, a/3 and 2 more" {
		t.Errorf("got %q", got)
	}
	if got := nameList(names[:2]); got != "a/1, a/2" {
		t.Errorf("a short list is named in full: %q", got)
	}
}

// collectionServer answers one list URL with the objects given, and
// answers 404 everywhere else, the way an API server answers for a
// kind it does not serve.
func collectionServer(t *testing.T, path string, objects ...map[string]any) *kubernetes.Client {
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

func TestEvaluatePreconditionNamesTheObjectsThatRemain(t *testing.T) {
	cases := map[string]struct {
		precondition cluster.Precondition
		client       func(t *testing.T) *kubernetes.Client
		holds        bool
		names        string
	}{
		"a chart still installed keeps helm running": {
			precondition: cluster.NoHelmCharts,
			client: func(t *testing.T) *kubernetes.Client {
				return collectionServer(t, kubernetes.HelmChartsPath,
					namedObject("kube-system", "traefik", nil))
			},
			names: "kube-system/traefik",
		},
		"no charts left releases helm": {
			precondition: cluster.NoHelmCharts,
			client: func(t *testing.T) *kubernetes.Client {
				return collectionServer(t, kubernetes.HelmChartsPath)
			},
			holds: true,
		},
		"a LoadBalancer Service keeps servicelb running": {
			precondition: cluster.NoLoadBalancerServices,
			client: func(t *testing.T) *kubernetes.Client {
				return collectionServer(t, "/api/v1/services",
					namedObject("default", "whoami", map[string]any{"type": "LoadBalancer"}))
			},
			names: "default/whoami",
		},
		"ordinary Services release servicelb": {
			precondition: cluster.NoLoadBalancerServices,
			client: func(t *testing.T) *kubernetes.Client {
				return collectionServer(t, "/api/v1/services",
					namedObject("default", "kubernetes", map[string]any{"type": "ClusterIP"}))
			},
			holds: true,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			holds, blocker, err := evaluatePrecondition(c.client(t), c.precondition)
			if err != nil {
				t.Fatal(err)
			}
			if holds != c.holds {
				t.Fatalf("holds = %v, want %v (%s)", holds, c.holds, blocker)
			}
			if !strings.Contains(blocker, c.names) {
				t.Errorf("the blocker names what remains: %q", blocker)
			}
		})
	}
}

func TestEvaluatePreconditionCarriesAFailedRead(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))

	if _, _, err := evaluatePrecondition(client, cluster.NoLoadBalancerServices); err == nil {
		t.Error("a refused read is not an answer")
	}
}

func TestAnUnservedHelmChartKindHolds(t *testing.T) {
	// A cluster that never ran the Helm controller serves no
	// HelmChart kind at all. There is nothing to wait for.
	holds, _, err := evaluatePrecondition(collectionServer(t, "/served/nowhere"), cluster.NoHelmCharts)

	if err != nil || !holds {
		t.Errorf("holds = %v, err = %v", holds, err)
	}
}
