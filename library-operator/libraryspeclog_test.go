package main

// These tests read the line a Library leaves when a person edits its spec:
// once, on the pass that first acts on the new generation.

import (
	"testing"
)

func TestASpecEditLeavesOneLineAcrossPasses(t *testing.T) {
	cases := []struct {
		name     string
		observed int64
		want     int
	}{
		{name: "a generation the status has not observed", observed: 1, want: 1},
		{name: "a generation the status observed", observed: 2, want: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			library.Metadata.Generation = 2
			library.Status.Conditions = []Condition{{Type: conditionReady, ObservedGeneration: c.observed}}
			operator, logged := loggingOperator(t, cluster)

			operator.pass()
			operator.pass()

			found := linesWith(logged, "library house/movies: acting on spec generation 2, which the status has not observed")
			if len(found) != c.want {
				t.Errorf("lines = %q, want %d in:\n%s", found, c.want, logged)
			}
		})
	}
}
