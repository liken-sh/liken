package main

// Init records why the previous boot ended in a kernel panic or oops.
//
// The serial console shows the trace, but nothing else keeps it: the
// panic=10 kernel argument reboots the machine ten seconds later, the
// firmware falls back to the proven slot, and the machine comes back
// healthy with no record of the crash.
//
// pstore is the kernel's mechanism for this. On a panic or an oops
// the kernel writes the tail of its log to a platform store that
// survives the reboot. On UEFI machines that store is the firmware's
// variable memory, through the efi-pstore module that
// image/etc/liken/modules.conf loads early in boot. On the next boot
// the kernel shows each record as one file under /sys/fs/pstore. A
// panic before efi-pstore loads leaves no record.
//
// Each boot copies those files to the crash directory of machineState
// and then deletes them from pstore. The copy must be on disk before
// the delete, because deleting a pstore file deletes the firmware
// variable, which is the only copy of the record. The records must
// leave the store because firmware variable memory is a few hundred
// kilobytes, shared with the boot entries, and a full store has no
// room for the next crash.
//
// Each boot builds status.lastCrash again from the copied records, so
// an operator who erases a Machine's status gets the summary back on
// the next boot (the reconstructibility rule in machine/status.go).

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// pstoreDir is where the kernel exposes the platform store. It is a
// variable so that tests can point it at a directory of fake records.
var pstoreDir = "/sys/fs/pstore"

const (
	// crashKeep is the number of crash directories that pruning keeps.
	// Crashes are not removed by age: the newest one stays in
	// status.lastCrash with its timestamp however old it is.
	crashKeep = 10

	// crashRecordCap is the most bytes that init reads from one
	// record. A real record is at most the kernel's 10 KiB dump size,
	// so a larger one means a broken backend, and PID 1 must not read
	// it without limit.
	crashRecordCap = 1 << 20

	// crashMessageCap is the longest summary message, the maxLength
	// of the field in the CRD schema. The records keep the full text.
	crashMessageCap = 1024

	// crashDirFormat names a crash directory by its time in UTC.
	// Names sort in time order, and they have no colons, so they are
	// valid on FAT.
	crashDirFormat = "20060102T150405Z"

	// crashGroupWindow is the largest gap between the timestamps of
	// two records of one dump. The kernel writes all parts of a dump
	// at the same moment. The ordinal in the header restarts at 1 on
	// every boot, so only the time tells a Panic#1 of this boot from
	// one of an earlier boot.
	crashGroupWindow = 5 * time.Second
)

// crashStore returns the directory in machineState that holds one
// subdirectory per preserved crash batch, named by crashDirFormat.
func crashStore(stateDir string) string {
	return filepath.Join(stateDir, "crash")
}

// mountPstore mounts the pstore filesystem on pstoreDir. The kernel
// creates the mount point when pstore is built in, and EBUSY means it
// is already mounted. The mount lists the records of every backend
// that registered before it, so efi-pstore can load first.
func mountPstore() {
	err := unix.Mount("pstore", pstoreDir, "pstore",
		unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "")
	switch {
	case err == nil:
		fmt.Printf("liken: mounted pstore on %s\n", pstoreDir)
	case errors.Is(err, unix.EBUSY):
	default:
		fmt.Fprintf(os.Stderr, "liken: mounting pstore on %s: %v\n", pstoreDir, err)
	}
}

// pstoreRecord is one file of the store. time is the file's mtime,
// which pstore sets to the wall-clock time of the dump. The reason,
// ordinal, part, and body fields are set only when the file is a
// kmsg dump that parses. A record that does not parse is still
// copied, but no summary is built from it.
type pstoreRecord struct {
	name      string
	time      time.Time
	reason    string
	ordinal   int
	part      int
	body      string
	raw       []byte
	parseable bool
}

// readPstoreRecords reads every file in a directory as a record. It
// reads the pstore mount and a preserved crash directory the same
// way, because the copy keeps names, bytes, and mtimes.
func readPstoreRecords(dir string) []pstoreRecord {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var recs []pstoreRecord
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: crash: reading pstore record %s: %v\n", e.Name(), err)
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(f, crashRecordCap))
		_ = f.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: crash: reading pstore record %s: %v\n", e.Name(), err)
			continue
		}
		rec := pstoreRecord{name: e.Name(), time: info.ModTime(), raw: raw}
		// A .enc.z suffix marks a dump that the kernel stored
		// compressed. It is copied as it is and not parsed.
		if strings.HasPrefix(rec.name, "dmesg-") && !strings.HasSuffix(rec.name, ".enc.z") {
			rec.reason, rec.ordinal, rec.part, rec.body, rec.parseable = parseDumpHeader(raw)
		}
		recs = append(recs, rec)
	}
	return recs
}

// dumpHeaderRE matches the first line of a kmsg dump, such as
// "Panic#1 Part3": the reason, an ordinal that counts dumps since
// boot, and the part number. The kernel chooses the reason words, and
// this configuration dumps only Panic and Oops.
var dumpHeaderRE = regexp.MustCompile(`^([A-Za-z]+)#(\d+) Part(\d+)$`)

// parseDumpHeader splits a record into the fields of its header and
// its body. ok is false when the first line is not a dump header. A
// record from the ramoops backend starts with a "====" timestamp
// line before the header, which this function skips.
func parseDumpHeader(data []byte) (reason string, ordinal, part int, body string, ok bool) {
	text := string(data)
	if strings.HasPrefix(text, "====") {
		if _, rest, found := strings.Cut(text, "\n"); found {
			text = rest
		}
	}
	header, rest, _ := strings.Cut(text, "\n")
	m := dumpHeaderRE.FindStringSubmatch(strings.TrimRight(header, "\r"))
	if m == nil {
		return "", 0, 0, "", false
	}
	ordinal, _ = strconv.Atoi(m[2])
	part, _ = strconv.Atoi(m[3])
	return m[1], ordinal, part, rest, true
}

// crashGroup is every part of one kmsg dump.
type crashGroup struct {
	reason  string
	ordinal int
	time    time.Time
	parts   []pstoreRecord
}

// text joins the parts into the log in time order. The kernel writes
// the newest lines to part 1, including the panic message, and older
// lines to the higher parts, so the parts are joined from the highest
// number down.
func (g crashGroup) text() string {
	parts := slices.Clone(g.parts)
	slices.SortFunc(parts, func(a, b pstoreRecord) int { return b.part - a.part })
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.body)
		if !strings.HasSuffix(p.body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// groupCrashes groups the parseable records into crashes, newest
// first. Parts of one dump have the same reason and ordinal and were
// written within crashGroupWindow of each other.
func groupCrashes(recs []pstoreRecord) []crashGroup {
	sorted := slices.Clone(recs)
	slices.SortFunc(sorted, func(a, b pstoreRecord) int { return b.time.Compare(a.time) })
	var groups []crashGroup
	for _, rec := range sorted {
		if !rec.parseable {
			continue
		}
		joined := false
		for i := range groups {
			g := &groups[i]
			if g.reason == rec.reason && g.ordinal == rec.ordinal &&
				g.time.Sub(rec.time) <= crashGroupWindow {
				g.parts = append(g.parts, rec)
				joined = true
				break
			}
		}
		if !joined {
			groups = append(groups, crashGroup{
				reason:  rec.reason,
				ordinal: rec.ordinal,
				time:    rec.time,
				parts:   []pstoreRecord{rec},
			})
		}
	}
	return groups
}

// syslogPrefixRE matches the "<level>[ timestamp] " prefix of each
// line in a kmsg dump.
var syslogPrefixRE = regexp.MustCompile(`^<\d+>\[\s*\d+\.\d+\] ?`)

// crashMessage returns the line of the dump that best says what
// happened. It prefers the "Kernel panic" line, then a "BUG:" line,
// then an "Oops:" line, and otherwise the first non-empty line. The
// kernel can write arbitrary bytes into a panic message, and the
// result is written to a YAML file and to the Kubernetes API, so the
// result has only printable characters and is at most crashMessageCap
// bytes.
func crashMessage(text string) string {
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		line = syslogPrefixRE.ReplaceAllString(strings.TrimRight(line, "\r"), "")
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	for _, marker := range []string{"Kernel panic - not syncing:", "BUG:", "Oops:"} {
		for _, line := range lines {
			if idx := strings.Index(line, marker); idx >= 0 {
				return sanitizeCrashMessage(line[idx:])
			}
		}
	}
	if len(lines) > 0 {
		return sanitizeCrashMessage(lines[0])
	}
	return ""
}

// sanitizeCrashMessage removes invalid UTF-8 and unprintable
// characters, turns tabs into spaces, and cuts the string to
// crashMessageCap bytes without splitting a character.
func sanitizeCrashMessage(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, ""))
	for len(s) > crashMessageCap {
		_, size := lastRune(s)
		s = s[:len(s)-size]
	}
	return strings.TrimSpace(s)
}

// lastRune returns the last character of s and its width in bytes. s
// must not be empty.
func lastRune(s string) (rune, int) {
	r := []rune(s)
	last := r[len(r)-1]
	return last, len(string(last))
}

// crashSummary builds the status.lastCrash value for one crash.
// records is the directory that holds the full text.
func crashSummary(g crashGroup, records string) *machine.CrashStatus {
	t := g.time
	return &machine.CrashStatus{
		Time:    &t,
		Reason:  machine.CrashReason(g.reason),
		Message: crashMessage(g.text()),
		Records: records,
	}
}

// preserveCrashRecords copies every record into dest, with the same
// bytes and mtime. It syncs the files and the directories to disk
// before it returns, because the caller then erases the originals.
// It overwrites files that already exist in dest, because a previous
// boot can have stopped partway through the copy, and a directory
// that exists does not show that it is complete.
func preserveCrashRecords(recs []pstoreRecord, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for _, rec := range recs {
		path := filepath.Join(dest, rec.name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := f.Write(rec.raw); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if err := os.Chtimes(path, rec.time, rec.time); err != nil {
			return err
		}
	}
	// Without a sync of the directories, a power loss can remove the
	// new directory together with the files in it.
	for _, d := range []string{dest, filepath.Dir(dest)} {
		if err := syncDirectory(d); err != nil {
			return err
		}
	}
	return nil
}

// clearPstore deletes the files of recs from dir, which also deletes
// the firmware variables behind them. When a delete fails, it prints
// the error and goes on. The record is still in the store on the
// next boot, and settleCrashRecords copies and clears it again.
func clearPstore(dir string, recs []pstoreRecord) {
	for _, rec := range recs {
		if err := os.Remove(filepath.Join(dir, rec.name)); err != nil {
			fmt.Fprintf(os.Stderr, "liken: crash: deleting pstore record %s: %v\n", rec.name, err)
		}
	}
}

// pruneCrashStore deletes all but the newest keep crash directories.
// It orders them by name, which is time order because of
// crashDirFormat.
func pruneCrashStore(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	slices.Reverse(names)
	for _, name := range names[min(keep, len(names)):] {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			fmt.Fprintf(os.Stderr, "liken: crash: deleting crash directory %s: %v\n", name, err)
		}
	}
}

// latestPreservedCrash returns the summary of the newest copied crash
// that has a parseable record, or nil.
func latestPreservedCrash(store string) *machine.CrashStatus {
	entries, err := os.ReadDir(store)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	slices.Reverse(names)
	for _, name := range names {
		dir := filepath.Join(store, name)
		if groups := groupCrashes(readPstoreRecords(dir)); len(groups) > 0 {
			return crashSummary(groups[0], dir)
		}
	}
	return nil
}

// settleCrashRecords returns the summary of the newest crash in the
// crash store or in pstore, or nil when there is none.
//
// When durable is true, it copies the records from pstore to the crash
// store and then clears pstore. When durable is false, machineState is
// in memory, so pstore is the only place where a record survives a
// reboot. It keeps the newest crash in pstore and deletes the older
// ones, because the kernel fails without a message when it cannot fit
// the next dump.
func settleCrashRecords(stateDir string, durable bool) *machine.CrashStatus {
	store := crashStore(stateDir)
	recs := readPstoreRecords(pstoreDir)
	groups := groupCrashes(recs)

	// inPstore is the summary of the crash whose records stay in
	// pstore, either because durable is false or because the copy
	// failed.
	var inPstore *machine.CrashStatus
	if len(recs) > 0 {
		if durable {
			dest := filepath.Join(store, newestRecordTime(recs).UTC().Format(crashDirFormat))
			if err := preserveCrashRecords(recs, dest); err != nil {
				fmt.Fprintf(os.Stderr, "liken: crash: copying pstore records to %s: %v (the records stay in pstore)\n", dest, err)
				if len(groups) > 0 {
					inPstore = crashSummary(groups[0], pstoreDir)
				}
			} else {
				fmt.Printf("liken: crash: copied %d pstore records to %s\n", len(recs), dest)
				clearPstore(pstoreDir, recs)
			}
		} else {
			for _, g := range groups[min(1, len(groups)):] {
				clearPstore(pstoreDir, g.parts)
			}
			if len(groups) > 0 {
				inPstore = crashSummary(groups[0], pstoreDir)
			}
		}
	}

	var summary *machine.CrashStatus
	if durable {
		pruneCrashStore(store, crashKeep)
		summary = latestPreservedCrash(store)
	}
	if summary == nil || (inPstore != nil && inPstore.Time.After(*summary.Time)) {
		summary = inPstore
	}

	if summary != nil {
		// Print the summary to the console too, for a person at the
		// serial port.
		fmt.Printf("liken: crash: last kernel %s at %s: %s (records: %s)\n",
			summary.Reason, summary.Time.UTC().Format(time.RFC3339),
			summary.Message, summary.Records)
	}
	return summary
}

// newestRecordTime returns the latest mtime of recs, which is the
// time in the name of the batch's directory. recs must not be empty.
func newestRecordTime(recs []pstoreRecord) time.Time {
	newest := recs[0].time
	for _, rec := range recs[1:] {
		if rec.time.After(newest) {
			newest = rec.time
		}
	}
	return newest
}
