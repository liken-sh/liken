package main

// Writing to the objects in PipeWire's graph.
//
// The writes take the path the graph read takes in pipewire.go: pw-cli
// set-param is one process per write, changing one parameter on an
// object the same dump named.

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// pwCLITimeout bounds one property write.
//
// A set-param connects to the same socket, writes one pod, and
// exits. The bound guards the same failure the dump's bound
// guards: a socket that accepts the connection and then answers
// nothing.
const pwCLITimeout = 10 * time.Second

// setParam writes one of an object's parameters through pw-cli. The
// parameter is named because the two writes this operator makes go
// to different ones: Props on a node, and Route on a Bluetooth
// device.
//
// The write is an exec of pw-cli rather than a protocol message,
// for readGraph's reason: one short-lived process per action, no
// client library, no long-lived connection to keep healthy.
//
// The pod argument is a SPA object literal, pw-cli's own input
// form: { bluetoothAudioCodec: 1 } names an enum value by its
// integer id.
func setParam(ctx context.Context, object int, param, pod string) error {
	ctx, cancel := context.WithTimeout(ctx, pwCLITimeout)
	defer cancel()

	command := exec.CommandContext(ctx, "pw-cli", "set-param", strconv.Itoa(object), param, pod)
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("running pw-cli set-param %d %s %s: %w: %s",
			object, param, pod, err, strings.TrimSpace(string(output)))
	}
	return nil
}
