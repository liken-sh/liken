package main

// The startup probe of the PipeWire container runs pw-dump, which
// connects to PipeWire's socket and prints the whole graph, so a probe
// that passes means that readGraph can read the graph too.
//
// The kubelet keeps only 10 KiB of an exec probe's output and logs a
// message each time a probe prints more, and the graph is often larger.
// So the probe runs pw-dump as a child, discards the graph, and exits
// with pw-dump's status. The PipeWire image has no shell that could
// redirect the output, so the probe is a mode of the operator's binary.

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// graphProbeMode is the argument that the PipeWire container's
// startupProbe in deploy/operator.yaml passes to the operator's binary.
const graphProbeMode = "graph-answers"

// graphProbeTimeout is shorter than the probe's timeoutSeconds of 10,
// so the probe stops pw-dump and prints why before the kubelet times
// the probe out.
const graphProbeTimeout = 8 * time.Second

func graphProbe() {
	if err := checkGraph(context.Background()); err != nil {
		fatal("%v", err)
	}
}

// checkGraph discards pw-dump's stdout, which is the graph, and returns
// its stderr in the error, because pw-dump writes there why it could
// not connect.
func checkGraph(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, graphProbeTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, "pw-dump")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	command.WaitDelay = time.Second
	err := command.Run()
	if err == nil {
		return nil
	}
	// The error of a pw-dump killed at the timeout is "signal: killed",
	// which does not name the timeout.
	if ctx.Err() != nil {
		err = fmt.Errorf("pw-dump did not finish in %s", graphProbeTimeout)
	} else {
		err = fmt.Errorf("running pw-dump: %w", err)
	}
	if words := strings.TrimSpace(stderr.String()); words != "" {
		return fmt.Errorf("%w: %s", err, words)
	}
	return err
}
