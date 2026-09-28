package main

// Lifting is the narrow part of parsing that the relay is allowed to
// do. It recognizes the timestamp-and-level header that a log format
// puts at the front of every line, copies those two facts into the
// envelope, and leaves the line itself untouched. The k3s and
// containerd logs mix two such formats. k3s itself and containerd
// log through logrus (time="..." level=... msg=...). The Kubernetes
// components embedded in k3s log through klog, whose header is a
// single severity letter, the month and day, and a wall-clock time
// (I0707 13:51:16.123456 ...). A line that matches neither format
// ships with the relay's own observation time and info severity.
// This is exact for a line just written, and wrong only for
// unliftable lines replayed long after the fact.
//
// This file uses hand-written scanners instead of regular
// expressions, because each format is a fixed prefix. A handful of
// index checks is clearer to read than a pattern, runs on every
// single log line, and cannot backtrack.

import (
	"strings"
	"time"
)

// lift extracts the event time and severity word from a line's
// header. It tries logrus first, because k3s's own lines make up
// most of the volume, then tries klog, then falls back to the
// observation time.
func lift(line string, now time.Time) (time.Time, string) {
	if when, severity, ok := liftLogrus(line); ok {
		return when, severity
	}
	if when, severity, ok := liftKlog(line, now); ok {
		return when, severity
	}
	return now, "info"
}

// logrusSeverities maps logrus's level words onto syslog severity
// words. Syslog has no trace, so trace joins debug.
var logrusSeverities = map[string]string{
	"panic":   "emerg",
	"fatal":   "crit",
	"error":   "err",
	"warning": "warning",
	"warn":    "warning",
	"info":    "info",
	"debug":   "debug",
	"trace":   "debug",
}

// liftLogrus recognizes logrus's text format, which always starts
// with time="<RFC3339>" level=<word>. Any byte out of place means
// this line is not a logrus line, and the function abandons the
// whole lift instead of applying it halfway.
func liftLogrus(line string) (time.Time, string, bool) {
	const timePrefix = `time="`
	if len(line) < len(timePrefix) || line[:len(timePrefix)] != timePrefix {
		return time.Time{}, "", false
	}
	rest := line[len(timePrefix):]
	quote := strings.IndexByte(rest, '"')
	if quote < 0 {
		return time.Time{}, "", false
	}
	when, err := time.Parse(time.RFC3339, rest[:quote])
	if err != nil {
		return time.Time{}, "", false
	}

	const levelPrefix = ` level=`
	rest = rest[quote+1:]
	if len(rest) < len(levelPrefix) || rest[:len(levelPrefix)] != levelPrefix {
		return time.Time{}, "", false
	}
	rest = rest[len(levelPrefix):]
	end := strings.IndexByte(rest, ' ')
	if end < 0 {
		end = len(rest)
	}
	severity, ok := logrusSeverities[rest[:end]]
	if !ok {
		return time.Time{}, "", false
	}
	return when, severity, true
}

// klogSeverities maps klog's single-letter severities (Info, Warning,
// Error, Fatal) onto syslog words.
var klogSeverities = map[byte]string{
	'I': "info",
	'W': "warning",
	'E': "err",
	'F': "crit",
}

// liftKlog recognizes klog's header: Lmmdd hh:mm:ss.uuuuuu, fixed
// width, with no year. The function takes the year from the
// observation clock, with one correction. A December line read in
// January would otherwise land eleven months in the future, so
// anything more than a day ahead of now is moved back a year. Log
// lines from the future are otherwise impossible. Log lines from
// months ago are simply from an old file.
func liftKlog(line string, now time.Time) (time.Time, string, bool) {
	// Lmmdd hh:mm:ss.uuuuuu: 21 bytes before the rest of the header.
	const headerLen = 21
	if len(line) < headerLen {
		return time.Time{}, "", false
	}
	severity, ok := klogSeverities[line[0]]
	if !ok {
		return time.Time{}, "", false
	}
	for _, i := range []int{1, 2, 3, 4, 6, 7, 9, 10, 12, 13, 15, 16, 17, 18, 19, 20} {
		if line[i] < '0' || line[i] > '9' {
			return time.Time{}, "", false
		}
	}
	if line[5] != ' ' || line[8] != ':' || line[11] != ':' || line[14] != '.' {
		return time.Time{}, "", false
	}

	digits := func(from, to int) int {
		n := 0
		for _, c := range []byte(line[from:to]) {
			n = n*10 + int(c-'0')
		}
		return n
	}
	month := digits(1, 3)
	day := digits(3, 5)
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}, "", false
	}
	when := time.Date(now.Year(), time.Month(month), day,
		digits(6, 8), digits(9, 11), digits(12, 14), digits(15, 21)*1_000,
		time.UTC)
	if when.After(now.Add(24 * time.Hour)) {
		when = when.AddDate(-1, 0, 0)
	}
	return when, severity, true
}
