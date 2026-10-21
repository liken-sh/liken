package disruption

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

func TestASelectorReadsTheMachinesLabels(t *testing.T) {
	gpu := map[string]string{"pool": "gpu", "zone": "attic"}
	cases := []struct {
		name     string
		selector Selector
		labels   map[string]string
		selects  bool
	}{
		{"an empty selector selects every machine", Selector{}, nil, true},
		{"matchLabels", Selector{MatchLabels: map[string]string{"pool": "gpu"}}, gpu, true},
		{"matchLabels with another value", Selector{MatchLabels: map[string]string{"pool": "cpu"}}, gpu, false},
		{"In", Selector{MatchExpressions: []Requirement{{Key: "zone", Operator: "In", Values: []string{"attic", "den"}}}}, gpu, true},
		{"NotIn", Selector{MatchExpressions: []Requirement{{Key: "zone", Operator: "NotIn", Values: []string{"attic"}}}}, gpu, false},
		{"NotIn on a missing label", Selector{MatchExpressions: []Requirement{{Key: "rack", Operator: "NotIn", Values: []string{"a"}}}}, gpu, true},
		{"Exists", Selector{MatchExpressions: []Requirement{{Key: "pool", Operator: "Exists"}}}, gpu, true},
		{"DoesNotExist", Selector{MatchExpressions: []Requirement{{Key: "pool", Operator: "DoesNotExist"}}}, gpu, false},
		{"an operator the code does not know", Selector{MatchExpressions: []Requirement{{Key: "pool", Operator: "Gt"}}}, gpu, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.selector.Selects(c.labels); got != c.selects {
				t.Errorf("Selects = %v, want %v", got, c.selects)
			}
		})
	}
}

func TestApprovalFollowsThePolicyAndTheHash(t *testing.T) {
	cases := []struct {
		name     string
		policy   machine.RebootPolicy
		hash     string
		approved string
		want     bool
	}{
		{"Auto approves", machine.RebootAuto, "6f2a91c04b7e5d", "", true},
		{"Manual waits", machine.RebootManual, "6f2a91c04b7e5d", "", false},
		{"the full hash", machine.RebootManual, "6f2a91c04b7e5d", "6f2a91c04b7e5d", true},
		{"the short hash", machine.RebootManual, "6f2a91c04b7e5d", "6f2a91c04b7e", true},
		{"a prefix too short to trust", machine.RebootManual, "6f2a91c04b7e5d", "6f2a91", false},
		{"another hash", machine.RebootManual, "6f2a91c04b7e5d", "0c1e9a2b77b0", false},
		{"a request with no hash", machine.RebootManual, "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Disruption{Spec: Spec{Hash: c.hash, ApprovedHash: c.approved}}
			if got := d.Approved(c.policy); got != c.want {
				t.Errorf("Approved = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAPersonsRebootIsSpentWhenTheMachineBootsAgain(t *testing.T) {
	booted := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	running := machine.BootID(machine.BootStatus{Time: &booted})
	cases := []struct {
		name      string
		requester string
		kind      Kind
		hash      string
		spent     bool
	}{
		{"the running boot", "kubernetes-admin", KindReboot, running, false},
		{"the running boot, short", "kubernetes-admin", KindReboot, running[:12], false},
		{"an earlier boot", "kubernetes-admin", KindReboot, "0c1e9a2b77b0aa", true},
		{"machine-operator's own request", MachineOperator, KindReboot, "0c1e9a2b77b0aa", false},
		{"a restart", MachineOperator, KindRestart, "0c1e9a2b77b0aa", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Disruption{Spec: Spec{Requester: c.requester, Kind: c.kind, Hash: c.hash}}
			if got := d.Spent(running); got != c.spent {
				t.Errorf("Spent = %v, want %v", got, c.spent)
			}
		})
	}
}

func TestACompleteRequestIsDone(t *testing.T) {
	d := Disruption{Spec: Spec{Requester: "kubernetes-admin", Kind: KindReboot, Hash: "0c1e9a2b77b0aa"}}
	d.Status.Conditions = api.SetCondition(nil, api.Condition{Type: CompleteCondition, Status: api.ConditionTrue, Reason: "BootChanged"}, time.Now())

	if !d.Done("") {
		t.Error("a complete request is not done")
	}
}

// A Disruption names its Machine as its owner, so the garbage
// collector deletes it with the Machine, and its metadata encodes in
// the shape the API server reads.
func TestARequestIsOwnedByItsMachine(t *testing.T) {
	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-4", UID: "uid-node-4"}}
	raw, err := json.Marshal(New(OwnName("node-4"), m, Spec{Machine: "node-4"}))
	if err != nil {
		t.Fatal(err)
	}
	var encoded struct {
		Metadata struct {
			Name            string `json:"name"`
			OwnerReferences []struct {
				Kind string `json:"kind"`
				UID  string `json:"uid"`
			} `json:"ownerReferences"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatal(err)
	}
	if encoded.Metadata.Name != "node-4.machine-operator" || len(encoded.Metadata.OwnerReferences) != 1 ||
		encoded.Metadata.OwnerReferences[0].UID != "uid-node-4" {
		t.Errorf("metadata = %+v", encoded.Metadata)
	}
}

func TestARebootRequestIsNamedForItsBoot(t *testing.T) {
	if got := RebootRequestName("node-4", "0c1e9a2b77b0aaff"); got != "node-4.reboot-0c1e9a2b77b0" {
		t.Errorf("name = %s", got)
	}
}
