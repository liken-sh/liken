// The lab's release server: the dist/ tree over HTTP, every request
// logged.
//
// A real release server is nothing more than this: static files under
// stable URLs. The trust chain deliberately asks nothing of the
// transport. Every byte a machine downloads is verified against a
// digest it got from the cluster's API, so the server needs no
// authentication, no TLS, and no logic of its own to be safe to
// upgrade from. (Integrity comes from the digests; privacy would take
// TLS, but there is nothing private about an OS image.)
//
// The logging is why this program exists instead of using something
// like python3 -m http.server. Upgrade drills watch this terminal to
// see machines fetch: which release, which artifact, how many bytes.
// A stalled download or a re-fetch after a corruption drill is
// visible the moment it happens.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"
)

// handler serves the published releases under /releases/, mirroring
// the source URL the Cluster's spec declares, and logs one line per
// request. It is separate from main so the tests can run the server
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

func main() {
	dir := flag.String("dir", "dist", "the published releases to serve")
	addr := flag.String("addr", ":8017", "the address to listen on")
	flag.Parse()

	fmt.Printf("serving releases from %s on %s (guests reach this at http://10.0.2.2:8017/releases)\n", *dir, *addr)
	log.Fatal(http.ListenAndServe(*addr, handler(*dir)))
}
