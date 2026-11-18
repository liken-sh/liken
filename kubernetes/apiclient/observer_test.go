package apiclient

import (
	"context"
	"net/http"
	"testing"
	"testing/synctest"
)

// An observer receives one Outcome for each request, with the HTTP
// status of the response and the error the caller gets.
func TestAnObserverReceivesTheOutcomeOfEachRequest(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantStatus int
		wantErr    bool
	}{
		{"a 200", http.StatusOK, http.StatusOK, false},
		{"a 404", http.StatusNotFound, http.StatusNotFound, true},
		{"a 422", http.StatusUnprocessableEntity, http.StatusUnprocessableEntity, true},
		{"a 503", http.StatusServiceUnavailable, http.StatusServiceUnavailable, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(`{}`))
			}))
			var outcomes []Outcome
			observed := client.WithObserver(func(o Outcome) { outcomes = append(outcomes, o) })

			err := observed.RequestJSON(http.MethodPut, "/api/v1/nodes/node-1", []byte(`{}`), nil)

			want := []Outcome{{Method: http.MethodPut, Path: "/api/v1/nodes/node-1", Status: c.wantStatus, Err: err}}
			if len(outcomes) != 1 || outcomes[0] != want[0] || (err != nil) != c.wantErr {
				t.Errorf("outcomes %+v with err %v, want %+v", outcomes, err, want)
			}
		})
	}
}

// A client whose context has ended sends nothing, so the Outcome has
// status zero.
func TestTheOutcomeHasNoStatusWhenNoResponseCame(t *testing.T) {
	client, _ := testClient(t, http.NotFoundHandler())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var outcomes []Outcome
	observed := client.WithContext(ctx).WithObserver(func(o Outcome) { outcomes = append(outcomes, o) })

	err := observed.RequestJSON(http.MethodGet, "/api/v1/nodes/node-1", nil, nil)

	if len(outcomes) != 1 || outcomes[0].Status != 0 || outcomes[0].Err == nil || err == nil {
		t.Errorf("outcomes %+v with err %v, want one outcome with no status and the error", outcomes, err)
	}
}

// A 429 that the client waits out and resends is one request, so the
// observer receives one Outcome, for the last response.
func TestAnObserverReceivesOneOutcomeForARequestThat429Delayed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, _ := testClient(t, &throttling{refusals: 1, retryAfter: "1"})
		var outcomes []Outcome
		observed := client.WithObserver(func(o Outcome) { outcomes = append(outcomes, o) })

		err := observed.RequestJSON(http.MethodGet, "/api/v1/nodes/node-1", nil, nil)

		if len(outcomes) != 1 || outcomes[0].Status != http.StatusOK || err != nil {
			t.Errorf("outcomes %+v with err %v, want one 200", outcomes, err)
		}
	})
}
