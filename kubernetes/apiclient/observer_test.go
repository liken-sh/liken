package apiclient

// The observer: one call for each request, with the final answer.

import (
	"context"
	"net/http"
	"testing"
	"testing/synctest"
)

// An observer hears the final answer to each request: the status the
// API server sent, or no status when no answer came, with the error the
// caller gets. A 429 that the client waits out and sends again is one
// request, so the observer hears its last answer once.
func TestAnObserverHearsEachRequestsFinalAnswer(t *testing.T) {
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
			var heard []Outcome
			observed := client.WithObserver(func(o Outcome) { heard = append(heard, o) })

			err := observed.RequestJSON(http.MethodPut, "/api/v1/nodes/node-1", []byte(`{}`), nil)

			want := []Outcome{{Method: http.MethodPut, Path: "/api/v1/nodes/node-1", Status: c.wantStatus, Err: err}}
			if len(heard) != 1 || heard[0] != want[0] || (err != nil) != c.wantErr {
				t.Errorf("heard %+v with err %v, want %+v", heard, err, want)
			}
		})
	}
}

func TestAnObserverHearsNoStatusWhenNoAnswerCame(t *testing.T) {
	client, _ := testClient(t, http.NotFoundHandler())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var heard []Outcome
	observed := client.WithContext(ctx).WithObserver(func(o Outcome) { heard = append(heard, o) })

	err := observed.RequestJSON(http.MethodGet, "/api/v1/nodes/node-1", nil, nil)

	if len(heard) != 1 || heard[0].Status != 0 || heard[0].Err == nil || err == nil {
		t.Errorf("heard %+v with err %v, want one outcome with no status and the error", heard, err)
	}
}

func TestAnObserverHearsTheLastAnswerAfterA429Once(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, _ := testClient(t, &throttling{refusals: 1, retryAfter: "1"})
		var heard []Outcome
		observed := client.WithObserver(func(o Outcome) { heard = append(heard, o) })

		err := observed.RequestJSON(http.MethodGet, "/api/v1/nodes/node-1", nil, nil)

		if len(heard) != 1 || heard[0].Status != http.StatusOK || err != nil {
			t.Errorf("heard %+v with err %v, want one 200", heard, err)
		}
	})
}
