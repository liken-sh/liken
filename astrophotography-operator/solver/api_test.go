package solver

// During a rollout, a new operator can send requests to an old solver,
// so the JSON of each response must not change.

import (
	"encoding/json"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
)

func TestTheJSONOfEachResponse(t *testing.T) {
	cases := []struct {
		name     string
		response any
		want     string
	}{
		// The solution of a frame from the INDI CCD simulator.
		{"a solved field",
			SolveResponse{Solved: true, Center: &sky.Position{RA: 83.4883, Dec: -5.4123}, Scale: 2.233, Rotation: 180, Parity: ParityNegative, Index: 4207},
			`{"solved":true,"center":{"ra":83.4883,"dec":-5.4123},"scale":2.233,"rotation":180,"parity":"negative","index":4207}`},
		{"a field that does not solve", SolveResponse{}, `{"solved":false}`},
		{"the lab's index files",
			Indexes{
				{ID: 4207, Files: 12, ScaleArcmin: astrophotography.ScaleRange{Low: 22, High: 30}},
				{ID: 4208, Files: 1, ScaleArcmin: astrophotography.ScaleRange{Low: 30, High: 42}},
			},
			`[{"id":4207,"files":12,"scaleArcmin":{"low":22,"high":30}},{"id":4208,"files":1,"scaleArcmin":{"low":30,"high":42}}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := json.Marshal(c.response)
			if err != nil || string(raw) != c.want {
				t.Errorf("marshaling the response gives %s with error %v, want %s", raw, err, c.want)
			}
		})
	}
}
