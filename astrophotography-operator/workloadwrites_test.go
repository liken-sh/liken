package main

// The operator's writes of a Solver's workload while the watches of the
// Deployments and the Services run behind the API server. A watch
// delivers each write some time after the API server answers it, and
// every pass that runs in that time reads a store that does not hold
// the write yet.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
)

// A pass that runs before the watches deliver the operator's own
// writes reads the Deployment and the Service from the API server. So
// it creates each one once, and replaces the Deployment once, from the
// API server's copy and not from the store's older one.
func TestAPassDoesNotActOnACopyOlderThanItsOwnWrite(t *testing.T) {
	edited := map[string]any{"indexFiles": map[string]any{"claimName": "indexes-2"}}
	cases := []struct {
		name string
		// before runs while the watches keep up, and during while they
		// lag.
		before, during func(w *operatorWorld)
	}{
		{"the create and the replace", func(*operatorWorld) {}, func(w *operatorWorld) {
			w.api.put(solversPath, solverObject("lab", nil))
			settle()
			w.api.put(solversPath, solverObject("lab", edited))
		}},
		{"the replace", func(w *operatorWorld) {
			w.api.put(solversPath, solverObject("lab", nil))
		}, func(w *operatorWorld) {
			w.api.put(solversPath, solverObject("lab", edited))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorkloads(t, nil)
				c.before(w)
				settle()
				w.api.lag(deploymentsPath)
				w.api.lag(servicesPath)
				c.during(w)
				settle()
				// A change elsewhere wakes one more pass while the
				// watches lag.
				w.api.put(targetsPath, object("observatory", "m42", nil, map[string]any{}))
				settle()
				w.api.catchUp(deploymentsPath)
				w.api.catchUp(servicesPath)
				settle()
				deployments := []string{
					"POST /apis/apps/v1/namespaces/observatory/deployments",
					"PUT /apis/apps/v1/namespaces/observatory/deployments/lab-solver",
				}
				if got := w.api.writes(deploymentsPath); !slices.Equal(got, deployments) {
					t.Errorf("Deployment writes %q, want %q", got, deployments)
				}
				services := []string{"POST /api/v1/namespaces/observatory/services"}
				if got := w.api.writes(servicesPath); !slices.Equal(got, services) {
					t.Errorf("Service writes %q, want %q", got, services)
				}
				if log := w.logs.String(); log != "" {
					t.Errorf("log %q, want no failed pass", log)
				}
			})
		})
	}
}

// A pass that must read the Deployment from the API server, because the
// store does not hold the operator's create yet, fails while the API
// server refuses the read, and creates nothing again. It runs again
// after the backoff, reads the Deployment, and writes nothing.
func TestARefusedReadOfTheOperatorsOwnWriteFailsThePass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		settle()
		w.api.lag(deploymentsPath)
		w.api.put(solversPath, solverObject("lab", nil))
		settle()
		w.api.refuseReads(true)
		w.api.put(targetsPath, object("observatory", "m42", nil, map[string]any{}))
		settle()
		failed := w.logs.String()
		w.api.refuseReads(false)
		time.Sleep(retryAfter)
		settle()
		w.api.catchUp(deploymentsPath)
		settle()
		want := []string{"POST /apis/apps/v1/namespaces/observatory/deployments"}
		if got := w.api.writes(deploymentsPath); !slices.Equal(got, want) {
			t.Errorf("Deployment writes %q, want %q", got, want)
		}
		if !strings.Contains(failed, "a pass of the Solver loop: Solver observatory/lab") {
			t.Errorf("log %q, want the failed pass", failed)
		}
		if ready := readyOf(w.solver(t, "lab")); ready.Message != "the solver's Deployment has no pod yet" {
			t.Errorf("Ready %s: %s", ready.Reason, ready.Message)
		}
	})
}

// writtenWorkload builds the Deployment and the Service that the
// operator writes for a Solver, as the API server stores them.
func writtenWorkload(t *testing.T, solverObject map[string]any) (map[string]any, map[string]any) {
	t.Helper()
	var s astrophotography.Solver
	asJSON(t, solverObject, &s)
	w := solverWorkload(&s, testWorkerImage)
	w.deployment.Metadata.Annotations = map[string]string{templateAnnotation: w.stamp()}
	var d, svc map[string]any
	asJSON(t, w.deployment, &d)
	asJSON(t, w.service, &svc)
	return d, svc
}

// asJSON decodes the JSON of one value into another.
func asJSON(t *testing.T, from, to any) {
	t.Helper()
	raw, err := json.Marshal(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, to); err != nil {
		t.Fatal(err)
	}
}

// A Deployment and a Service that the lead before this one created,
// and that the stores do not hold yet, answer the create with 409. The
// pass takes them as created. The next pass reads them from the API
// server, finds the same template, and writes nothing.
func TestAWorkloadThatTheLastLeadCreatedIsNotCreatedAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		settle()
		w.api.lag(deploymentsPath)
		w.api.lag(servicesPath)
		lab := solverObject("lab", nil)
		d, s := writtenWorkload(t, lab)
		w.api.put(deploymentsPath, d)
		w.api.put(servicesPath, s)
		w.api.put(solversPath, lab)
		settle()
		w.api.put(targetsPath, object("observatory", "m42", nil, map[string]any{}))
		settle()
		w.api.catchUp(deploymentsPath)
		w.api.catchUp(servicesPath)
		settle()
		cases := []struct {
			name      string
			got, want []string
		}{
			{"Deployment", w.api.writes(deploymentsPath), []string{"POST /apis/apps/v1/namespaces/observatory/deployments"}},
			{"Service", w.api.writes(servicesPath), []string{"POST /api/v1/namespaces/observatory/services"}},
		}
		for _, c := range cases {
			if !slices.Equal(c.got, c.want) {
				t.Errorf("%s writes %q, want %q", c.name, c.got, c.want)
			}
		}
		if log := w.logs.String(); log != "" {
			t.Errorf("log %q, want no failed pass", log)
		}
	})
}
