package main

// The startup probe of the PipeWire container: whether PipeWire
// answers the call the operator makes to read the graph.
//
// The fact the probe reads is a client connection, not a file that
// exists. pw-dump connects to the socket, waits for the whole graph,
// and prints it, so a pw-dump that exits 0 is a PipeWire that answers
// the same call readGraph makes. The graph is often larger than the
// 10 KiB the kubelet keeps of a probe's output, and the kubelet logs a
// short write each time a probe prints more. So this check runs
// pw-dump, discards the graph, and passes pw-dump's exit status on.
// The image holds no shell, so the probe cannot redirect the output
// itself, and the operator's binary is already in the image.

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// graphMode is the argument that selects this check. The startup
// probe on the PipeWire container names it, and no other caller has a
// reason to.
const graphMode = "graph-answers"

// graphProbeTimeout bounds the pw-dump this check runs. The probe's
// own timeout is 10 seconds, and the kubelet counts a probe that
// exceeds it as a failure, so this check gives up first, stops
// pw-dump, and reports the reason itself.
const graphProbeTimeout = 8 * time.Second

// graphProbe is the probe. It exits 0 when pw-dump reads the whole
// graph, and 1 with pw-dump's own error text when it does not.
func graphProbe() {
	if err := graphAnswers(context.Background()); err != nil {
		fatal("%v", err)
	}
}

// graphAnswers runs pw-dump once. pw-dump's standard output goes to
// the null device, because the check needs only the exit status. Its
// standard error is kept for the report, because that is where it
// says why the connection failed.
func graphAnswers(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, graphProbeTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, "pw-dump")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	// The context's kill bounds the exec, and WaitDelay bounds the
	// wait after the kill, the same way readGraph bounds its pw-dump.
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		return fmt.Errorf("running pw-dump: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
