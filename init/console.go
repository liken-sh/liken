package main

// init's own log lines go to /dev/kmsg, the kernel's log buffer,
// rather than straight to the serial console. The kernel echoes
// every buffered record to the console anyway (with its printk
// timestamps, which the raw writes never had), so the console reads
// as it always did; what changes is that init's lines now also
// *exist* somewhere: as structured records in the ring buffer,
// interleaved with the kernel's own in true order, where the
// liken-logs relay can read them into the cluster. Records written
// through /dev/kmsg carry syslog facility 1 where the kernel's carry
// facility 0, so the two streams separate by a field instead of by
// guessing from prefixes.
//
// The mechanism is a reassignment of the os.Stdout and os.Stderr
// package variables, which every fmt.Printf in this program reads at
// each call: one change point, no touched call sites. The variables
// point at the write ends of two pipes, and a goroutine per pipe
// carries complete lines into /dev/kmsg. The underlying file
// descriptors 1 and 2 are deliberately left alone, still aimed at
// the console the kernel opened for us: the Go runtime writes panic
// reports to fd 2 directly, and a panic in PID 1 must reach the
// console even if everything built here is wedged.
//
// The pipes introduce this program's sharpest residual risk: if a
// drainer goroutine ever died, its pipe would fill and the next
// fmt.Printf would block forever, wedging PID 1. That is why the
// drainers are not machine-plane components (a component restart
// waits out a backoff, and the plane logs failures to os.Stderr,
// which would deadlock into the dead drainer's own pipe), why their
// loop is nothing but a line splitter and a write, and why any
// failure inside falls back to writing the console directly rather
// than returning.
//
// A few lines per exec are printed before /dev exists and cannot go
// to kmsg: the hello, the PID-1 refusal, and switch_root's
// narration. They stay console-only, unshipped; there is nowhere
// else for them to go yet.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"
)

// console is the raw serial console: file descriptor 1 exactly as
// the kernel opened it, captured before any reassignment. Child
// process echo (k3s's, mke2fs's) writes here directly, bypassing
// kmsg: at k3s volumes the 256KiB ring buffer would churn in
// seconds, evicting the kernel's own records, and k3s's output is
// already tailed into the cluster from its log file, so buffering it
// would ship every line twice.
var console io.Writer = os.Stdout

const (
	// Syslog priorities for init's two streams: facility 1
	// (userspace) shifted past the three severity bits, plus
	// severity 6 (info) for stdout and 4 (warning) for stderr.
	kmsgInfo    = 1<<3 | 6
	kmsgWarning = 1<<3 | 4

	// The kernel rejects /dev/kmsg writes longer than about 1KB
	// (LOG_LINE_MAX) with EINVAL; it does not truncate. 800 bytes of
	// payload leaves room for the priority prefix and the
	// continuation marks around split chunks.
	kmsgPayloadLimit = 800
)

// redirectToKmsg points init's own output at /dev/kmsg. It runs
// after mountEssentials (it needs /proc/sys and /dev/kmsg) and
// before the first machine-plane component starts, so the package
// variables are reassigned while main is still the only goroutine
// that reads them. Every failure aborts the whole redirect and
// leaves the direct console writes in place: a machine that can't
// buffer its logs still narrates its boot.
func redirectToKmsg() {
	// The kernel rate-limits userspace kmsg writes by default (a
	// small burst per few seconds), which would silently eat most of
	// the boot report. This sysctl turns that off machine-wide. It
	// must come first, and it is load-bearing, not tuning: without
	// it the redirect would *lose* lines the console used to show.
	if err := os.WriteFile("/proc/sys/kernel/printk_devkmsg", []byte("on\n"), 0); err != nil {
		fmt.Printf("liken: logs stay on the console: printk_devkmsg: %v\n", err)
		return
	}
	// Assert that the console echoes severity-6 records: a single
	// number written to this file sets console_loglevel, and 7 means
	// everything below debug prints. The kernel's default config
	// already says 7; this guards against a quiet= future. Best
	// effort, because the echo is a convenience while the buffer is
	// the record.
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
// machine runs (the write end is os.Stdout or os.Stderr and is never
// closed), and it must never let a failure stop it; emitKmsgLine
// absorbs everything, so the only exit is a read error that can only
// mean the process is dying anyway.
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
					// No newline yet: put the fragment back and
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
// cannot fail: a write error (or anything worse) sends the line to
// the raw console instead, because losing a log line is acceptable
// only after both destinations refused it.
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
// records, marking the cuts: a piece that continues gets a trailing
// " ...", and a piece that continues another gets a leading "... ",
// so a reader of any one record can tell it is looking at a
// fragment. Most lines fit in one untouched piece; the ones that
// don't are things like the kernel command line echo in the world
// report.
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
// paths call it just before the world ends: a reboot explanation
// that died in a pipe helps nobody. The pipes drain in microseconds;
// the pause is a generous bound, and it costs a reboot almost
// nothing. When the redirect never happened this is a harmless nap.
func syncLogs() {
	time.Sleep(50 * time.Millisecond)
}
