package solver

// A rollout runs an old solver beside a new operator for a while, so
// these tests fix the wire form of each answer.

import (
	"encoding/json"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
)

func TestTheWireFormOfEachAnswer(t *testing.T) {
	cases := []struct {
		name   string
		answer any
		want   string
	}{
		// The probe of plan 02 solved a 5 s frame of the CCD simulator at
		// these values.
		{"a solved field",
			SolveAnswer{Solved: true, Center: &sky.Position{RA: 83.4883, Dec: -5.4123}, Scale: 2.233, Rotation: 180, Parity: ParityNegative, Index: 4207},
			`{"solved":true,"center":{"ra":83.4883,"dec":-5.4123},"scale":2.233,"rotation":180,"parity":"negative","index":4207}`},
		{"a field that does not solve", SolveAnswer{}, `{"solved":false}`},
		{"the lab's index files",
			Indexes{
				{ID: 4207, Files: 12, ScaleArcmin: astrophotography.ScaleRange{Low: 22, High: 30}},
				{ID: 4208, Files: 1, ScaleArcmin: astrophotography.ScaleRange{Low: 30, High: 42}},
			},
			`[{"id":4207,"files":12,"scaleArcmin":{"low":22,"high":30}},{"id":4208,"files":1,"scaleArcmin":{"low":30,"high":42}}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := json.Marshal(c.answer)
			if err != nil || string(raw) != c.want {
				t.Errorf("the answer is\n%s, %v\nwant\n%s", raw, err, c.want)
			}
		})
	}
}
