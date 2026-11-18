package apiservertest_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	utilnet "k8s.io/apimachinery/pkg/util/net"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// echo responds with the request's method, path, and body.
var echo = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_, _ = io.WriteString(w, r.Method+" "+r.URL.Path+" "+string(body))
})

func send(t *testing.T, client *http.Client, method, body string) (string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, apiservertest.Host+"/things/a", strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	return string(got), err
}

// The server behaves the same in a bubble and outside one, so a test can
// switch to it before it switches to synctest.
func TestTheServerRespondsInAndOutOfABubble(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T, func(*testing.T))
	}{
		{"outside a bubble", func(t *testing.T, test func(*testing.T)) { test(t) }},
		{"in a bubble", synctest.Test},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.run(t, func(t *testing.T) {
				client := apiservertest.Start(t, echo).Client()

				got, err := send(t, client, http.MethodPut, `{"a":1}`)

				if err != nil || got != `PUT /things/a {"a":1}` {
					t.Errorf("the response is %q, %v, want the request echoed", got, err)
				}
			})
		})
	}
}

// A client built from Config reaches the server.
func TestClientGoReachesTheServer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, err := rest.HTTPClientFor(apiservertest.Start(t, echo).Config())
		if err != nil {
			t.Fatalf("building the client from Config: %v", err)
		}

		got, err := send(t, client, http.MethodGet, "")

		if err != nil || got != "GET /things/a " {
			t.Errorf("the response is %q, %v, want the request echoed", got, err)
		}
	})
}

// A server that is down refuses each connection with ECONNREFUSED, as
// a restarting API server does, and responds again once it is back up.
func TestADownServerRefusesEachConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := apiservertest.Start(t, echo)
		server.SetDown(true)
		_, down := send(t, server.Client(), http.MethodGet, "")
		server.SetDown(false)
		got, up := send(t, server.Client(), http.MethodGet, "")

		if !utilnet.IsConnectionRefused(down) {
			t.Errorf("a request to the down server returned %v, want ECONNREFUSED", down)
		}
		if up != nil || got != "GET /things/a " {
			t.Errorf("after SetDown(false) the response is %q, %v, want the request echoed", got, up)
		}
	})
}

// stream is a handler that sends one line and then keeps the response
// open until the request ends, as the API server does for a watch.
type stream struct{ ended atomic.Int64 }

func (s *stream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, "first\n")
	w.(http.Flusher).Flush()
	<-r.Context().Done()
	s.ended.Add(1)
}

// open starts a request to a stream handler, reads its first line, and
// then reads on in a goroutine, as client-go's watch reader waits for
// the next event. The returned channel closes when that read returns.
func open(t *testing.T, ctx context.Context, server *apiservertest.Server) (io.Closer, <-chan struct{}) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiservertest.Host+"/watch", nil)
	if err != nil {
		t.Fatalf("building the watch request: %v", err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("sending the watch request: %v", err)
	}
	lines := bufio.NewReader(resp.Body)
	if line, err := lines.ReadString('\n'); err != nil || line != "first\n" {
		t.Fatalf("the first line of the stream is %q, %v, want \"first\\n\"", line, err)
	}
	read := make(chan struct{})
	go func() {
		defer close(read)
		_, _ = lines.ReadString('\n')
	}()
	return resp.Body, read
}

// A stream ends on both sides at once when the client closes the body,
// when the request's context ends, or when the server goes down, with no
// advance of the bubble's clock.
func TestAStreamEnds(t *testing.T) {
	cases := []struct {
		name string
		end  func(body io.Closer, cancel context.CancelFunc, server *apiservertest.Server)
	}{
		{"the caller closes the body", func(body io.Closer, _ context.CancelFunc, _ *apiservertest.Server) { _ = body.Close() }},
		{"the request's context ends", func(_ io.Closer, cancel context.CancelFunc, _ *apiservertest.Server) { cancel() }},
		{"the server goes down", func(_ io.Closer, _ context.CancelFunc, server *apiservertest.Server) { server.SetDown(true) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				handler := &stream{}
				server := apiservertest.Start(t, handler)
				ctx, cancel := context.WithCancel(t.Context())
				body, read := open(t, ctx, server)
				started := time.Now()

				c.end(body, cancel, server)
				<-read
				synctest.Wait()

				if handler.ended.Load() != 1 || time.Since(started) != 0 {
					t.Errorf("the handler ended %d requests after %s, want 1 request after 0s", handler.ended.Load(), time.Since(started))
				}
			})
		})
	}
}

// A request whose context has already ended never reaches the handler.
func TestARequestWithAnEndedContextIsNotSent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var reached atomic.Int64
		server := apiservertest.Start(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Add(1) }))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiservertest.Host, nil)
		if err != nil {
			t.Fatalf("building the request: %v", err)
		}

		_, err = server.Client().Do(req)
		synctest.Wait()

		if !errors.Is(err, context.Canceled) || reached.Load() != 0 {
			t.Errorf("Do returned %v and %d requests reached the handler, want context.Canceled and 0", err, reached.Load())
		}
	})
}

// A request that ends before the server responds fails. A dropped
// connection fails with the connection's error, and a canceled context
// fails with the context's error, as with net/http's Transport.
func TestARequestThatEndsBeforeTheResponseFails(t *testing.T) {
	cases := []struct {
		name string
		end  func(cancel context.CancelFunc)
		want error
	}{
		{"the server drops the connection", func(context.CancelFunc) { panic(http.ErrAbortHandler) }, io.ErrUnexpectedEOF},
		{"the request's context ends", func(cancel context.CancelFunc) { cancel() }, context.Canceled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				server := apiservertest.Start(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					c.end(cancel)
					<-r.Context().Done()
				}))
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiservertest.Host, nil)
				if err != nil {
					t.Fatalf("building the request: %v", err)
				}

				_, err = server.RoundTrip(req)

				if !errors.Is(err, c.want) {
					t.Errorf("RoundTrip returned %v, want %v", err, c.want)
				}
			})
		})
	}
}

// HandleError returns at once in a bubble. apimachinery's own rate limit
// would sleep until a time recorded outside the bubble, which is years
// after the bubble's clock.
func TestAnUnhandledErrorReturnsAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()

		utilruntime.HandleError(errors.New("the watch failed"))
		utilruntime.HandleError(errors.New("the watch failed again"))

		if waited := time.Since(started); waited != 0 {
			t.Errorf("two calls to HandleError took %s on the bubble's clock, want 0s", waited)
		}
	})
}
