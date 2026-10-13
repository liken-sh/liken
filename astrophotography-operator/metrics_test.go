package main

// The registry, the listener, and the reconcile layer, scraped through
// the real promhttp handler.

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// scrape reads a registry as Prometheus does: one GET that the real
// handler answers. The handler writes to a recorder, so a test in a
// synctest bubble scrapes with no socket.
func scrape(t *testing.T, m *metrics) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	m.handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("the scrape answered %d", recorder.Code)
	}
	return recorder.Body.String()
}

// samples answers each sample line of a scrape whose metric is name,
// without the comment lines.
func samples(body, name string) []string {
	var out []string
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, name+" ") || strings.HasPrefix(line, name+"{") {
			out = append(out, line)
		}
	}
	return out
}

// metricName answers the name of a sample line, before its labels.
func metricName(line string) string {
	name, _, _ := strings.Cut(line, " ")
	name, _, _ = strings.Cut(name, "{")
	return name
}

func requireSample(t *testing.T, body, line string) {
	t.Helper()
	if !slices.Contains(samples(body, metricName(line)), line) {
		t.Errorf("the scrape has no %q; it has %q", line, samples(body, metricName(line)))
	}
}

func TestBuildInfoNamesTheComponentAndItsRelease(t *testing.T) {
	t.Parallel()
	requireSample(t, scrape(t, newMetrics("2026.10.12-001")),
		`liken_build_info{component="astrophotography-operator",version="2026.10.12-001"} 1`)
}

func TestTheRuntimeLayerReportsTheProcess(t *testing.T) {
	t.Parallel()
	body := scrape(t, newMetrics("dev"))
	for _, name := range []string{"go_goroutines", "go_memstats_alloc_bytes", "process_start_time_seconds"} {
		if len(samples(body, name)) == 0 {
			t.Errorf("the scrape has no %s", name)
		}
	}
}

// Every kind publishes its counters at zero from the first scrape, so
// Prometheus has a reading to compute a rate from when one first moves.
func TestEveryKindPublishesItsCountersAtZero(t *testing.T) {
	t.Parallel()
	body := scrape(t, newMetrics("dev"))
	for _, kind := range []string{"Target", "Solver", "Reservation"} {
		requireSample(t, body, `astrophotography_reconcile_duration_seconds_count{kind="`+kind+`"} 0`)
		requireSample(t, body, `astrophotography_reconcile_errors_total{kind="`+kind+`"} 0`)
	}
	for _, kind := range []string{"Target", "Solver", "Reservation", "Camera", "Guider", "Pod", "Service", "Deployment", "Job"} {
		requireSample(t, body, `astrophotography_watch_restarts_total{kind="`+kind+`"} 0`)
	}
}

func TestAPassCountsUnderItsKind(t *testing.T) {
	t.Parallel()
	m := newMetrics("dev")
	m.observePass("Target", time.Millisecond, false)
	m.observePass("Target", time.Millisecond, true)
	m.watchReopened("Pod")()
	body := scrape(t, m)
	for _, line := range []string{
		`astrophotography_reconcile_duration_seconds_count{kind="Target"} 2`,
		`astrophotography_reconcile_errors_total{kind="Target"} 1`,
		`astrophotography_reconcile_duration_seconds_count{kind="Solver"} 0`,
		`astrophotography_watch_restarts_total{kind="Pod"} 1`,
	} {
		requireSample(t, body, line)
	}
}

func TestAnEmptyAddressServesNoMetrics(t *testing.T) {
	t.Parallel()
	addr, err := newMetrics("dev").serve(t.Context(), "")
	if err != nil || addr != nil {
		t.Errorf("serve = %v, %v; want no listener", addr, err)
	}
}

func TestAnAddressTheKernelRefusesIsAnError(t *testing.T) {
	t.Parallel()
	if _, err := newMetrics("dev").serve(t.Context(), "127.0.0.1:-1"); err == nil {
		t.Error("serve answered no error")
	}
}

// pipeListener hands each dial one end of a net.Pipe, whose reads and
// writes wait on channels, so a scrape in a synctest bubble waits on
// nothing outside the bubble.
type pipeListener struct {
	accepted chan net.Conn
	closed   chan struct{}
	once     sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.accepted:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9200} }

func (l *pipeListener) dial(context.Context, string, string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.accepted <- server:
		return client, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

// listenInMemory makes serve open a pipeListener until the test ends,
// and answers a client that dials it. listen is a variable that every
// test shares, so a test that calls this does not run in parallel.
func listenInMemory(t *testing.T) *http.Client {
	t.Helper()
	listener := &pipeListener{accepted: make(chan net.Conn), closed: make(chan struct{})}
	restore := listen
	listen = func(string, string) (net.Listener, error) { return listener, nil }
	t.Cleanup(func() { listen = restore })
	return &http.Client{Transport: &http.Transport{DialContext: listener.dial, DisableKeepAlives: true}}
}

func TestTheListenerServesTheRegistryUntilItsContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scraper := listenInMemory(t)
		ctx, cancel := context.WithCancel(t.Context())
		addr, err := newMetrics("dev").serve(ctx, ":9200")
		if err != nil {
			t.Fatal(err)
		}
		response, err := scraper.Get("http://" + addr.String() + "/metrics")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if !strings.Contains(string(body), "liken_build_info") {
			t.Errorf("the listener served no build info:\n%s", body)
		}

		cancel()
		synctest.Wait()
		if _, err := scraper.Get("http://" + addr.String() + "/metrics"); !errors.Is(err, net.ErrClosed) {
			t.Errorf("a scrape after the end answered %v", err)
		}
	})
}
