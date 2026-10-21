package main

// The machine's Disruptions: the request this operator keeps for its
// staged documents, and a person's request to reboot the running boot.

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
	"github.com/liken-sh/liken/liken/machine"
)

var (
	stagedSpec = machine.PendingDisruption{Condition: "SpecConverged", Kind: machine.DisruptionReboot,
		Hash: "9d3b2a7c11e4", Summary: "the machine spec: modules"}
	stagedCluster = machine.PendingDisruption{Condition: "ClusterConverged", Kind: machine.DisruptionRestart,
		Hash: "51f0e2d8aa90", Summary: "the cluster document: k3s flags"}
	personsReboot = machine.PendingDisruption{Condition: rebootRequestCondition, Kind: machine.DisruptionReboot,
		Hash: "0c1e9a2b77b0", Summary: "a reboot of this machine, with no change to apply"}
	noDemotion = api.Condition{Type: "NodeCurrent", Status: api.ConditionTrue, Reason: "NodeMatchesRole"}
)

// The operator's own request covers every staged document and a
// demotion, takes a reboot when any of them needs one, and leaves a
// person's reboot to the person's own request.
func TestTheOwnRequestCoversWhatWaits(t *testing.T) {
	cases := []struct {
		name     string
		pending  []machine.PendingDisruption
		demotion api.Condition
		kind     disruption.Kind
		reason   string
	}{
		{"a restart alone", []machine.PendingDisruption{stagedCluster}, noDemotion,
			disruption.KindRestart, "the cluster document: k3s flags"},
		{"a reboot covers the restart", []machine.PendingDisruption{stagedSpec, stagedCluster}, noDemotion,
			disruption.KindReboot, "the machine spec: modules; the cluster document: k3s flags"},
		{"a person's reboot is left out", []machine.PendingDisruption{stagedCluster, personsReboot}, noDemotion,
			disruption.KindRestart, "the cluster document: k3s flags"},
		{"a demotion", nil, api.Condition{Type: "NodeCurrent", Reason: "AwaitingTurn"},
			disruption.KindReboot, "the demotion of this machine to follower"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := ownRequest("node-4", c.pending, c.demotion)

			if spec == nil || spec.Kind != c.kind || spec.Reason != c.reason || spec.Requester != disruption.MachineOperator {
				t.Errorf("own request = %+v, want a %s for %q", spec, c.kind, c.reason)
			}
		})
	}
}

func TestNothingWaitingMeansNoOwnRequest(t *testing.T) {
	if spec := ownRequest("node-4", []machine.PendingDisruption{personsReboot}, noDemotion); spec != nil {
		t.Errorf("own request = %+v, want none", spec)
	}
}

// The request's hash names what it applies, so a change to any staged
// document changes it.
func TestTheOwnRequestsHashFollowsTheStagedDocuments(t *testing.T) {
	before := ownRequest("node-4", []machine.PendingDisruption{stagedSpec, stagedCluster}, noDemotion)
	reordered := ownRequest("node-4", []machine.PendingDisruption{stagedCluster, stagedSpec}, noDemotion)
	edited := stagedSpec
	edited.Hash = "77aa0b1c2d3e"
	after := ownRequest("node-4", []machine.PendingDisruption{edited, stagedCluster}, noDemotion)

	if before.Hash != reordered.Hash || before.Hash == after.Hash {
		t.Errorf("hashes %.12s, %.12s, %.12s; want the first two equal and the third different",
			before.Hash, reordered.Hash, after.Hash)
	}
}

// requestsAPI serves the Disruptions collection.
func requestsAPI(items ...disruption.Disruption) *fakeapi.Server {
	var objects []map[string]any
	for _, d := range items {
		raw, _ := json.Marshal(d)
		var object map[string]any
		_ = json.Unmarshal(raw, &object)
		objects = append(objects, object)
	}
	return fakeapi.New(map[string]*fakeapi.Collection{
		"/apis/liken.sh/v1alpha1/disruptions": {APIVersion: api.APIVersion, Kind: "Disruption", Items: objects},
	})
}

func storedRequests(t *testing.T, c *apiclient.Client) []disruption.Disruption {
	t.Helper()
	r := &reader{client: c}
	held, err := r.requests("node-4")
	if err != nil {
		t.Fatal(err)
	}
	return held
}

func requestingNode4() *machine.Machine {
	return &machine.Machine{Metadata: api.ObjectMeta{Name: "node-4", UID: "uid-node-4"}}
}

// The operator creates its request, updates it when more is staged,
// keeps a person's approval through the update, and deletes it when
// nothing waits.
func TestTheOwnRequestFollowsWhatWaits(t *testing.T) {
	c := testClient(t, requestsAPI())
	r := &reader{client: c}
	m := requestingNode4()

	syncOwnRequest(r, m, nil, ownRequest("node-4", []machine.PendingDisruption{stagedCluster}, noDemotion))
	created := storedRequests(t, c)
	if len(created) != 1 || created[0].Metadata.Name != "node-4.machine-operator" || created[0].Spec.Kind != disruption.KindRestart {
		t.Fatalf("after the create: %+v", created)
	}
	if owner := created[0].Metadata.OwnerReferences; len(owner) != 1 || owner[0].Kind != "Machine" || owner[0].UID != "uid-node-4" {
		t.Errorf("the request's owners are %+v, want the Machine", owner)
	}

	approved := created[0]
	approved.Spec.ApprovedHash = "aaaaaaaaaaaa"
	syncOwnRequest(r, m, []disruption.Disruption{approved},
		ownRequest("node-4", []machine.PendingDisruption{stagedSpec, stagedCluster}, noDemotion))
	updated := storedRequests(t, c)
	if len(updated) != 1 || updated[0].Spec.Kind != disruption.KindReboot || updated[0].Spec.ApprovedHash != "aaaaaaaaaaaa" {
		t.Fatalf("after the update: %+v", updated)
	}

	syncOwnRequest(r, m, updated, nil)
	if left := storedRequests(t, c); len(left) != 0 {
		t.Errorf("after nothing waits: %+v", left)
	}
}

// A cluster that does not serve the kind reads as no requests.
func TestAClusterWithoutDisruptionsReadsAsNone(t *testing.T) {
	c := testClient(t, fakeapi.New(map[string]*fakeapi.Collection{}))

	if held := storedRequests(t, c); held != nil {
		t.Errorf("requests = %+v, want none", held)
	}
}

// personRequest is a person's request to reboot node-4.
func personRequest(hash, approvedHash string) disruption.Disruption {
	return *disruption.New(disruption.RebootRequestName("node-4", hash), requestingNode4(), disruption.Spec{
		Machine: "node-4", Requester: "kubernetes-admin", Kind: disruption.KindReboot, Hash: hash, ApprovedHash: approvedHash})
}

// A person's request for the running boot is a reboot request, and its
// approvedHash approves it on a Manual machine.
func TestAPersonsRequestAsksForAReboot(t *testing.T) {
	boot := machine.BootID(machine.BootStatus{Time: &requestBootTime})
	cases := []struct {
		name    string
		policy  machine.RebootPolicy
		request disruption.Disruption
		reason  string
	}{
		{"Auto", machine.RebootAuto, personRequest(boot, ""), "RebootRequested"},
		{"Manual, unapproved", machine.RebootManual, personRequest(boot, ""), "RebootPending"},
		{"Manual, approved", machine.RebootManual, personRequest(boot, boot[:12]), "RebootRequested"},
		{"an earlier boot", machine.RebootAuto, personRequest("51f0e2d8aa90", ""), "NothingRequested"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := requestingMachine(c.policy, nil)

			conv := decideRebootRequest(m, requestFacts(requestBootTime), turnGranted, []disruption.Disruption{c.request})

			if conv.condition.Reason != c.reason {
				t.Errorf("reason = %s (%s), want %s", conv.condition.Reason, conv.condition.Message, c.reason)
			}
		})
	}
}

// A person's request for an earlier boot is marked Complete, and a
// request that names no boot gets the running one and the Machine as
// its owner.
func TestThePeoplesRequestsFollowTheBoot(t *testing.T) {
	boot := machine.BootID(machine.BootStatus{Time: &requestBootTime})
	spent := personRequest("51f0e2d8aa90aa", "")
	unnamed := personRequest("", "")
	unnamed.Metadata.Name = "node-4.reboot-now"
	unnamed.Metadata.OwnerReferences = nil
	c := testClient(t, requestsAPI(spent, unnamed))
	r := &reader{client: c}

	settlePeopleRequests(r, requestingNode4(), storedRequests(t, c), boot, requestBootTime)

	held := storedRequests(t, c)
	byName := func(name string) disruption.Disruption {
		return held[slices.IndexFunc(held, func(d disruption.Disruption) bool { return d.Metadata.Name == name })]
	}
	if d := byName(spent.Metadata.Name); !d.Complete() {
		t.Errorf("the request for an earlier boot is not complete: %+v", d.Status)
	}
	if d := byName("node-4.reboot-now"); d.Spec.Hash != boot || d.Complete() || len(d.Metadata.OwnerReferences) != 1 {
		t.Errorf("the request that named no boot = %+v, owners %+v; want the running boot, the Machine, and not complete",
			d.Spec, d.Metadata.OwnerReferences)
	}
}

// The own request's approvedHash approves every staged change it
// covers. It counts only while the request covers what the last pass
// found waiting, and the approve annotation approves nothing.
func TestTheOwnRequestsApprovalApprovesWhatItCovers(t *testing.T) {
	waiting := []machine.PendingDisruption{stagedCluster, stagedSpec}
	covering := ownRequest("node-4", waiting, noDemotion)
	request := func(hash, approved string) []disruption.Disruption {
		d := disruption.New("node-4.machine-operator", requestingNode4(), *covering)
		d.Spec.Hash, d.Spec.ApprovedHash = hash, approved
		return []disruption.Disruption{*d}
	}
	cases := []struct {
		name       string
		requests   []disruption.Disruption
		annotation string
		approved   []string
	}{
		{"no request", nil, "", nil},
		{"a request with no approval", request(covering.Hash, ""), "", nil},
		{"an approved request", request(covering.Hash, covering.Hash), "", []string{stagedCluster.Hash, stagedSpec.Hash}},
		{"an approval of other bytes", request("77aa0b1c2d3e4f", "77aa0b1c2d3e4f"), "", nil},
		{"the approve annotation alone", request(covering.Hash, ""), stagedSpec.Hash, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := requestingNode4()
			m.Status.Pending = waiting
			if c.annotation != "" {
				m.Metadata.Annotations = map[string]string{"liken.sh/approve-disruption": c.annotation}
			}

			decided := withRequestApprovals(m, c.requests)

			approval := decided.Metadata.Annotations[requestApprovalKey]
			for _, hash := range []string{stagedCluster.Hash, stagedSpec.Hash} {
				if got, want := approvedByRequest(approval, hash), slices.Contains(c.approved, hash); got != want {
					t.Errorf("%s approved = %v, want %v", hash, got, want)
				}
			}
			if _, ok := m.Metadata.Annotations[requestApprovalKey]; ok {
				t.Error("the Machine itself changed")
			}
		})
	}
}
