package main

// The boot's half of the fail-stop record.
//
// failBoot prints the reason for a refusal and powers the machine off.
// On an install boot that is enough: a person picked the entry and is
// reading the console. On a boot from disk nobody is reading anything.
// The message goes to the kernel ring buffer, k3s never starts, so no
// log relay carries it off the machine, and the power-off erases the
// ring buffer. The machine then repeats the same few seconds at every
// power-on, and the only way to learn which of failBoot's call sites
// fired is to read init's source.
//
// This file closes that gap. recordFailStop writes the reason to
// machineState before the power-off, and reportFailStop reads it back
// on the next boot: to the console for a person at the serial port,
// and into status.lastFailStop for everyone else. machine/failstop.go
// owns the record itself.
//
// One limit remains, and it follows from where the record lives. A
// boot that cannot satisfy the machineState role has nowhere to write,
// and records nothing. The same is true of a boot that stops over a
// role which mounts before machineState does. Every other refusal,
// including all of the identity failures, lands on the disk.

import (
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	"github.com/liken-sh/liken/machine"
)

// failStopReasonCap bounds the recorded reason, matching the maxLength
// that the CRD sets on status.lastFailStop.reason. The cut happens
// once, here, before the write, so the record on disk and the field in
// the cluster carry exactly the same words.
const failStopReasonCap = 1024

// recordFailStop writes this boot's refusal to machineState, when
// there is a mounted machineState to write to. A failure to record is
// reported and no more: the machine is already stopping, and losing
// the record must not stand in the way of the power-off.
//
// An install boot records nothing, for two reasons. A person picked
// that entry and is reading the console, which is the audience the
// record exists to replace. And the installer must leave nothing on a
// machine it did not finish installing: a record written from the
// stick would be read back by the first successful boot and reported
// as that machine's own history.
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
	fmt.Fprintln(os.Stderr, "liken: fail-stop: recorded the reason; the next boot reports it as status.lastFailStop")
}

// reportFailStop reads the standing record and puts it on the console.
// It runs on every boot, refused or not, and it never removes the
// record: the field answers when this machine last refused to boot,
// which stays true until the next refusal overwrites it. Deriving the
// answer from the file on every boot is what makes the field
// reconstructible after somebody erases the Machine's status.
func reportFailStop(stateDir string) *machine.FailStop {
	record, err := machine.ReadFailStop(stateDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: fail-stop: reading the record: %v\n", err)
		return nil
	}
	if record == nil {
		return nil
	}
	// Console parity: the fact prints where an operator at the serial
	// port can see it, in the same words status carries.
	fmt.Printf("liken: fail-stop: this machine last refused to boot at %s: %s\n",
		record.Time.UTC().Format(time.RFC3339), record.Reason)
	return record
}

// capReason cuts a reason to the cap on a rune boundary, so a
// truncated message stays valid UTF-8 and the API server accepts it.
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
