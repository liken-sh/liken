package main

import (
	"os"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"unicode"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// rolledBackPass writes the status that a rollback leaves, with a
// condition from a newer release and cluster-operator's grant, runs the
// passes, and returns the Machine.
func rolledBackPass(t *testing.T) *machine.Machine {
	t.Helper()
	isolatePass(t)
	server := newPassAPI()
	client, _ := passClients(t, server)
	m, err := kubernetes.GetMachine(client, "node-1")
	if err != nil {
		t.Fatalf("reading Machine node-1: %v", err)
	}
	status := m.Status
	status.Conditions = []api.Condition{
		{Type: "FutureAttached", Status: api.ConditionFalse, Reason: "AdapterUnplugged"},
		{Type: machine.RebootApprovedCondition, Status: api.ConditionTrue, Reason: "DisruptionBudgetAllows"},
	}
	if _, err := kubernetes.PublishStatus(client, m, &status); err != nil {
		t.Fatalf("publishing the status of Machine node-1: %v", err)
	}
	_, after := runPasses(t, server, &reader{client: client})
	return after
}

func TestAPassDropsTheConditionsOfANewerRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		after := rolledBackPass(t)

		if c := api.FindCondition(after.Status.Conditions, "FutureAttached"); c != nil {
			t.Errorf("after the pass, the condition of a newer release is still there: %+v, want it dropped", c)
		}
		if c := api.FindCondition(after.Status.Conditions, machine.RebootApprovedCondition); c == nil {
			t.Error("the pass dropped the RebootApproved condition of cluster-operator, want it kept")
		}
	})
}

func TestEveryConditionAPassWritesIsOwned(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		after := rolledBackPass(t)

		for _, c := range after.Status.Conditions {
			if !ownsCondition(c.Type) {
				t.Errorf("the pass wrote the condition %s, which ownedConditionTypes does not list", c.Type)
			}
		}
	})
}

// conditionsDescription returns the description of status.conditions
// from the Machine CRD. The reference page is generated from it.
func conditionsDescription(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../machine/manifests/machines-crd.yaml")
	if err != nil {
		t.Fatalf("reading the Machine CRD: %v", err)
	}
	var crd struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPIV3Schema struct {
						Properties struct {
							Status struct {
								Properties struct {
									Conditions struct {
										Description string `json:"description"`
									} `json:"conditions"`
								} `json:"properties"`
							} `json:"status"`
						} `json:"properties"`
					} `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("parsing the Machine CRD: %v", err)
	}
	return crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Status.Properties.Conditions.Description
}

// The reference page is generated from the CRD, so the CRD's
// description must name every condition that a pass writes.
func TestTheCRDDescribesEveryCondition(t *testing.T) {
	described := strings.FieldsFunc(conditionsDescription(t), func(r rune) bool {
		return !unicode.IsLetter(r)
	})

	for conditionType := range ownedConditionTypes {
		if !slices.Contains(described, conditionType) {
			t.Errorf("the description of status.conditions does not mention %s, want every condition that a pass writes", conditionType)
		}
	}
}
