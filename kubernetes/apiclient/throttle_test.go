package apiclient

// The 429 answer: the client waits the time the API server asks for,
// sends the request again, and ends the wait with its context.

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

// throttling answers 429 to the first requests, with the given headers
// and body, and then answers the object. It counts the requests.
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

// A 429 is sent again after the wait the API server asks for, while
// the total wait stays within maxThrottleWait. The API server states
// the wait in the Retry-After header, in the Status body, or not at
// all, which means one second.
func TestA429IsSentAgainAfterTheWaitItAsksFor(t *testing.T) {
	status := `{"kind":"Status","message":"storage is (re)initializing","details":{"retryAfterSeconds":2}}`
	cases := []struct {
		name         string
		server       *throttling
		wantErr      bool
		wantRequests int64
		wantWait     time.Duration
	}{
		{"the header", &throttling{refusals: 2, retryAfter: "3"}, false, 3, 6 * time.Second},
		{"the Status body", &throttling{refusals: 2, body: status}, false, 3, 4 * time.Second},
		{"no advice", &throttling{refusals: 1}, false, 2, time.Second},
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

// A write that meets a 429 is sent again with its whole body, and with
// its content type.
func TestAWriteAfterA429SendsItsWholeBodyAgain(t *testing.T) {
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
			t.Fatal(err)
		}

		if len(bodies) != 2 || bodies[0] != body || bodies[1] != body || types[1] != "application/json" {
			t.Errorf("the server received %q with types %q, want the whole body twice", bodies, types)
		}
	})
}

// The wait after a 429 ends when the client's context ends, and the
// request answers the 429.
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

// A request a client with a wait context already sent runs to its
// answer when the context ends. A writer that must know whether its
// write landed, such as an operator that releases a Lease after its last
// write, still learns the answer.
func TestAWaitContextLetsARequestSentRunToItsAnswer(t *testing.T) {
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
			t.Errorf("err = %v, want the answer of the request that was sent", err)
		}
	})
}

// The wait after a 429 of a client with a wait context ends when the
// context ends, and the request answers the 429.
func TestAWaitContextEndsTheWaitAfterA429(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		throttled := &throttling{refusals: 100, retryAfter: "4"}
		client, _ := testClient(t, throttled)
		began := time.Now()

		err := client.WithWaitContext(ctx).RequestJSON(http.MethodGet, "/things/studio", nil, nil)

		if !errors.Is(err, ErrThrottled) || time.Since(began) != 0 || throttled.requests.Load() != 1 {
			t.Errorf("err = %v after %s and %d requests, want the 429 at once", err, time.Since(began), throttled.requests.Load())
		}
	})
}

// WithContext replaces a wait context, so a client bound to a new
// context waits out a 429 under it, and not under the wait context that
// ended.
func TestWithContextReplacesAWaitContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ended, cancel := context.WithCancel(t.Context())
		cancel()
		client, _ := testClient(t, &throttling{refusals: 1})
		began := time.Now()

		err := client.WithWaitContext(ended).WithContext(t.Context()).RequestJSON(http.MethodGet, "/things/studio", nil, nil)

		if err != nil || time.Since(began) != time.Second {
			t.Errorf("err = %v after %s, want the 429's one second waited out under the new context", err, time.Since(began))
		}
	})
}

// A 429 the client answered states the seconds the API server asked the
// caller to wait. A 429 that stated no wait, and any other error, state
// none.
func TestA429StatesTheWaitTheAPIServerAskedFor(t *testing.T) {
	cases := []struct {
		name   string
		server *throttling
		want   int
	}{
		{"the header", &throttling{refusals: 100, retryAfter: "11"}, 11},
		{"no advice", &throttling{refusals: 100}, 0},
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

// The guard runs again before a write is sent again after a 429, so a
// write that waited past the guard's deadline is not sent again.
func TestAWriteGuardRunsBeforeEachSendAfterA429(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := &throttling{refusals: 1, retryAfter: "1"}
		client, _ := testClient(t, server)
		var asked atomic.Int64
		guarded := client.WithWriteGuard(func() error {
			if asked.Add(1) > 1 {
				return errors.New("the lease is overdue")
			}
			return nil
		})

		err := guarded.RequestJSON(http.MethodPut, "/api/v1/nodes/node-1", []byte(`{}`), nil)

		if err == nil || asked.Load() != 2 || server.requests.Load() != 1 {
			t.Errorf("err = %v after %d guard calls and %d sends, want a refusal on the second call and one send",
				err, asked.Load(), server.requests.Load())
		}
	})
}
