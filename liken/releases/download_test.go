package releases

// Tests for the stall limit on a download. Each test runs in a
// synctest bubble against an in-memory server, so a minute of silence
// costs no real time.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// A server that sends the bytes given, one write each, with the wait
// given before each write, and then holds the response open until the
// client goes away.
func dribblingServer(t *testing.T, wait time.Duration, writes ...string) *http.Client {
	t.Helper()
	server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, chunk := range writes {
			time.Sleep(wait)
			io.WriteString(w, chunk)
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
	}))
	return server.Client()
}

// A completed read and how long it took on the bubble's clock.
type readResult struct {
	body    string
	err     error
	elapsed time.Duration
}

// The body of url read until it ends or fails.
func readAll(ctx context.Context, client *http.Client, url string) readResult {
	start := time.Now()
	resp, err := Get(ctx, client, url)
	if err != nil {
		return readResult{err: err, elapsed: time.Since(start)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return readResult{body: string(body), err: err, elapsed: time.Since(start)}
}

func TestADownloadStopsWhenTheServerStopsSending(t *testing.T) {
	cases := []struct {
		name   string
		writes []string
		read   string
	}{
		{name: "before the headers"},
		{name: "in the body", writes: []string{"the first bytes"}, read: "the first bytes"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := dribblingServer(t, 0, one.writes...)

				got := readAll(t.Context(), client, apiservertest.Host+"/vmlinuz")

				if !errors.Is(got.err, errStalled) {
					t.Errorf("err = %v, want the stall", got.err)
				}
				if got.elapsed != StallLimit {
					t.Errorf("the download stopped after %v, want %v", got.elapsed, StallLimit)
				}
				if got.body != one.read {
					t.Errorf("read %q before the stall, want %q", got.body, one.read)
				}
			})
		})
	}
}

// A download that keeps making progress runs past the stall limit.
func TestASlowDownloadThatMakesProgressFinishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for range 5 {
				time.Sleep(StallLimit - time.Second)
				io.WriteString(w, "x")
				w.(http.Flusher).Flush()
			}
		}))

		got := readAll(t.Context(), server.Client(), apiservertest.Host+"/vmlinuz")

		if got.err != nil || got.body != "xxxxx" {
			t.Errorf("read %q, %v, want every byte", got.body, got.err)
		}
		if got.elapsed <= StallLimit {
			t.Errorf("the download took %v, want longer than the stall limit", got.elapsed)
		}
	})
}

// A caller that cancels the download ends it at once, with its own
// cause.
func TestACancelledDownloadEndsWithTheCallersCause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := dribblingServer(t, 0, "the first bytes")
		retargeted := errors.New("the target changed")
		ctx, cancel := context.WithCancelCause(t.Context())
		time.AfterFunc(time.Second, func() { cancel(retargeted) })

		got := readAll(ctx, client, apiservertest.Host+"/vmlinuz")

		if !errors.Is(got.err, retargeted) {
			t.Errorf("err = %v, want the caller's cause", got.err)
		}
		if got.elapsed != time.Second {
			t.Errorf("the download stopped after %v, want 1s", got.elapsed)
		}
	})
}

func TestADownloadReportsTheServersRefusal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := apiservertest.Start(t, http.NotFoundHandler())

		got := readAll(t.Context(), server.Client(), apiservertest.Host+"/vmlinuz")

		if got.err == nil || !strings.Contains(got.err.Error(), "404 Not Found") {
			t.Errorf("err = %v, want the server's status", got.err)
		}
	})
}
