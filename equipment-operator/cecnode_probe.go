package main

// The probe of the node workload. The kubelet asks it whether the
// node workload's loop still runs, for the readiness probe and the
// liveness probe of deploy/cec.yaml. A loop that stops reaching its
// select, such as one blocked on a call that never returns, makes no
// pass and writes no entry, and its entry would keep describing the
// adapter as it was. So the probe asks the loop itself: the handler
// sends a question into the loop's select and waits a short time for
// the answer. A pod whose loop does not answer stops being Ready, so
// the Deployment marks its entry stale (cecpods.go), and the kubelet
// restarts its container after the liveness probe's failures.
//
// No timer in the loop keeps the probe's answer current. The loop
// answers each question when it reaches its select, so the answer is
// always the loop's state at the moment of the question.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// CEC_PROBE_ADDRESS is where the probe listens, as host:port. The
// DaemonSet sets it, and an empty value, as in a run by hand, turns
// the listener off.
const cecProbeVariable = "CEC_PROBE_ADDRESS"

// cecProbeWait is how long the probe waits for the loop's answer. A
// loop in its select answers at once. A loop in a pass answers when the
// pass ends, and a pass can wait on the adapter or the API server for
// longer than this wait. One probe that fails during such a pass costs
// nothing, because the probes in deploy/cec.yaml fail a pod only after
// several misses in a row. Each probe's timeout is longer than this
// wait, so the handler answers before the kubelet stops waiting.
const cecProbeWait = 2 * time.Second

// answers reports whether the loop answers a question within
// cecProbeWait, or before ctx ends.
func (n *cecNode) answers(ctx context.Context) bool {
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

// probeHandler answers 200 while the loop answers, and 503 with the
// reason when it does not.
func (n *cecNode) probeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !n.answers(r.Context()) {
			http.Error(w, fmt.Sprintf("the node workload's loop on %s did not answer within %s", n.machine, cecProbeWait), http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})
}

// serveProbe starts the probe's listener at address and returns at
// once, with the address the listener holds. An empty address starts
// none. The listener stops when ctx ends.
func (n *cecNode) serveProbe(ctx context.Context, address string) (net.Addr, error) {
	if address == "" {
		return nil, nil
	}
	listener, err := listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("the probe listener: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/healthz", n.probeHandler())
	// A client that opens a connection and sends no headers holds a
	// goroutine for as long as it stays, and this bound releases it.
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	context.AfterFunc(ctx, func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	return listener.Addr(), nil
}
