package apiclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// throttling returns 429, with retryAfter and body, to the first
// refusals requests, and then returns an object. It counts the requests.
type throttling struct {
	refusals   int
	retryAfter string
	body       string
	requests   atomic.Int64
}

func (s *throttling) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.requests.Add(1) <= int64(s.refusals) {
		if s.retryAfter != "" {
			w.Header().Set("Retry-After", s.retryAfter)
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(s.body))
		return
	}
	_, _ = w.Write([]byte(`{"metadata":{"name":"studio"}}`))
}

// The API server gives the wait in the Retry-After header or in the
// Status body. When it gives neither, the client waits one second. The
// client stops retrying when the total wait would pass maxThrottleWait.
func TestA429IsRetriedAfterTheWaitItAsksFor(t *testing.T) {
	status := `{"kind":"Status","message":"storage is (re)initializing","details":{"retryAfterSeconds":2}}`
	cases := []struct {
		name         string
		server       *throttling
		wantErr      bool
		wantRequests int64
		wantWait     time.Duration
	}{
		{"the Retry-After header", &throttling{refusals: 2, retryAfter: "3"}, false, 3, 6 * time.Second},
		{"the Status body", &throttling{refusals: 2, body: status}, false, 3, 4 * time.Second},
		{"no wait given", &throttling{refusals: 1}, false, 2, time.Second},
		{"longer than the limit", &throttling{refusals: 100, retryAfter: "4"}, true, 3, 8 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, _ := testClient(t, c.server)
				var out struct {
					Metadata struct{ Name string } `json:"metadata"`
				}
				began := time.Now()

				err := client.RequestJSON(http.MethodGet, "/things/studio", nil, &out)

				if c.wantErr != (err != nil) || (err == nil && out.Metadata.Name != "studio") {
					t.Errorf("err = %v, name %q; want an error: %v", err, out.Metadata.Name, c.wantErr)
				}
				if err != nil && (!strings.Contains(err.Error(), "429") || !errors.Is(err, ErrThrottled)) {
					t.Errorf("err = %v, want the 429 as ErrThrottled", err)
				}
				if got, waited := c.server.requests.Load(), time.Since(began); got != c.wantRequests || waited != c.wantWait {
					t.Errorf("the client sent %d requests in %s, want %d in %s", got, waited, c.wantRequests, c.wantWait)
				}
			})
		})
	}
}

func TestAWriteIsRetriedAfterA429WithItsWholeBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var bodies, types []string
		refused := false
		client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			read, _ := io.ReadAll(r.Body)
			bodies, types = append(bodies, string(read)), append(types, r.Header.Get("Content-Type"))
			if !refused {
				refused = true
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		}))
		body := `{"metadata":{"name":"studio","resourceVersion":"7"},"status":{"phase":"Ready"}}`

		if err := client.Request(http.MethodPut, "/things/studio/status", "application/json", []byte(body), nil); err != nil {
			t.Fatalf("sending the PUT request: %v", err)
		}

		if len(bodies) != 2 || bodies[0] != body || bodies[1] != body || types[1] != "application/json" {
			t.Errorf("the server received %q with types %q, want the whole body twice", bodies, types)
		}
	})
}

func TestTheWaitAfterA429EndsWithTheContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := &throttling{refusals: 100, retryAfter: "4"}
		client, _ := testClient(t, server)
		ctx, cancel := context.WithCancel(t.Context())
		time.AfterFunc(20*time.Millisecond, cancel)
		began := time.Now()

		err := client.WithContext(ctx).RequestJSON(http.MethodGet, "/things/studio", nil, nil)

		if err == nil || !strings.Contains(err.Error(), "429") || time.Since(began) != 20*time.Millisecond {
			t.Errorf("err = %v after %s, want the 429 when the context ends at 20ms", err, time.Since(began))
		}
		if server.requests.Load() != 1 {
			t.Errorf("the client sent %d requests, want 1", server.requests.Load())
		}
	})
}

// The wait context ends only the waits after a 429. A request already
// sent still returns its response, so a caller that needs to know
// whether its write succeeded, such as an operator that releases a
// Lease after its last write, receives the result.
func TestAWaitContextDoesNotCancelASentRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		release := make(chan struct{})
		client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cancel()
			<-release
			_, _ = w.Write([]byte(`{}`))
		}))
		time.AfterFunc(20*time.Millisecond, func() { close(release) })

		err := client.WithWaitContext(ctx).RequestJSON(http.MethodGet, "/things/studio", nil, nil)

		if err != nil {
			t.Errorf("err = %v, want the response to the request that was sent", err)
		}
	})
}

func TestAWaitContextEndsTheWaitAfterA429(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		throttled := &throttling{refusals: 100, retryAfter: "4"}
		client, _ := testClient(t, throttled)
		began := time.Now()

		err := client.WithWaitContext(ctx).RequestJSON(http.MethodGet, "/things/studio", nil, nil)

		if !errors.Is(err, ErrThrottled) || time.Since(began) != 0 || throttled.requests.Load() != 1 {
			t.Errorf("err = %v after %s and %d requests, want the 429 error at once", err, time.Since(began), throttled.requests.Load())
		}
	})
}

func TestWithContextReplacesAWaitContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ended, cancel := context.WithCancel(t.Context())
		cancel()
		client, _ := testClient(t, &throttling{refusals: 1})
		began := time.Now()

		err := client.WithWaitContext(ended).WithContext(t.Context()).RequestJSON(http.MethodGet, "/things/studio", nil, nil)

		if err != nil || time.Since(began) != time.Second {
			t.Errorf("err = %v after %s, want the 429 retried after one second under the new context", err, time.Since(began))
		}
	})
}

// RetryAfterSeconds returns 0 for a 429 that gave no wait and for any
// other error.
func TestRetryAfterSecondsReturnsTheWaitOfA429(t *testing.T) {
	cases := []struct {
		name   string
		server *throttling
		want   int
	}{
		{"the Retry-After header", &throttling{refusals: 100, retryAfter: "11"}, 11},
		{"no wait given", &throttling{refusals: 100}, 0},
		{"no 429", &throttling{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, _ := testClient(t, c.server)
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
				defer cancel()

				err := client.WithWaitContext(ctx).RequestJSON(http.MethodGet, "/things/studio", nil, nil)

				if got := RetryAfterSeconds(err); got != c.want {
					t.Errorf("RetryAfterSeconds(%v) = %d, want %d", err, got, c.want)
				}
			})
		})
	}
}

// A write that waited past the guard's deadline is not sent again.
func TestTheWriteGuardRunsBeforeEachRetryAfterA429(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := &throttling{refusals: 1, retryAfter: "1"}
		client, _ := testClient(t, server)
		var guardCalls atomic.Int64
		guarded := client.WithWriteGuard(func() error {
			if guardCalls.Add(1) > 1 {
				return errors.New("the lease is overdue")
			}
			return nil
		})

		err := guarded.RequestJSON(http.MethodPut, "/api/v1/nodes/node-1", []byte(`{}`), nil)

		if err == nil || guardCalls.Load() != 2 || server.requests.Load() != 1 {
			t.Errorf("err = %v after %d guard calls and %d sends, want a refusal on the second call and one send",
				err, guardCalls.Load(), server.requests.Load())
		}
	})
}
