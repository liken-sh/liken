// Package releases produces and operates the release channel: the
// layout a release server publishes (dist/<version>/ holding the
// artifacts and the release.yaml that names them by digest), and the
// server that exposes it.
//
// There is one kind of channel, and it is public: the generic OS
// (vmlinuz, the generic liken.cpio, the toolkit binary) with no
// deployment inside. Every fleet upgrades from it directly. Each
// machine carries its own deployment layer between its slots, so the
// channel never composes or hosts anything specific to one
// deployment. The server here stands in for the releases on the
// liken.sh website. A deployment's choices live on its machines and
// in its cluster's API, not on a server.
package releases

// This file implements the release server: a dist/ tree over HTTP,
// with every request logged.
//
// A real release server is nothing more than this: static files
// under stable URLs. The trust chain deliberately asks nothing of
// the transport. A machine verifies every byte it downloads against
// a digest it got from the cluster's API, so the server needs no
// authentication, no TLS, and no logic of its own to be safe to
// upgrade from. (Integrity comes from the digests. Privacy would
// need TLS, but there is nothing private about an OS image.)
//
// The logging is why this file exists instead of something like
// python3 -m http.server. During upgrade drills, a person watches
// this terminal to see machines fetch: which release, which
// artifact, how many bytes. A stalled or repeated download is
// visible the moment it happens.

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"time"
)

// Serve exposes a channel directory the way a release server would
// and blocks until the listener fails.
func Serve(dir, addr string) error {
	fmt.Println(banner(dir, addr))
	return http.ListenAndServe(addr, handler(dir))
}

// handler serves the published releases under /releases/, mirroring
// the source URL the Cluster's spec declares, and logs one line for
// each request. It is separate from Serve so the tests can run the
// server against a throwaway directory.
func handler(dir string) http.Handler {
	files := http.StripPrefix("/releases/", http.FileServer(http.Dir(dir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		files.ServeHTTP(recorder, r)
		log.Printf("%s %s -> %d (%d bytes, %s)",
			r.Method, r.URL.Path, recorder.status, recorder.bytes,
			time.Since(start).Round(time.Millisecond))
	})
}

// responseRecorder wraps a ResponseWriter to remember what was sent.
// The standard library's handlers write directly to the socket, so
// the only way to log a response is to wrap the writer and watch what
// passes through it.
type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(p []byte) (int, error) {
	n, err := r.ResponseWriter.Write(p)
	r.bytes += int64(n)
	return n, err
}

// banner is the startup line: where the releases come from, where the
// server listens, and how a guest reaches it. QEMU's user-mode
// networking presents the host's loopback to every guest as
// 10.0.2.2, so the hint spells out the exact URL that a machine's
// release source points at, derived from whatever port the server
// was given. An address with no port cannot produce the hint, so
// banner omits it.
func banner(dir, addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Sprintf("serving releases from %s on %s", dir, addr)
	}
	return fmt.Sprintf("serving releases from %s on %s (guests reach this at http://10.0.2.2:%s/releases)", dir, addr, port)
}
