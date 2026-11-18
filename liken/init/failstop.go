package main

// failBoot prints its reason and powers the machine off. On a boot from
// disk nobody reads the console. k3s has not started, so no log relay
// sends the message off the machine, and the power-off erases the
// kernel ring buffer. The machine then refuses again at every power-on
// and leaves no trace of the reason.
//
// So recordFailStop writes the reason to machineState before the
// power-off, and reportFailStop reads it on the next boot for
// status.lastFailStop. machine/failstop.go defines the record.
//
// A refusal leaves a record only when machineState is mounted from a
// partition. An identity refusal happens after settleStorage, so it is
// recorded whenever the manifest declares machineState. A storage
// refusal is recorded only when it happens after machineState mounts.
// reconcileStorage waits for every declared disk and plans every role
// before it mounts any, so a disk that never attaches stops the boot
// with no record, even when the state disk is fine.

import (
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	"github.com/liken-sh/liken/liken/machine"
)

// failStopReasonCap is the maxLength that the CRD sets on
// status.lastFailStop.reason. recordFailStop cuts the reason to it
// before writing, so the file and the status field hold the same text.
const failStopReasonCap = 1024

// recordFailStop writes this boot's refusal to machineState when
// machineState is mounted. A write error is printed and otherwise
// ignored, because the machine must still power off.
//
// An install boot records nothing. A person reads its console, and the
// first boot of the installed machine would otherwise report the
// installer's refusal as its own.
func recordFailStop(stateDir, reason string, now time.Time) {
	if installing() {
		return
	}
	if !machineStateWritable {
		fmt.Fprintln(os.Stderr, "liken: fail-stop: machineState is not mounted, so this refusal leaves no record")
		return
	}
	record := machine.FailStop{Reason: capReason(reason), Time: now}
	if err := machine.WriteFailStop(stateDir, record); err != nil {
		fmt.Fprintf(os.Stderr, "liken: fail-stop: recording the refusal: %v\n", err)
		return
	}
	fmt.Fprintln(os.Stderr, "liken: fail-stop: recorded the reason, and the next boot reports it as status.lastFailStop")
}

// reportFailStop reads the record, prints it on the console, and
// returns it, or returns nil when there is none or it cannot be read.
// It never deletes the record, so every boot reports the last refusal
// until a later one replaces it, and status.lastFailStop comes back
// when the Machine's status is erased.
func reportFailStop(stateDir string) *machine.FailStop {
	record, err := machine.ReadFailStop(stateDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: fail-stop: reading the record: %v\n", err)
		return nil
	}
	if record == nil {
		return nil
	}
	fmt.Printf("liken: fail-stop: this machine last refused to boot at %s: %s\n",
		record.Time.UTC().Format(time.RFC3339), record.Reason)
	return record
}

// capReason cuts a reason to failStopReasonCap bytes on a rune
// boundary. The API server rejects a status that is not valid UTF-8.
func capReason(reason string) string {
	if len(reason) <= failStopReasonCap {
		return reason
	}
	cut := failStopReasonCap
	for cut > 0 && !utf8.RuneStart(reason[cut]) {
		cut--
	}
	return reason[:cut]
}
