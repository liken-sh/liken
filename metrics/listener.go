package metrics

// The listener: how a scrape reaches an operator's registry.
//
// A Prometheus server pulls. It opens an HTTP connection to each
// target every few seconds and reads a text document of the current
// numbers. So every process that publishes metrics has to serve a
// port, and the contract gives each liken process a port of its own,
// because some of these pods run on the host network and two of them
// can share a node.
//
// A scrape reads only the in-memory registry. It never walks sysfs,
// never calls the Kubernetes API, and never waits on hardware. This
// is what makes the listener safe to answer at any moment: a scraper
// that arrives during a reboot decision reads the last numbers the
// reconcile pass published, and delays nothing.

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Handler answers a scrape from this operator's registry. It is
// exported so a test can scrape without a socket, and so a program
// can put /metrics on a server of its own.
func (o *Operator) Handler() http.Handler {
	return promhttp.HandlerFor(o.registry, promhttp.HandlerOpts{
		// A collector that fails is reported as a broken scrape,
		// rather than served as a document with a gap in it. A
		// scraper reads the failure and records the target as down,
		// which is the accurate answer.
		ErrorHandling: promhttp.HTTPErrorOnError,
	})
}

// Serve starts the /metrics listener and returns at once. It returns
// the address the listener holds, so a caller can report the real
// port after asking for port 0.
//
// An empty address turns the listener off. The function then returns
// no address and no error, and the process runs with no metrics at
// all. This is what lets an owner who runs no Prometheus give up the
// port, and it is why the address is a setting rather than a
// constant.
//
// The listen call happens here, in the caller's own goroutine, so a
// port already in use is an error the caller can report. Serving
// happens on a goroutine, because the work the process exists for
// must not wait on a scraper. A failure after this point ends the
// listener alone.
func (o *Operator) Serve(address string) (net.Addr, error) {
	if address == "" {
		return nil, nil
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", o.Handler())
	server := &http.Server{
		Handler: mux,
		// A client that opens a connection and sends no headers holds
		// a goroutine for as long as it stays. This bound releases
		// that goroutine, and it is the one timeout a metrics
		// endpoint needs: the handler itself only reads memory.
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := server.Serve(listener); err != nil {
			fmt.Fprintf(os.Stderr, "the metrics listener stopped: %v\n", err)
		}
	}()
	return listener.Addr(), nil
}
