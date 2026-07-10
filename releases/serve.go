// Package releases produces and operates release channels: the
// layout a release server publishes (dist/<version>/ holding the
// artifacts and the release.yaml that names them by digest), the
// server that exposes it, and the drills that prove machines refuse
// what the digests disown.
//
// Two kinds of channel share this machinery. liken's own public
// releases carry the generic OS (vmlinuz, the generic liken.cpio,
// the toolkit binary) with no deployment inside; a deployment's
// channel carries that OS composed with the deployment's layer,
// because the digest chain must cover the exact bytes its machines
// boot. The layouts are the same shape, so the same publishing,
// serving, and corrupting code operates both.
package releases

// The release server: a dist/ tree over HTTP, every request logged.
//
// A real release server is nothing more than this: static files under
// stable URLs. The trust chain deliberately asks nothing of the
// transport. Every byte a machine downloads is verified against a
// digest it got from the cluster's API, so the server needs no
// authentication, no TLS, and no logic of its own to be safe to
// upgrade from. (Integrity comes from the digests; privacy would take
// TLS, but there is nothing private about an OS image.)
//
// The logging is why this exists instead of something like
// python3 -m http.server. Upgrade drills watch this terminal to see
// machines fetch: which release, which artifact, how many bytes. A
// stalled download or a re-fetch after a corruption drill is visible
// the moment it happens.

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
// the source URL the Cluster's spec declares, and logs one line per
// request. It is separate from Serve so the tests can run the server
// against a throwaway directory.
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
// the only way to log a response is to wrap the writer and observe
// what passes through it.
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
// networking presents the host's loopback to every guest as 10.0.2.2,
// so the hint spells out the exact URL a machine's release source
// points at, derived from whatever port the server was given. An
// address without a port can't produce the hint, so it is omitted.
func banner(dir, addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Sprintf("serving releases from %s on %s", dir, addr)
	}
	return fmt.Sprintf("serving releases from %s on %s (guests reach this at http://10.0.2.2:%s/releases)", dir, addr, port)
}
