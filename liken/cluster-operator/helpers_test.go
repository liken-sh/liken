package main

// This file has the test helpers that several test files share: a
// metrics scrape, a client for a fake API server, and builders for
// reboot requests. apiservertest serves the fake API server over
// in-memory connections, so tests in a synctest bubble can use it.

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
)

// fleetMetrics creates an operator metrics registry with the cluster
// metrics on it. It returns the clusterMetrics that a pass publishes
// into and a function that returns the registry's current text
// exposition.
func fleetMetrics(t *testing.T) (*clusterMetrics, func() string) {
	t.Helper()
	o := metrics.NewOperator(component, machine.Version, []string{clusterKind}, []string{machineKind})
	layer := newClusterMetrics(o)

	return layer, func() string { return scrapeHandler(t, o.Handler()) }
}

// scrapeHandler sends one GET to /metrics on handler and returns the
// response body.
func scrapeHandler(t *testing.T, handler http.Handler) string {
	t.Helper()
	server := apiservertest.Start(t, handler)

	resp, err := server.Client().Get(apiservertest.Host + "/metrics")
	if err != nil {
		t.Fatalf("requesting /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the /metrics response: %v", err)
	}
	return string(body)
}

// testClient returns an API client for a fake server that runs
// handler. The client reads its token from a temporary credentials
// directory, as it does from the service account volume in a pod.
func testClient(t *testing.T, handler http.Handler) *apiclient.Client {
	t.Helper()
	server := apiservertest.Start(t, handler)

	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatalf("writing the token file: %v", err)
	}
	return apiclient.New(apiservertest.Host, server.Client(), credentials)
}

// requestsOfAwaitingMachines returns one approved reboot request for each machine that
// has a condition with the reason AwaitingTurn. Machine-operator
// creates such a request when it has staged an update.
func requestsOfAwaitingMachines(machines []machine.Machine) fleetRequests {
	var q fleetRequests
	for i := range machines {
		if !slices.ContainsFunc(machines[i].Status.Conditions, func(c api.Condition) bool { return c.Reason == "AwaitingTurn" }) {
			continue
		}
		q.disruptions = append(q.disruptions, approvedRequest(machines[i].Metadata.Name))
	}
	return q
}

// approvedRequest returns a reboot request from machine-operator for
// the named machine. Its spec.approvedHash equals its spec.hash, so it
// is approved whatever the machine's rebootPolicy.
func approvedRequest(name string) disruption.Disruption {
	d := disruption.New(disruption.OwnName(name), &machine.Machine{Metadata: api.ObjectMeta{Name: name, UID: "uid-" + name}},
		disruption.Spec{Machine: name, Requester: disruption.MachineOperator, Kind: disruption.KindReboot,
			Hash: "6f2a91c04b7e5d", ApprovedHash: "6f2a91c04b7e5d"})
	return *d
}

// decideFor runs decideRollout with the requests from requestsOfAwaitingMachines and a
// default budget set to the maxUnavailable limit in the Cluster's
// spec.disruption.
func decideFor(machines []machine.Machine, heartbeats map[string]time.Time, trust heartbeatTrust,
	c *cluster.Cluster, appliedVersion string, now time.Time) rollout {
	q := requestsOfAwaitingMachines(machines)
	q.budgets = []disruption.Budget{*disruption.NewDefaultBudget(c.Spec.Disruption.MaxUnavailableOrDefault())}
	return decideRollout(machines, heartbeats, trust, c, appliedVersion, q, now)
}
