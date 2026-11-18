package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// testClock is a clock that a test advances by hand.
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

// copyingFFmpeg writes a stand-in ffmpeg that copies the display
// upstream's input to its output and discards the audio input.
func copyingFFmpeg(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\ncat <&4 > /dev/null &\ncat <&3\nwait\n"
	mustSucceed(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

// An upstream that sends headers at once and then streams for longer
// than the header deadline is not cut off, and an upstream that sends
// headers late gets a 504.
func TestTheHeaderDeadlineBoundsOnlyTheWaitForHeaders(t *testing.T) {
	// A synctest bubble cannot wait on the ffmpeg subprocess, so this
	// case runs on the real clock with a short deadline.
	t.Run("a stream that outlives the bound", func(t *testing.T) {
		original := upstreamHeaderTimeout
		upstreamHeaderTimeout = 50 * time.Millisecond
		t.Cleanup(func() { upstreamHeaderTimeout = original })
		fixture := newAPIFixture(t)
		clock := &testClock{now: testAPIClock}
		fixture.server.now = clock.read
		fixture.server.ffmpeg = copyingFFmpeg(t)
		fixture.display.body = []byte("frame")
		fixture.display.chunks = 6
		fixture.display.chunkGap = 40 * time.Millisecond
		// Each chunk advances the server's clock by five seconds, so the
		// stream spans thirty seconds of server time, and each gap stays
		// under upstreamIdleTimeout.
		fixture.display.onChunk = func() { clock.advance(5 * time.Second) }

		recorder := fixture.get(playerPathFor("media.mp4"))

		mustMatch(t, recorder.Code, http.StatusOK)
		mustMatch(t, recorder.Body.String(), "frameframeframeframeframeframe")
		mustMatch(t, clock.read().Sub(testAPIClock), 30*time.Second)
	})

	t.Run("headers that arrive after the bound", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newAPIFixture(t)
			fixture.display.delay = upstreamHeaderTimeout + time.Second

			recorder := fixture.get(playerPathFor("media.mp4"))

			mustMatch(t, recorder.Code, http.StatusGatewayTimeout)
			document := problemOf(t, recorder)
			mustMatch(t, document.Type, aboutBlank)
			mustMatch(t, document.Detail, "the upstream sent no headers within 10s")
		})
	})
}

// The listener sets a deadline for reading request headers and none for
// the response, so a capture streams for as long as the caller reads.
func TestThisApiBoundsTheRequestHeadersAndNotTheResponse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)

		listener := fixture.server.listener(":8443")

		mustMatch(t, listener.ReadHeaderTimeout, serverReadHeaderTimeout)
		mustMatch(t, listener.WriteTimeout, time.Duration(0))
		mustMatch(t, listener.IdleTimeout, time.Duration(0))
		mustMatch(t, listener.ReadTimeout, time.Duration(0))
	})
}
