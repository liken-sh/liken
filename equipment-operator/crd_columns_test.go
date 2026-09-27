package main

// The columns kubectl get prints for each resource. The default view
// answers whether the equipment is reachable and what it does now, and
// Age stays its last column. The wide view, kubectl get -o wide, adds
// the facts a person reads when a default column looks wrong.

import (
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// column is one printer column as a test states it: its name, its path,
// and its priority, where 1 is the wide view.
type column struct {
	name, path string
	priority   int32
}

func condition(kind string) string {
	return `.status.conditions[?(@.type=="` + kind + `")].status`
}

func conditionReason(kind string) string {
	return `.status.conditions[?(@.type=="` + kind + `")].reason`
}

const age = ".metadata.creationTimestamp"

func TestThePrinterColumns(t *testing.T) {
	cases := []struct {
		path string
		want []column
	}{
		{cecBusesCRD, []column{
			{"Mode", ".spec.mode", 0},
			{"Machines", ".spec.adapters[*].machine", 0},
			{"Joined", condition("Joined"), 0},
			{"Coherent", condition("Coherent"), 0},
			{"Age", age, 0},
			{"Scanned", condition("Scanned"), 1},
			{"State", ".status.adapters[*].state", 1},
			{"Devices", ".status.devices[*].osdName", 1},
		}},
		{televisionsCRD, []column{
			{"Bus", ".spec.cec.bus", 0},
			{"Power", ".status.power", 0},
			{"Display", ".status.activeDisplay", 0},
			{"Player", ".status.session.player", 0},
			{"Reachable", condition("Reachable"), 0},
			{"Age", age, 0},
			{"Source", ".status.activeSource", 1},
			{"Session", ".status.session.display", 1},
			{"Wake", conditionReason("WakeApplied"), 1},
			{"Applied", conditionReason("PowerApplied"), 1},
		}},
		{receiversCRD, []column{
			{"Power", ".status.zones.main.power", 0},
			{"Input", ".status.zones.main.input", 0},
			{"Volume", ".status.zones.main.volume", 0},
			{"Player", ".status.session.player", 0},
			{"Reachable", condition("Reachable"), 0},
			{"Age", age, 0},
			{"Driver", ".status.driver", 1},
			{"Address", ".status.address", 1},
			{"Playing", ".status.session.active", 1},
			{"Sound", ".status.zones.main.soundMode", 1},
		}},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			mustDeepEqual(t, columnsOf(loadCRDFrom(t, c.path).Spec.Versions[0].AdditionalPrinterColumns), c.want)
		})
	}
}

func columnsOf(printed []apiextensionsv1.CustomResourceColumnDefinition) []column {
	columns := make([]column, 0, len(printed))
	for _, one := range printed {
		columns = append(columns, column{one.Name, one.JSONPath, one.Priority})
	}
	return columns
}
