package main

// Each write of a PipeWire parameter runs pw-cli set-param once, as the
// graph read in pipewire.go runs pw-dump once. A process for each write
// needs no PipeWire client library and keeps no connection open.

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// pwCLITimeout limits one pw-cli run, because a PipeWire that accepts
// the connection and never replies would block it forever.
// pwDumpTimeout limits pw-dump for the same reason.
const pwCLITimeout = 10 * time.Second

// setParam writes the parameter param, such as Props or Route, of the
// PipeWire object with the id object. pod is a SPA POD object in
// pw-cli's input syntax, with each enum value given as its integer id,
// as in { bluetoothAudioCodec: 1 }.
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
