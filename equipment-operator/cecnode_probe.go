package main

// The readiness and liveness probes of the node workload in
// deploy/cec.yaml call this handler. A main loop that is blocked on a
// call that never returns writes no new entry, and its last entry still
// looks current. So the handler sends a request into the loop's select
// and waits for the loop to reply. When the loop does not reply, the
// pod stops being Ready, the operator treats the pod's entry as stale
// (cecpods.go), and the kubelet restarts the container after enough
// liveness failures.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// cecProbeVariable names the environment variable that holds the
// host:port of the probe. The DaemonSet sets it. When it is empty, as in
// a run by hand, the probe does not listen.
const cecProbeVariable = "CEC_PROBE_ADDRESS"

// cecProbeWait is how long the handler waits for the loop to reply. A
// loop in the middle of a pass replies when the pass ends, which can
// take longer than cecProbeWait. One failed probe does no harm, because
// the probes in deploy/cec.yaml fail a pod only after several failures
// in a row. cecProbeWait must stay below the probes' timeoutSeconds, so
// the handler responds before the kubelet gives up.
const cecProbeWait = 2 * time.Second

// loopResponds reports whether the loop replies within cecProbeWait
// and before ctx ends.
func (n *cecNode) loopResponds(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, cecProbeWait)
	defer cancel()
	answer := make(chan struct{})
	select {
	case n.probes <- answer:
	case <-ctx.Done():
		return false
	}
	select {
	case <-answer:
		return true
	case <-ctx.Done():
		return false
	}
}

func (n *cecNode) probeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !n.loopResponds(r.Context()) {
			http.Error(w, fmt.Sprintf("the node workload's loop on %s did not answer within %s", n.machine, cecProbeWait), http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})
}

// serveProbe starts the probe's HTTP server in the background and
// returns the address it listens on. An empty address starts no server
// and returns nil. The server stops when ctx ends.
func (n *cecNode) serveProbe(ctx context.Context, address string) (net.Addr, error) {
	if address == "" {
		return nil, nil
	}
	listener, err := listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("opening the probe listener: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/healthz", n.probeHandler())
	// Without a header timeout, a client that connects and sends no
	// headers keeps a server goroutine until it disconnects.
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	context.AfterFunc(ctx, func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	return listener.Addr(), nil
}
