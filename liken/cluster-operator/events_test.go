package main

// These tests read the Events that the cluster operator posts about
// Machines and the Cluster from eventstest's fake events collection.
// They run in a synctest bubble so the recorder's goroutine uses the
// fake clock.

import (
	"fmt"
	"io"
	"net/http"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// recordingReader returns a fleetReader whose recorder posts Events to
// the events fake, which passes all other requests to handler.
func recordingReader(t *testing.T, handler http.Handler) (*fleetReader, *eventstest.Events) {
	t.Helper()
	recorded := &eventstest.Events{}
	client := testClient(t, recorded.Around(handler))
	recorder := events.New(t.Context(), client, component, events.Options{Instance: "liken-cluster-operator-0", Log: io.Discard})
	return &fleetReader{client: client, recorder: recorder}, recorded
}

// postedAbout returns the Events about one object as
// "Type Reason: message" strings, in the order the fake received them.
func postedAbout(recorded *eventstest.Events, kind, name string) []string {
	synctest.Wait()
	var out []string
	for _, e := range recorded.About(kind, name) {
		if e.Metadata.Namespace != "default" {
			out = append(out, "outside default: "+e.Reason)
		}
		out = append(out, e.Type+" "+e.Reason+": "+e.Message)
	}
	return out
}

func TestAPassPostsTheLostMachineAndTheClustersTransitions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
		clusterDoc.Spec.Leaders = []string{"node-1"}
		fake := &fleetAPI{
			clusterDoc: clusterDoc,
			machines: []machine.Machine{
				labMachine("node-1", api.PhaseReady),
				labMachine("node-2", api.PhaseReady),
				labMachine("node-3", api.PhaseReady),
			},
			renewals: map[string]time.Time{
				"node-1": passTime.Add(-10 * time.Second),
				"node-2": time.Date(2026, 7, 6, 11, 55, 0, 0, time.UTC),
			},
		}
		reads, recorded := recordingReader(t, fake.handler())
		fake.sawEachRenewal(reads)
		cm, _ := fleetMetrics(t)

		if err := reconcileFleet(reads, clusterDoc, "", &fluxProbe{}, &overduePodReports{}, cm, passTime); err != nil {
			t.Fatalf("reconciling the fleet: %v", err)
		}

		cases := []struct {
			kind, name string
			want       []string
		}{
			{machineKind, "node-1", nil},
			{machineKind, "node-2", []string{"Warning MachineLost: Marked the machine Lost. cluster-operator last saw its heartbeat Lease renewed at 2026-07-06T11:55:00Z"}},
			{machineKind, "node-3", []string{"Warning MachineLost: Marked the machine Lost. cluster-operator has never seen it renew a heartbeat Lease"}},
			{clusterKind, "lab", []string{
				"Warning MachinesDegraded: 1/3 machines are ready. Not ready and not updating: node-2, node-3",
				"Normal RolloutComplete: No machine waits for a reboot turn",
			}},
		}
		for _, c := range cases {
			if got := postedAbout(recorded, c.kind, c.name); !slices.Equal(got, c.want) {
				t.Errorf("the Events about %s %s are %q, want %q", c.kind, c.name, got, c.want)
			}
		}
	})
}

func TestARefusedWritePostsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clusterDoc := &cluster.Cluster{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
		fake := &fleetAPI{clusterDoc: clusterDoc}
		handler := refusing(refusing(fake.handler(), http.MethodPut, "/machines/node-1", http.StatusConflict),
			http.MethodPut, "/clusters/lab", http.StatusInternalServerError)
		reads, recorded := recordingReader(t, handler)

		markLost(reads, []machine.Machine{labMachine("node-1", api.PhaseReady)}, []string{"node-1"}, nil, passTime)
		s := decideFleet(nil, nil, passTime)
		publishClusterStatus(reads, clusterDoc, s, rollout{}, nil, "", "", passTime)

		synctest.Wait()
		if got := recorded.List(); len(got) != 0 {
			t.Errorf("refused writes posted %d Events, want none", len(got))
		}
	})
}

func TestCarryingOutTheRolloutPostsEachTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &fleetAPI{}
		reads, recorded := recordingReader(t, fake.handler())
		granted := labMachine("node-4", api.PhaseReady)
		granted.Status.Conditions = api.SetCondition(nil, api.Condition{
			Type: machine.RebootApprovedCondition, Status: api.ConditionTrue, Reason: "DisruptionBudgetAllows",
		}, passTime.Add(-time.Minute))
		machines := []machine.Machine{labMachine("node-3", api.PhaseUpdatePending), granted}

		carryOutRollout(reads, machines, rollout{grant: []string{"node-3"}, revoke: []string{"node-4"}}, []cluster.RebootTurn{{Machine: "node-3"}}, passTime)

		cases := []struct {
			name string
			want []string
		}{
			{"node-3", []string{"Normal RebootTurnGranted: Granted a reboot turn, because the disruption budgets allow one"}},
			{"node-4", []string{"Normal RebootTurnReclaimed: Removed the reboot turn grant, because the machine is available and requests no reboot"}},
		}
		for _, c := range cases {
			if got := postedAbout(recorded, machineKind, c.name); !slices.Equal(got, c.want) {
				t.Errorf("the Events about Machine %s are %q, want %q", c.name, got, c.want)
			}
		}
	})
}

// recordingClient is recordingReader for the flux tests, which take
// the client and the recorder as separate arguments.
func recordingClient(t *testing.T, handler http.Handler) (*apiclient.Client, *events.Recorder, *eventstest.Events) {
	t.Helper()
	reads, recorded := recordingReader(t, handler)
	return reads.client, reads.recorder, recorded
}

// absentFlux serves a cluster with no Flux controllers and no deploy
// key. Reads return 404, except the read of kustomizeControllerPath, which
// returns the status code probe. Creates return 201, except the create
// of the flux-system Namespace, which returns the status code
// namespace.
func absentFlux(probe, namespace int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == kustomizeControllerPath:
			w.WriteHeader(probe)
		case r.Method == http.MethodGet:
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces":
			w.WriteHeader(namespace)
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
		}
	})
}

func TestTheFluxFeaturePostsTheKeyAndTheControllers(t *testing.T) {
	objects, err := parseFluxManifest([]byte(testFluxManifest))
	if err != nil {
		t.Fatalf("parsing the test Flux manifest: %v", err)
	}
	minted := "Normal FluxDeployKeyMinted: Created the flux-system Secret with a new deploy key. Register the public key from status.flux.publicKey at the git forge"
	applied := func(created, present, failed int) string {
		return fmt.Sprintf("Normal FluxEngineSeeded: Applied the embedded Flux manifest, because the kustomize-controller Deployment was missing: "+
			"%d objects created, %d already present, %d not created", created, present, failed)
	}
	cases := []struct {
		name             string
		probe, namespace int
		want             []string
	}{
		{"a namespace that exists", http.StatusNotFound, http.StatusConflict,
			[]string{minted, applied(len(objects)-1, 1, 0)}},
		{"a namespace that fails", http.StatusNotFound, http.StatusInternalServerError,
			[]string{minted, applied(len(objects)-1, 0, 1)}},
		{"a probe that fails", http.StatusInternalServerError, http.StatusCreated,
			[]string{minted}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, recorder, recorded := recordingClient(t, absentFlux(c.probe, c.namespace))

				ensureFluxDeployKey(client, recorder, fluxCluster())
				ensureFluxControllers(client, recorder, fluxCluster(), []byte(testFluxManifest), &fluxProbe{}, passTime)

				if got := postedAbout(recorded, clusterKind, "lab"); !slices.Equal(got, c.want) {
					t.Errorf("the Events about Cluster lab are %q, want %q", got, c.want)
				}
			})
		})
	}
}
