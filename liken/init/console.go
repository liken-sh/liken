package main

// init's own log lines go to /dev/kmsg, the kernel's log buffer,
// instead of straight to the serial console. The kernel echoes every
// buffered record to the console anyway, with its printk timestamps
// added, which the raw writes never had. So the console reads as it
// always did. What changes is that init's lines now also exist
// somewhere else too: as structured records in the ring buffer,
// interleaved with the kernel's own records in true order, where the
// liken-logs relay can read them into the cluster. Records written
// through /dev/kmsg carry syslog facility 1, where the kernel's
// records carry facility 0. So the two streams separate by a field,
// instead of by guessing from prefixes.
//
// The mechanism reassigns the os.Stdout and os.Stderr package
// variables, which every fmt.Printf in this program reads at each
// call. This gives one change point, with no call sites touched. The
// variables point at the write ends of two pipes, and a goroutine
// per pipe carries complete lines into /dev/kmsg. The underlying
// file descriptors 1 and 2 are deliberately left alone, still aimed
// at the console the kernel opened for init: the Go runtime writes
// panic reports to fd 2 directly, and a panic in PID 1 must reach
// the console even when everything built here has stopped working.
//
// The pipes create this program's sharpest remaining risk. If a
// drainer goroutine ever died, its pipe would fill, and the next
// fmt.Printf would block forever, blocking PID 1. That is why the
// drainers are not machine-plane components: a component restart
// waits out a backoff, and the plane logs failures to os.Stderr,
// which would deadlock into the dead drainer's own pipe. For the
// same reason, each drainer's loop does nothing but split lines and
// write them, and any failure inside falls back to writing the
// console directly, rather than returning.
//
// A few lines per exec print before /dev exists and cannot go to
// kmsg: the hello line, the PID-1 refusal, and switch_root's
// messages. These lines stay console-only and are not shipped
// anywhere else, because there is nowhere else for them to go yet.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/liken-sh/liken/machine"
)

// console is the raw serial console: file descriptor 1, exactly as
// the kernel opened it, captured before any reassignment. Output
// from child processes, for example k3s and mke2fs, writes here
// directly, and bypasses kmsg. At the volume k3s produces, the
// 256KiB ring buffer would fill and discard the kernel's own records
// within seconds. Also, the code already tails k3s's output into the
// cluster from its log file, so sending it through kmsg too would
// ship every line twice.
var console io.Writer = os.Stdout

const (
	// Syslog priorities for init's two streams: the userspace
	// facility shifted past the three severity bits, plus info for
	// stdout and warning for stderr. The facility and severity
	// numbers form the wire contract with the log relays, so they
	// live in the machine package that both binaries import.
	kmsgInfo    = machine.FacilityUser<<3 | machine.SeverityInfo
	kmsgWarning = machine.FacilityUser<<3 | machine.SeverityWarning

	// The kernel rejects /dev/kmsg writes longer than about 1KB
	// (LOG_LINE_MAX) with EINVAL; it does not truncate them. 800
	// bytes of payload leaves room for the priority prefix and the
	// continuation marks around split chunks.
	kmsgPayloadLimit = 800
)

// redirectToKmsg points init's own output at /dev/kmsg. It runs
// after mountEssentials, because it needs /proc/sys and /dev/kmsg,
// and before the first machine-plane component starts, so the
// package variables are reassigned while main is still the only
// goroutine that reads them. Any failure stops the whole redirect
// and leaves the direct console writes in place: a machine that
// cannot buffer its logs still reports its boot on the console.
func redirectToKmsg() {
	// The kernel rate-limits userspace kmsg writes by default, to a
	// small burst every few seconds, which would silently discard
	// most of the boot report. This sysctl turns rate-limiting off
	// for the whole machine. It must run first, and it is required,
	// not a tuning option: without it, the redirect would lose lines
	// that the console used to show.
	if err := os.WriteFile("/proc/sys/kernel/printk_devkmsg", []byte("on\n"), 0); err != nil {
		fmt.Printf("liken: logs stay on the console: printk_devkmsg: %v\n", err)
		return
	}
	// Assert that the console echoes severity-6 records: writing a
	// single number to this file sets console_loglevel, and 7 means
	// everything below debug prints. The kernel's default config
	// already sets 7; this guards against a quiet= setting in a
	// future config. This is best effort, because the echo is a
	// convenience while the buffer is the record of truth.
	if err := os.WriteFile("/proc/sys/kernel/printk", []byte("7"), 0); err != nil {
		fmt.Printf("liken: setting console loglevel: %v\n", err)
	}

	kmsg, err := os.OpenFile("/dev/kmsg", os.O_WRONLY, 0)
	if err != nil {
		fmt.Printf("liken: logs stay on the console: /dev/kmsg: %v\n", err)
		return
	}

	outR, outW, err := os.Pipe()
	if err != nil {
		fmt.Printf("liken: logs stay on the console: pipe: %v\n", err)
		return
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		fmt.Printf("liken: logs stay on the console: pipe: %v\n", err)
		return
	}

	go drainToKmsg(outR, kmsg, kmsgInfo)
	go drainToKmsg(errR, kmsg, kmsgWarning)
	os.Stdout = outW
	os.Stderr = errW
	fmt.Println("liken: init logs via /dev/kmsg from here on (earlier lines were console-only)")
}

// drainToKmsg carries one stream from its pipe into the kernel's
// buffer, one record per line. The loop must never end while the
// machine runs; the write end is os.Stdout or os.Stderr, and the
// code never closes it. The loop must never let a failure stop it
// either. emitKmsgLine absorbs everything, so the only exit is a
// read error, and a read error can only mean the process is dying
// anyway.
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
					// No newline yet. Put the fragment back and
					// wait for the rest.
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

// emitKmsgLine writes one line as one or more kmsg records, and
// cannot fail. A write error, or anything worse, sends the line to
// the raw console instead, because losing a log line is acceptable
// only after both destinations have refused it.
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

// splitKmsgLine cuts a line into pieces the kernel will accept as
// records, and marks the cuts. A piece that continues gets a
// trailing " ...", and a piece that continues another piece gets a
// leading "... ". This lets a reader of any one record tell that it
// is looking at a fragment. Most lines fit in one untouched piece;
// the lines that do not fit are things like the kernel command line
// echo in the world report.
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

// syncLogs pauses long enough for the drainers to move the last
// lines out of the pipes and into the kernel's buffer. The shutdown
// paths call it just before rebooting or powering off, so the final
// lines, usually the explanation of why the machine is going down,
// reach the kernel's buffer instead of being lost in a pipe. The
// pipes drain in microseconds, so 50ms is a generous bound. When the
// redirect never happened, the pause costs nothing that matters next
// to a reboot.
func syncLogs() {
	time.Sleep(50 * time.Millisecond)
}
