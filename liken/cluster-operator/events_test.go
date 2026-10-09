package main

// These tests read the Events the cluster operator posts about the
// Machines and the Cluster from eventstest's fake of the events
// collection, in a synctest bubble, so the recorder's goroutine runs on
// the fake clock.

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

// recordingReader serves the events fake in front of handler, and
// answers a fleet reader whose recorder writes through it.
func recordingReader(t *testing.T, handler http.Handler) (*fleetReader, *eventstest.Events) {
	t.Helper()
	recorded := &eventstest.Events{}
	client := testClient(t, recorded.Around(handler))
	recorder := events.New(t.Context(), client, component, events.Options{Instance: "liken-cluster-operator-0", Log: io.Discard})
	return &fleetReader{client: client, recorder: recorder}, recorded
}

// postedAbout answers each Event about one object as
// "Type Reason: message", in the order the fake received them.
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

func TestASweepPostsTheLostMachineAndTheClustersTransitions(t *testing.T) {
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
				"node-1": sweepNow.Add(-10 * time.Second),
				"node-2": time.Date(2026, 7, 6, 11, 55, 0, 0, time.UTC),
			},
		}
		reads, recorded := recordingReader(t, fake.handler())
		fake.sawEachRenewal(reads)
		cm, _ := fleetMetrics(t)

		if err := sweepFleet(reads, clusterDoc, "", &engineProbe{}, &podSteward{}, cm, sweepNow); err != nil {
			t.Fatal(err)
		}

		cases := []struct {
			kind, name string
			want       []string
		}{
			{machineKind, "node-1", nil},
			{machineKind, "node-2", []string{"Warning MachineLost: the heartbeat lease was last renewed at 2026-07-06T11:55:00Z; marked Lost"}},
			{machineKind, "node-3", []string{"Warning MachineLost: the machine has never renewed a heartbeat lease; marked Lost"}},
			{clusterKind, "lab", []string{
				"Warning MachinesDegraded: 1/3 machines ready; unwell: node-2, node-3",
				"Normal RolloutComplete: no machines are waiting for a reboot turn",
			}},
		}
		for _, c := range cases {
			if got := postedAbout(recorded, c.kind, c.name); !slices.Equal(got, c.want) {
				t.Errorf("%s %s: got %q, want %q", c.kind, c.name, got, c.want)
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

		markLost(reads, []machine.Machine{labMachine("node-1", api.PhaseReady)}, []string{"node-1"}, nil, sweepNow)
		s := decideFleetSweep(nil, nil, sweepNow)
		publishClusterStatus(reads, clusterDoc, s, rollout{}, nil, "", "", sweepNow)

		synctest.Wait()
		if got := recorded.List(); len(got) != 0 {
			t.Errorf("refused writes posted %d Events", len(got))
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
		}, sweepNow.Add(-time.Minute))
		machines := []machine.Machine{labMachine("node-3", api.PhaseUpdatePending), granted}

		carryOutRollout(reads, machines, rollout{grant: []string{"node-3"}, revoke: []string{"node-4"}}, sweepNow)

		cases := []struct {
			name string
			want []string
		}{
			{"node-3", []string{"Normal RebootTurnGranted: the cluster's disruption budget allows this machine to take its reboot turn now"}},
			{"node-4", []string{"Normal RebootTurnReclaimed: the machine is available and asks for no reboot, so its turn returns to the disruption budget"}},
		}
		for _, c := range cases {
			if got := postedAbout(recorded, machineKind, c.name); !slices.Equal(got, c.want) {
				t.Errorf("%s: got %q, want %q", c.name, got, c.want)
			}
		}
	})
}

// recordingClient serves the events fake in front of handler, for the
// flux tests that take a client and a recorder apart.
func recordingClient(t *testing.T, handler http.Handler) (*apiclient.Client, *events.Recorder, *eventstest.Events) {
	t.Helper()
	reads, recorded := recordingReader(t, handler)
	return reads.client, reads.recorder, recorded
}

// absentFlux answers the reads of a cluster with no flux engine and
// no deploy key: 404, or probe for the engine's read. Each create
// lands, except that the namespace answers namespace.
func absentFlux(probe, namespace int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == engineProbePath:
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

func TestTheFluxFeaturePostsTheKeyAndTheEngine(t *testing.T) {
	objects, err := parseSeed([]byte(testSeed))
	if err != nil {
		t.Fatal(err)
	}
	minted := "Normal FluxDeployKeyMinted: minted the flux deploy key; register the public half from status.flux.publicKey at the forge"
	seeded := func(created, present, failed int) string {
		return fmt.Sprintf("Normal FluxEngineSeeded: the flux engine was absent; planted its seed: "+
			"%d objects created, %d present already, %d failed", created, present, failed)
	}
	cases := []struct {
		name             string
		probe, namespace int
		want             []string
	}{
		{"a namespace that exists", http.StatusNotFound, http.StatusConflict,
			[]string{minted, seeded(len(objects)-1, 1, 0)}},
		{"a namespace that fails", http.StatusNotFound, http.StatusInternalServerError,
			[]string{minted, seeded(len(objects)-1, 0, 1)}},
		{"a probe that fails", http.StatusInternalServerError, http.StatusCreated,
			[]string{minted}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, recorder, recorded := recordingClient(t, absentFlux(c.probe, c.namespace))

				ensureFluxDeployKey(client, recorder, fluxCluster())
				ensureFluxEngine(client, recorder, fluxCluster(), []byte(testSeed), &engineProbe{}, sweepNow)

				if got := postedAbout(recorded, clusterKind, "lab"); !slices.Equal(got, c.want) {
					t.Errorf("got %q, want %q", got, c.want)
				}
			})
		})
	}
}
