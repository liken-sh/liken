package main

// What killed the machine, answered by the machine itself.
//
// A kernel panic is the one failure the rest of liken's observability
// cannot see. The trace goes to the serial console, the baked
// panic=10 argument reboots the machine ten seconds later, and the
// firmware falls back to the proven slot. The machine comes back
// healthy, and nothing anywhere says why it went down.
//
// pstore is the kernel's answer. At the moment of a panic or an
// oops, the kernel writes the tail of its own log to a platform
// store that survives the reboot. On UEFI machines that store is the
// firmware's variable memory, through the efi_pstore backend that
// the image's fixed module list loads early in every boot. On the
// next boot, the kernel serves whatever the store holds as plain
// files under /sys/fs/pstore, one file per record.
//
// This file is the boot step that reads those files. It preserves
// them under machineState's crash store, because firmware variable
// memory is a few hundred kilobytes shared with the boot entries: a
// journal that small must be emptied after every read, or the next
// crash finds no room to record itself. It then derives a one-line
// summary, the newest crash's time, reason, and the kernel's own
// message, and hands it to the facts tree, which is how the fact
// becomes status.lastCrash in the cluster.
//
// Two rules shape everything here. First, the copy must land before
// the clear: deleting a pstore file erases the backing firmware
// variable, the only copy of the evidence. Second, every boot
// re-derives the summary from the preserved records, never from
// memory of an earlier boot, so an erased Machine status rebuilds
// exactly (status.go's reconstructibility rule).
//
// One gap is inherent: a panic that lands before the fixed module
// list loads leaves no record, because the backend was not yet
// registered. The window is a few hundred milliseconds at the top
// of boot.

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

	"github.com/liken-sh/liken/machine"
)

// pstoreDir is where the kernel serves the platform store's records.
// A variable rather than a constant, so tests can stand up a
// directory of fake records.
var pstoreDir = "/sys/fs/pstore"

const (
	// crashKeep bounds the crash store. A crash is a dozen kilobytes,
	// so this bound is about tidiness, not space. There is no age
	// bound: an old crash stays on record, and its timestamp says how
	// old the news is.
	crashKeep = 10

	// crashRecordCap bounds one record read. A real record is at most
	// the kernel's 10 KiB dump budget; a backend broken enough to
	// serve more must not feed PID 1 unbounded bytes.
	crashRecordCap = 1 << 20

	// crashMessageCap bounds the summary message, matching the CRD
	// schema's maxLength. The full text stays in the records.
	crashMessageCap = 1024

	// crashDirFormat names one crash's directory by its moment, in
	// compact UTC. No colons, so the name would survive even a copy
	// onto FAT media.
	crashDirFormat = "20060102T150405Z"

	// crashGroupWindow is how close two records' timestamps must be
	// to belong to one dump. Parts of one dump are written in the
	// same instant; the kernel's per-boot ordinals repeat across
	// boots, so time is what separates this boot's Panic#1 from last
	// month's.
	crashGroupWindow = 5 * time.Second
)

// crashStore is where machineState keeps preserved crashes: one
// directory per crash, named by crashDirFormat.
func crashStore(stateDir string) string {
	return filepath.Join(stateDir, "crash")
}

// mountPstore mounts the platform store's filesystem. The kernel
// creates the mount point itself when pstore is built in. EBUSY
// means something already mounted it, which serves the same purpose.
// The records are static, so mounting after a backend registers
// loses nothing: the filesystem populates from whatever the store
// holds.
func mountPstore() {
	err := unix.Mount("pstore", pstoreDir, "pstore",
		unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "")
	switch {
	case err == nil:
		fmt.Printf("liken: mounted pstore on %s\n", pstoreDir)
	case errors.Is(err, unix.EBUSY):
	default:
		fmt.Fprintf(os.Stderr, "liken: mounting pstore: %v\n", err)
	}
}

// pstoreRecord is one file from the store: its exact bytes, its
// mtime (which pstore stamps with the wall clock at the moment of
// the dump), and, when the file is a readable kmsg dump, the parsed
// header. Records that do not parse still preserve; they are
// evidence, just not summarizable.
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

// readPstoreRecords reads every record in a directory. It works on
// the live pstore mount and on a preserved crash directory alike,
// because preservation keeps names, bytes, and mtimes.
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
			fmt.Fprintf(os.Stderr, "liken: crash: reading %s: %v\n", e.Name(), err)
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(f, crashRecordCap))
		_ = f.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: crash: reading %s: %v\n", e.Name(), err)
			continue
		}
		rec := pstoreRecord{name: e.Name(), time: info.ModTime(), raw: raw}
		// Only plain dmesg records carry a parseable dump. The
		// .enc.z suffix marks a record the kernel could not
		// decompress; its payload is raw deflate, preserved verbatim
		// and never parsed.
		if strings.HasPrefix(rec.name, "dmesg-") && !strings.HasSuffix(rec.name, ".enc.z") {
			rec.reason, rec.ordinal, rec.part, rec.body, rec.parseable = parseDumpHeader(raw)
		}
		recs = append(recs, rec)
	}
	return recs
}

// dumpHeaderRE matches the first line the kernel writes into every
// kmsg dump: the reason word, a per-boot ordinal, and the part
// number, as in "Panic#1 Part3". The reason vocabulary belongs to
// the kernel; Panic and Oops are the two words this configuration
// dumps.
var dumpHeaderRE = regexp.MustCompile(`^([A-Za-z]+)#(\d+) Part(\d+)$`)

// parseDumpHeader splits a record into its header and its body. A
// ramoops zone opens with its own "====" timestamp line ahead of the
// header; tolerating it costs one comparison and keeps the parser
// honest against every backend this kernel can register.
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

// crashGroup is one crash: every part of one kmsg dump.
type crashGroup struct {
	reason  string
	ordinal int
	time    time.Time
	parts   []pstoreRecord
}

// text reassembles the dump in chronological order. The kernel hands
// the log tail out newest-first, so part 1 holds the newest lines,
// including the panic message itself, and higher parts hold
// progressively older lines. Reading time order means reading parts
// in descending number.
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

// groupCrashes folds parseable records into crashes, newest first.
// Parts of one dump share a reason and an ordinal and were written
// in the same instant. The ordinal alone cannot identify a dump,
// because it counts per boot and restarts at one, so the timestamp
// window is what keeps two boots' first panics apart.
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

// syslogPrefixRE strips the "<level>[ timestamp] " that kmsg dumps
// carry on every body line.
var syslogPrefixRE = regexp.MustCompile(`^<\d+>\[\s*\d+\.\d+\] ?`)

// crashMessage picks the one line that says what happened, in the
// kernel's own words: the panic line when there is one, the oops's
// BUG line otherwise, then the oops code line, then the first line
// of the dump. Panic text can carry arbitrary bytes, and this string
// travels into a YAML file and a Kubernetes API write, so it leaves
// here printable and bounded or not at all.
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

// sanitizeCrashMessage drops everything unprintable and cuts the
// string at the cap, on a rune boundary.
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

// lastRune reports the final rune of a non-empty string and its
// width in bytes.
func lastRune(s string) (rune, int) {
	r := []rune(s)
	last := r[len(r)-1]
	return last, len(string(last))
}

// crashSummary condenses one crash into the status stub, with the
// records field naming where the full text lives.
func crashSummary(g crashGroup, records string) *machine.CrashStatus {
	t := g.time
	return &machine.CrashStatus{
		Time:    &t,
		Reason:  machine.CrashReason(g.reason),
		Message: crashMessage(g.text()),
		Records: records,
	}
}

// preserveCrashRecords copies every record, verbatim and with its
// crash-time mtime, into one directory per crash batch. The copies
// and their directory sync to disk before this function returns,
// because the caller's next step erases the originals. A directory
// that already exists means a prior boot preserved this same batch
// but died before clearing the store, so the copy is already safe
// and only the clear needs to happen again.
func preserveCrashRecords(recs []pstoreRecord, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
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
	dir, err := os.Open(dest)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// clearPstore erases records from the platform store. Unlinking a
// pstore file is what deletes the backing firmware variable. A
// failed erase is reported and left; the record comes back next
// boot, and the preserve step's already-exists rule keeps the retry
// cheap.
func clearPstore(dir string, recs []pstoreRecord) {
	for _, rec := range recs {
		if err := os.Remove(filepath.Join(dir, rec.name)); err != nil {
			fmt.Fprintf(os.Stderr, "liken: crash: clearing %s: %v\n", rec.name, err)
		}
	}
}

// pruneCrashStore keeps the newest crashes and removes the rest. The
// directory names are timestamps in a sortable format, so name order
// is time order.
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
			fmt.Fprintf(os.Stderr, "liken: crash: pruning %s: %v\n", name, err)
		}
	}
}

// latestPreservedCrash re-derives the summary from the crash store:
// the newest preserved crash that parses. This runs on every boot,
// crash or no crash, and is what makes status.lastCrash
// reconstructible; the store is the fact, and the status is only a
// reading of it.
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

// settleCrashRecords is the boot step: read the platform store,
// preserve and clear it when machineState is durable, and report the
// newest crash on record. On a machine whose machineState fell back
// to memory, the platform store is the only durable storage there
// is, so the records stay in it; only crashes older than the newest
// leave, to keep the firmware's small variable memory from filling
// and silently blocking the next dump.
func settleCrashRecords(stateDir string, durable bool) *machine.CrashStatus {
	store := crashStore(stateDir)
	recs := readPstoreRecords(pstoreDir)
	groups := groupCrashes(recs)

	// fresh is the summary of records that remain in pstore, either
	// because this machine has nowhere better to keep them, or
	// because preserving them failed.
	var fresh *machine.CrashStatus
	if len(recs) > 0 {
		if durable {
			dest := filepath.Join(store, newestRecordTime(recs).UTC().Format(crashDirFormat))
			if err := preserveCrashRecords(recs, dest); err != nil {
				fmt.Fprintf(os.Stderr, "liken: crash: preserving records: %v (they stay in pstore)\n", err)
				if len(groups) > 0 {
					fresh = crashSummary(groups[0], pstoreDir)
				}
			} else {
				fmt.Printf("liken: crash: preserved %d pstore records to %s\n", len(recs), dest)
				clearPstore(pstoreDir, recs)
			}
		} else {
			for _, g := range groups[min(1, len(groups)):] {
				clearPstore(pstoreDir, g.parts)
			}
			if len(groups) > 0 {
				fresh = crashSummary(groups[0], pstoreDir)
			}
		}
	}

	var summary *machine.CrashStatus
	if durable {
		pruneCrashStore(store, crashKeep)
		summary = latestPreservedCrash(store)
	}
	if summary == nil || (fresh != nil && fresh.Time.After(*summary.Time)) {
		summary = fresh
	}

	if summary != nil {
		// Console parity: the fact prints where an operator at the
		// serial port can see it, in the same words status carries.
		fmt.Printf("liken: crash: last kernel %s at %s: %s (records: %s)\n",
			summary.Reason, summary.Time.UTC().Format(time.RFC3339),
			summary.Message, summary.Records)
	}
	return summary
}

// newestRecordTime is the batch's moment: the newest record's mtime,
// which names the batch's directory in the crash store.
func newestRecordTime(recs []pstoreRecord) time.Time {
	newest := recs[0].time
	for _, rec := range recs[1:] {
		if rec.time.After(newest) {
			newest = rec.time
		}
	}
	return newest
}
