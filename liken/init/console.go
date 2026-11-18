package main

// init writes its log lines to /dev/kmsg, the kernel's log buffer,
// and not straight to the serial console. The kernel echoes each record
// to the console with a printk timestamp, so the console still shows
// every line. The records are also in the ring buffer, in order with
// the kernel's own, where the liken-logs relay reads them into the
// cluster. A record written through /dev/kmsg has syslog facility 1 and
// a kernel record has facility 0, so the relay tells them apart by that
// field and not by the text of the line.
//
// The redirect replaces os.Stdout and os.Stderr, which fmt.Printf reads
// on each call, so no call site changes. They become the write ends of
// two pipes, and one goroutine for each pipe copies complete lines into
// /dev/kmsg. File descriptors 1 and 2 stay on the console that the
// kernel opened for init, because the Go runtime writes a panic report
// to fd 2 directly, and a panic in PID 1 must reach the console even
// when the redirect has failed.
//
// If a drain goroutine stopped, its pipe would fill and the next
// fmt.Printf would block PID 1 forever. So the drain goroutines are not
// machine-plane components. The plane waits out a backoff before it
// restarts a component, and it logs the failure to os.Stderr, which
// would block on the stopped goroutine's own pipe. Each drain loop only
// splits lines and writes them, and when a write fails it writes the
// line to the console directly and continues.
//
// Some lines print before /dev is mounted, so they go to the console
// only: the hello line, the PID-1 refusal, and the output of
// switch_root.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

// console is file descriptor 1 as the kernel opened it, saved before
// the redirect replaces os.Stdout. The output of child processes such
// as k3s and mke2fs goes here and not to /dev/kmsg. k3s writes enough
// to overwrite the kernel's records in the 256KiB ring buffer within
// seconds, and init already copies k3s's log file into the cluster.
var console io.Writer = os.Stdout

const (
	// Syslog priorities: facility user, with severity info for stdout
	// and warning for stderr. The log relays filter on these values,
	// so the machine package defines them for both binaries.
	kmsgInfo    = machine.FacilityUser<<3 | machine.SeverityInfo
	kmsgWarning = machine.FacilityUser<<3 | machine.SeverityWarning

	// The kernel refuses a /dev/kmsg write longer than about 1KB with
	// EINVAL and does not truncate it. 800 bytes leaves room for the
	// priority prefix and the "..." marks of a split line.
	kmsgPayloadLimit = 800
)

// Tests point these paths at files they can read back.
var (
	printkDevkmsgPath = "/proc/sys/kernel/printk_devkmsg"
	printkPath        = "/proc/sys/kernel/printk"
	kmsgPath          = "/dev/kmsg"
)

// redirectToKmsg sends init's own output to /dev/kmsg. main calls it
// after mountEssentials, which mounts /proc and /dev, and before the
// first machine-plane component starts, so no other goroutine reads
// os.Stdout or os.Stderr while it replaces them. When a step fails, it
// returns and leaves the output on the console.
func redirectToKmsg() {
	// By default the kernel rate-limits userspace writes to /dev/kmsg
	// to a small burst every few seconds, which would drop most of the
	// boot report. "on" turns the limit off for the whole machine.
	// Without it the redirect would lose lines, so a failure here stops
	// the redirect.
	if err := os.WriteFile(printkDevkmsgPath, []byte("on\n"), 0); err != nil {
		fmt.Printf("liken: init output stays on the console: writing printk_devkmsg: %v\n", err)
		return
	}
	// One number written to this file sets console_loglevel. At 7 the
	// console echoes every record except debug, so it shows init's info
	// records. A failure does not stop the redirect, because the ring
	// buffer still receives every record.
	if err := os.WriteFile(printkPath, []byte("7"), 0); err != nil {
		fmt.Printf("liken: setting the console log level: %v\n", err)
	}

	kmsg, err := os.OpenFile(kmsgPath, os.O_WRONLY, 0)
	if err != nil {
		fmt.Printf("liken: init output stays on the console: opening /dev/kmsg: %v\n", err)
		return
	}

	outR, outW, err := os.Pipe()
	if err != nil {
		fmt.Printf("liken: init output stays on the console: creating a pipe: %v\n", err)
		return
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		fmt.Printf("liken: init output stays on the console: creating a pipe: %v\n", err)
		return
	}

	go drainToKmsg(outR, kmsg, kmsgInfo)
	go drainToKmsg(errR, kmsg, kmsgWarning)
	os.Stdout = outW
	os.Stderr = errW
	fmt.Println("liken: init now writes its output to /dev/kmsg, and earlier lines went only to the console")
}

// drainToKmsg copies one pipe into the kernel log, one record per
// line. It returns only on a read error. Nothing closes the write end,
// which is os.Stdout or os.Stderr, and emitKmsgLine handles every
// write failure, so the loop runs as long as init does.
func drainToKmsg(r io.Reader, kmsg io.Writer, priority int) {
	var buf bytes.Buffer
	chunk := make([]byte, 4096)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
			for {
				line, rerr := buf.ReadBytes('\n')
				if rerr != nil {
					// Keep a line with no newline until the rest
					// arrives.
					buf.Write(line)
					break
				}
				emitKmsgLine(kmsg, priority, line[:len(line)-1])
			}
		}
		if err != nil {
			return
		}
	}
}

// emitKmsgLine writes one line as one or more kmsg records. When a
// write returns an error or panics, it writes the line to the console
// instead.
func emitKmsgLine(kmsg io.Writer, priority int, line []byte) {
	defer func() {
		if recover() != nil {
			fmt.Fprintf(console, "%s\n", line)
		}
	}()
	for _, part := range splitKmsgLine(line, kmsgPayloadLimit) {
		if _, err := fmt.Fprintf(kmsg, "<%d>%s", priority, part); err != nil {
			fmt.Fprintf(console, "%s\n", part)
		}
	}
}

// splitKmsgLine cuts a line into pieces of at most limit bytes, plus
// a trailing " ..." on each piece that has more after it and a leading
// "... " on each piece after the first. The marks show a reader of one
// record that it is part of a longer line, such as the kernel command
// line in the boot report.
func splitKmsgLine(line []byte, limit int) [][]byte {
	if len(line) <= limit {
		return [][]byte{line}
	}
	var parts [][]byte
	for start := 0; start < len(line); start += limit {
		end := min(start+limit, len(line))
		var part []byte
		if start > 0 {
			part = append(part, "... "...)
		}
		part = append(part, line[start:end]...)
		if end < len(line) {
			part = append(part, " ..."...)
		}
		parts = append(parts, part)
	}
	return parts
}

// syncLogs gives the drain goroutines time to copy the last lines into
// the kernel log. The shutdown paths call it just before a reboot or
// power-off, so the last lines, which usually give the reason for the
// shutdown, are not lost in a pipe. A pipe drains in microseconds, so
// 50ms is enough.
func syncLogs() {
	time.Sleep(50 * time.Millisecond)
}
