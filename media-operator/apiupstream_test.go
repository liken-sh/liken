package main

// These tests cover what the two bounds on an upstream leg each cover:
// the header deadline waits for headers and nothing more, and the body
// that follows runs until the idle watchdog or the caller ends it.

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// testClock is a clock a test moves by hand, so a composition can run
// for thirty seconds of the server's time in a fraction of a second of
// the machine's.
type testClock struct {
	mutex sync.Mutex
	now   time.Time
}

func (c *testClock) read() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

func (c *testClock) advance(by time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.now = c.now.Add(by)
}

// copyingFFmpeg stands in for the mux and copies the video body
// through to the response, so the test reads what the display sibling
// sent and can tell a stream that was cut from one that ran.
func copyingFFmpeg(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\ncat <&4 > /dev/null &\ncat <&3\nwait\n"
	mustSucceed(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

// The header deadline covers the wait for headers alone. A sibling
// that answers at once and then streams for far longer than the bound
// is not cut, and a sibling that answers late is a 504.
func TestTheHeaderDeadlineBoundsOnlyTheWaitForHeaders(t *testing.T) {
	held := upstreamHeaderTimeout
	upstreamHeaderTimeout = 50 * time.Millisecond
	defer func() { upstreamHeaderTimeout = held }()

	t.Run("a stream that outlives the bound", func(t *testing.T) {
		fixture := newAPIFixture(t)
		clock := &testClock{now: testAPIClock}
		fixture.server.now = clock.read
		fixture.server.ffmpeg = copyingFFmpeg(t)
		fixture.display.body = []byte("frame")
		fixture.display.chunks = 6
		fixture.display.chunkGap = 40 * time.Millisecond
		// The server's clock moves five seconds per chunk, so the
		// composition runs thirty seconds and every idle mark stays
		// current. Real time is under half a second.
		fixture.display.onChunk = func() { clock.advance(5 * time.Second) }

		recorder := fixture.get(playerPathFor("media.mp4"))

		mustMatch(t, recorder.Code, http.StatusOK)
		mustMatch(t, recorder.Body.String(), "frameframeframeframeframeframe")
		mustMatch(t, clock.read().Sub(testAPIClock), 30*time.Second)
	})

	t.Run("headers that arrive after the bound", func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.display.delay = 500 * time.Millisecond

		recorder := fixture.get(playerPathFor("media.mp4"))

		mustMatch(t, recorder.Code, http.StatusGatewayTimeout)
		document := problemOf(t, recorder)
		mustMatch(t, document.Type, aboutBlank)
		mustMatch(t, document.Detail, "the upstream sent no headers within 50ms")
	})
}

// The caller's own response carries no write deadline, so a capture
// runs for as long as the caller reads it. Only the request headers
// are bounded.
func TestThisApiBoundsTheRequestHeadersAndNotTheResponse(t *testing.T) {
	fixture := newAPIFixture(t)

	listener := fixture.server.listener(":8443")

	mustMatch(t, listener.ReadHeaderTimeout, serverReadHeaderTimeout)
	mustMatch(t, listener.WriteTimeout, time.Duration(0))
	mustMatch(t, listener.IdleTimeout, time.Duration(0))
	mustMatch(t, listener.ReadTimeout, time.Duration(0))
}
