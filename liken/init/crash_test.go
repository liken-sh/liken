package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/machine"
)

// crashT is the moment the fixture machine crashed. Tests that need
// a second, older crash place it an hour earlier.
var crashT = time.Date(2026, 7, 23, 4, 12, 9, 0, time.UTC)

// pstoreFile is one fake record: its exact bytes and its mtime. The
// mtime matters as much as the bytes, because pstore stamps each
// record file with the wall clock at the moment of the crash, and
// the parser reads the crash time from exactly that stamp.
type pstoreFile struct {
	data  []byte
	mtime time.Time
}

// fakePstore builds a directory of records and aims pstoreDir at it.
func fakePstore(t *testing.T, files map[string]pstoreFile) string {
	t.Helper()
	dir := t.TempDir()
	for name, f := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, f.data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, f.mtime, f.mtime); err != nil {
			t.Fatal(err)
		}
	}
	old := pstoreDir
	pstoreDir = dir
	t.Cleanup(func() { pstoreDir = old })
	return dir
}

// dump renders a record the way the kernel writes one: the reason
// header first, then kmsg lines in syslog form.
func dump(header string, lines ...string) []byte {
	return []byte(header + "\n" + strings.Join(lines, "\n") + "\n")
}

// panicPstore is the common two-part panic fixture: Part1 carries
// the newest lines including the panic message, Part2 carries the
// older lines, exactly as kmsg_dump hands them out.
func panicPstore(t *testing.T) string {
	t.Helper()
	return fakePstore(t, map[string]pstoreFile{
		"dmesg-efi_pstore-172172172101001": {
			data: dump("Panic#1 Part1",
				"<0>[   12.345678] Kernel panic - not syncing: sysrq triggered crash",
				"<6>[   12.345680] CPU: 0 PID: 1 Comm: liken"),
			mtime: crashT,
		},
		"dmesg-efi_pstore-172172172102001": {
			data: dump("Panic#1 Part2",
				"<6>[   11.000000] an older line from before the panic"),
			mtime: crashT,
		},
	})
}

func TestParseDumpHeaderReadsReasonOrdinalAndPart(t *testing.T) {
	reason, ordinal, part, body, ok := parseDumpHeader([]byte("Panic#3 Part2\nthe body"))
	if !ok {
		t.Fatal("a well-formed header must parse")
	}
	if reason != "Panic" || ordinal != 3 || part != 2 {
		t.Errorf("got %s#%d Part%d", reason, ordinal, part)
	}
	if body != "the body" {
		t.Errorf("the body is everything after the header line: %q", body)
	}
}

func TestParseDumpHeaderToleratesTheRamoopsTimestampLine(t *testing.T) {
	_, _, _, body, ok := parseDumpHeader([]byte("====1721721600.123456-D\nOops#1 Part1\nthe body"))
	if !ok {
		t.Fatal("a ramoops zone header must not defeat the parse")
	}
	if body != "the body" {
		t.Errorf("got %q", body)
	}
}

func TestParseDumpHeaderRejectsGarbage(t *testing.T) {
	_, _, _, _, ok := parseDumpHeader([]byte("\x7fELF\x02\x01 not a dump at all"))
	if ok {
		t.Error("garbage must not parse as a dump")
	}
}

func TestCrashMessagePrefersTheKernelsPanicLine(t *testing.T) {
	msg := crashMessage("<6>[   12.0] CPU: 0 PID: 1\n" +
		"<0>[   12.3] Kernel panic - not syncing: Attempted to kill init! exitcode=0x00000100\n" +
		"<6>[   12.4] Hardware name: QEMU")
	if msg != "Kernel panic - not syncing: Attempted to kill init! exitcode=0x00000100" {
		t.Errorf("got %q", msg)
	}
}

func TestCrashMessageFallsBackToTheBugLine(t *testing.T) {
	msg := crashMessage("<6>[   12.0] some context\n" +
		"<1>[   12.3] BUG: unable to handle page fault for address: 0000000000000000\n" +
		"<1>[   12.3] Oops: 0002 [#1] SMP NOPTI")
	if msg != "BUG: unable to handle page fault for address: 0000000000000000" {
		t.Errorf("got %q", msg)
	}
}

func TestCrashMessageFallsBackToTheFirstLine(t *testing.T) {
	msg := crashMessage("<6>[   12.0] nothing here says panic or BUG\n<6>[   12.1] more")
	if msg != "nothing here says panic or BUG" {
		t.Errorf("got %q", msg)
	}
}

func TestCrashMessageBoundsAndSanitizes(t *testing.T) {
	long := "Kernel panic - not syncing: " + strings.Repeat("x", 4096) + "\x00\x01\x02"
	msg := crashMessage(long)
	if len(msg) > crashMessageCap {
		t.Errorf("the message must stay under the cap: %d bytes", len(msg))
	}
	if strings.ContainsAny(msg, "\x00\x01\x02") {
		t.Error("control bytes must not leave the machine")
	}
}

func TestReadPstoreRecordsAcceptsEverySpellingItMayMeet(t *testing.T) {
	dir := fakePstore(t, map[string]pstoreFile{
		"dmesg-efi_pstore-1":       {data: dump("Panic#1 Part1", "<0>[ 1.0] a"), mtime: crashT},
		"dmesg-efi-2":              {data: dump("Panic#1 Part2", "<6>[ 0.9] b"), mtime: crashT},
		"dmesg-ramoops-0":          {data: dump("Oops#1 Part1", "<1>[ 5.0] c"), mtime: crashT.Add(-time.Hour)},
		"dmesg-efi_pstore-9.enc.z": {data: []byte{0x78, 0x9c}, mtime: crashT},
		"console-ramoops-0":        {data: []byte("console text"), mtime: crashT},
	})
	recs := readPstoreRecords(dir)
	if len(recs) != 5 {
		t.Fatalf("every record is read, parseable or not: got %d", len(recs))
	}
	parseable := 0
	for _, r := range recs {
		if r.parseable {
			parseable++
		}
	}
	if parseable != 3 {
		t.Errorf("the three plain dmesg records parse: got %d", parseable)
	}
}

func TestReadPstoreRecordsCapsARunawayRecord(t *testing.T) {
	dir := fakePstore(t, map[string]pstoreFile{
		"dmesg-efi_pstore-1": {data: bytes.Repeat([]byte("a"), crashRecordCap+4096), mtime: crashT},
	})
	recs := readPstoreRecords(dir)
	if len(recs) != 1 || len(recs[0].raw) > crashRecordCap {
		t.Errorf("a broken backend must not hand init unbounded bytes: %d", len(recs[0].raw))
	}
}

func TestGroupCrashesJoinsPartsInChronologicalOrder(t *testing.T) {
	dir := panicPstore(t)
	groups := groupCrashes(readPstoreRecords(dir))
	if len(groups) != 1 {
		t.Fatalf("two parts of one dump are one crash: got %d groups", len(groups))
	}
	text := groups[0].text()
	older := strings.Index(text, "an older line")
	newer := strings.Index(text, "Kernel panic")
	if older == -1 || newer == -1 || older > newer {
		t.Errorf("part 2 is older and must come first: %q", text)
	}
}

func TestGroupCrashesSeparatesDistinctDumps(t *testing.T) {
	dir := fakePstore(t, map[string]pstoreFile{
		"dmesg-efi_pstore-1": {data: dump("Oops#1 Part1", "<1>[ 5.0] BUG: old oops"), mtime: crashT.Add(-time.Hour)},
		"dmesg-efi_pstore-2": {data: dump("Panic#1 Part1", "<0>[ 9.0] Kernel panic - not syncing: new panic"), mtime: crashT},
	})
	groups := groupCrashes(readPstoreRecords(dir))
	if len(groups) != 2 {
		t.Fatalf("distinct dumps are distinct crashes: got %d", len(groups))
	}
	if groups[0].reason != "Panic" {
		t.Errorf("the newest crash comes first: got %s", groups[0].reason)
	}
}

func TestSettleCrashPreservesClearsAndSummarizes(t *testing.T) {
	pstore := panicPstore(t)
	stateDir := t.TempDir()

	got := settleCrashRecords(stateDir, true)

	if got == nil {
		t.Fatal("a preserved crash must be reported")
	}
	if got.Reason != machine.CrashPanic || !strings.Contains(got.Message, "sysrq triggered crash") {
		t.Errorf("the summary carries the kernel's own words: %+v", got)
	}
	if got.Time == nil || !got.Time.Equal(crashT) {
		t.Errorf("the crash time is the record's mtime: %+v", got.Time)
	}
	left, _ := os.ReadDir(pstore)
	if len(left) != 0 {
		t.Errorf("pstore must be cleared after the copy lands: %d files remain", len(left))
	}
	preserved, err := os.ReadDir(got.Records)
	if err != nil || len(preserved) != 2 {
		t.Fatalf("the records field names the preserved directory: %v, %d", err, len(preserved))
	}
	raw, err := os.ReadFile(filepath.Join(got.Records, "dmesg-efi_pstore-172172172101001"))
	if err != nil || !strings.Contains(string(raw), "sysrq triggered crash") {
		t.Errorf("records are preserved verbatim: %v", err)
	}
	info, err := os.Stat(filepath.Join(got.Records, "dmesg-efi_pstore-172172172101001"))
	if err != nil || !info.ModTime().Equal(crashT) {
		t.Errorf("the preserved copy keeps the crash-time mtime: %v", info.ModTime())
	}
}

func TestSettleCrashLeavesRecordsWhenMemoryBacked(t *testing.T) {
	pstore := panicPstore(t)
	stateDir := t.TempDir()

	got := settleCrashRecords(stateDir, false)

	if got == nil || got.Records != pstore {
		t.Fatalf("with no durable machineState the records stay in pstore: %+v", got)
	}
	left, _ := os.ReadDir(pstore)
	if len(left) != 2 {
		t.Errorf("pstore is the machine's durable store; nothing is taken from it: %d", len(left))
	}
	if entries, err := os.ReadDir(filepath.Join(stateDir, "crash")); err == nil && len(entries) > 0 {
		t.Error("nothing is preserved onto a memory-backed root")
	}
}

func TestSettleCrashPrunesOlderCrashesFromPstoreWhenMemoryBacked(t *testing.T) {
	pstore := fakePstore(t, map[string]pstoreFile{
		"dmesg-efi_pstore-1": {data: dump("Oops#1 Part1", "<1>[ 5.0] BUG: old oops"), mtime: crashT.Add(-time.Hour)},
		"dmesg-efi_pstore-2": {data: dump("Panic#1 Part1", "<0>[ 9.0] Kernel panic - not syncing: new panic"), mtime: crashT},
	})

	got := settleCrashRecords(t.TempDir(), false)

	if got == nil || got.Reason != machine.CrashPanic {
		t.Fatalf("the newest crash is the one reported: %+v", got)
	}
	left, _ := os.ReadDir(pstore)
	if len(left) != 1 {
		t.Errorf("older crashes leave pstore so variable space stays free: %d remain", len(left))
	}
}

func TestSettleCrashKeepsPstoreWhenPreserveFails(t *testing.T) {
	pstore := panicPstore(t)
	sealed := t.TempDir()
	if err := os.Chmod(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })

	got := settleCrashRecords(sealed, true)

	if got == nil || got.Records != pstore {
		t.Fatalf("a failed preserve still reports, from pstore: %+v", got)
	}
	left, _ := os.ReadDir(pstore)
	if len(left) != 2 {
		t.Errorf("pstore must never be cleared before the copy lands: %d remain", len(left))
	}
}

func TestSettleCrashSkipsRepreservingAKnownCrash(t *testing.T) {
	panicPstore(t)
	stateDir := t.TempDir()
	known := filepath.Join(stateDir, "crash", crashT.UTC().Format(crashDirFormat))
	if err := os.MkdirAll(known, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(known, "dmesg-efi_pstore-172172172101001")
	if err := os.WriteFile(sentinel, dump("Panic#1 Part1", "<0>[ 12.3] Kernel panic - not syncing: sysrq triggered crash"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sentinel, crashT, crashT); err != nil {
		t.Fatal(err)
	}

	got := settleCrashRecords(stateDir, true)

	if got == nil || got.Records != known {
		t.Fatalf("the known directory is the record's home: %+v", got)
	}
	preserved, _ := os.ReadDir(known)
	if len(preserved) != 1 {
		t.Errorf("a crash a prior boot preserved is not copied again: %d files", len(preserved))
	}
	left, _ := os.ReadDir(pstoreDir)
	if len(left) != 0 {
		t.Errorf("the clear that failed last boot is retried: %d remain", len(left))
	}
}

func TestSettleCrashRederivesFromPreservedRecords(t *testing.T) {
	fakePstore(t, map[string]pstoreFile{})
	stateDir := t.TempDir()
	preserved := filepath.Join(stateDir, "crash", crashT.UTC().Format(crashDirFormat))
	if err := os.MkdirAll(preserved, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(preserved, "dmesg-efi_pstore-172172172101001")
	if err := os.WriteFile(path, dump("Panic#1 Part1", "<0>[ 12.3] Kernel panic - not syncing: sysrq triggered crash"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, crashT, crashT); err != nil {
		t.Fatal(err)
	}

	got := settleCrashRecords(stateDir, true)

	if got == nil {
		t.Fatal("every boot re-derives the fact from the preserved records")
	}
	if got.Reason != machine.CrashPanic || !strings.Contains(got.Message, "sysrq triggered crash") || got.Records != preserved {
		t.Errorf("the re-derived summary matches the original: %+v", got)
	}
}

func TestSettleCrashPrefersTheNewestCrash(t *testing.T) {
	panicPstore(t)
	stateDir := t.TempDir()
	oldT := crashT.Add(-24 * time.Hour)
	preserved := filepath.Join(stateDir, "crash", oldT.UTC().Format(crashDirFormat))
	if err := os.MkdirAll(preserved, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(preserved, "dmesg-efi_pstore-9")
	if err := os.WriteFile(path, dump("Oops#1 Part1", "<1>[ 5.0] BUG: yesterday's oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, oldT, oldT); err != nil {
		t.Fatal(err)
	}

	got := settleCrashRecords(stateDir, true)

	if got == nil || got.Reason != machine.CrashPanic {
		t.Fatalf("the newer crash wins the summary: %+v", got)
	}
}

func TestSettleCrashWithNothingReportsNothing(t *testing.T) {
	fakePstore(t, map[string]pstoreFile{})
	if got := settleCrashRecords(t.TempDir(), true); got != nil {
		t.Errorf("a machine with no crash history reports none: %+v", got)
	}
}

func TestPruneCrashStoreKeepsTheNewest(t *testing.T) {
	store := t.TempDir()
	for i := range 12 {
		name := crashT.Add(time.Duration(i) * time.Hour).UTC().Format(crashDirFormat)
		if err := os.MkdirAll(filepath.Join(store, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	pruneCrashStore(store, crashKeep)

	entries, err := os.ReadDir(store)
	if err != nil || len(entries) != crashKeep {
		t.Fatalf("the newest %d stay: got %d, %v", crashKeep, len(entries), err)
	}
	oldest := crashT.UTC().Format(crashDirFormat)
	for _, e := range entries {
		if e.Name() == oldest {
			t.Error("the oldest directories are the ones pruned")
		}
	}
}
