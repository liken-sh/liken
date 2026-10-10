package cluster

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"testing"
	"time"

	"sigs.k8s.io/yaml"
)

// printerColumn is one column of `kubectl get clusters`.
type printerColumn struct {
	Name     string `json:"name"`
	JSONPath string `json:"jsonPath"`
	Priority int    `json:"priority"`
}

// ledgerSchema is the part of the Cluster CRD that declares the grant
// ledger and the printer column that shows it.
type ledgerSchema struct {
	Spec struct {
		Versions []struct {
			Columns []printerColumn `json:"additionalPrinterColumns"`
			Schema  struct {
				OpenAPIV3Schema struct {
					Properties struct {
						Status struct {
							Properties struct {
								RebootTurns struct {
									Type     string   `json:"type"`
									MaxItems int      `json:"maxItems"`
									ListType string   `json:"x-kubernetes-list-type"`
									MapKeys  []string `json:"x-kubernetes-list-map-keys"`
									Items    struct {
										Required   []string `json:"required"`
										Properties map[string]struct {
											Type   string `json:"type"`
											Format string `json:"format"`
										} `json:"properties"`
									} `json:"items"`
								} `json:"rebootTurns"`
							} `json:"properties"`
						} `json:"status"`
					} `json:"properties"`
				} `json:"openAPIV3Schema"`
			} `json:"schema"`
		} `json:"versions"`
	} `json:"spec"`
}

func readLedgerSchema(t *testing.T) ledgerSchema {
	t.Helper()
	raw, err := os.ReadFile("manifests/clusters-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd ledgerSchema
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	return crd
}

// The ledger is a list keyed by machine, so a machine appears once,
// and each entry requires every field the cluster operator writes.
func TestTheClusterCRDDeclaresTheLedger(t *testing.T) {
	turns := readLedgerSchema(t).Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Status.Properties.RebootTurns

	if turns.Type != "array" || turns.ListType != "map" || !slices.Equal(turns.MapKeys, []string{"machine"}) || turns.MaxItems != 1024 {
		t.Errorf("rebootTurns = %+v, want an array of at most 1024, listed as a map keyed by machine", turns)
	}
	if want := []string{"machine", "machineResourceVersion", "since"}; !slices.Equal(turns.Items.Required, want) {
		t.Errorf("required = %q, want %q", turns.Items.Required, want)
	}
	if since := turns.Items.Properties["since"]; since.Type != "string" || since.Format != "date-time" {
		t.Errorf("since = %+v, want a date-time string", since)
	}
	for _, field := range []string{"machine", "machineResourceVersion"} {
		if got := turns.Items.Properties[field].Type; got != "string" {
			t.Errorf("%s has type %q, want string", field, got)
		}
	}
}

// The Go type writes exactly the fields that the schema declares. A
// field the schema does not declare would be pruned, and the ledger
// would never prove itself stored.
func TestARebootTurnWritesTheDeclaredFields(t *testing.T) {
	declared := readLedgerSchema(t).Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Status.Properties.RebootTurns.Items.Properties
	raw, err := json.Marshal(RebootTurn{Machine: "node-4", MachineResourceVersion: "184467", Since: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	var written map[string]any
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatal(err)
	}
	if got, want := slices.Sorted(maps.Keys(written)), slices.Sorted(maps.Keys(declared)); !slices.Equal(got, want) {
		t.Errorf("a RebootTurn writes %q, and the schema declares %q", got, want)
	}
}

// The Turns column shows the machine names, and only in -o wide.
func TestTheTurnsColumnShowsInTheWideListing(t *testing.T) {
	columns := readLedgerSchema(t).Spec.Versions[0].Columns
	i := slices.IndexFunc(columns, func(c printerColumn) bool { return c.Name == "Turns" })
	if i < 0 || columns[i].JSONPath != ".status.rebootTurns[*].machine" || columns[i].Priority != 1 {
		t.Errorf("columns = %+v, want Turns on .status.rebootTurns[*].machine at priority 1", columns)
	}
}
