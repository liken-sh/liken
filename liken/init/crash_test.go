package main

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

// crashT is the time of the newest crash in the fixtures.
var crashT = time.Date(2026, 7, 23, 4, 12, 9, 0, time.UTC)

// pstoreFile is one fake record. The mtime matters because the code
// reads the crash time from the mtime, as pstore sets it.
type pstoreFile struct {
	data  []byte
	mtime time.Time
}

// fakePstore writes the files to a temporary directory and sets
// pstoreDir to it for the test.
func fakePstore(t *testing.T, files map[string]pstoreFile) string {
	t.Helper()
	dir := t.TempDir()
	for name, f := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, f.data, 0o644); err != nil {
			t.Fatalf("writing the test file: %v", err)
		}
		if err := os.Chtimes(path, f.mtime, f.mtime); err != nil {
			t.Fatalf("setting the file time: %v", err)
		}
	}
	old := pstoreDir
	pstoreDir = dir
	t.Cleanup(func() { pstoreDir = old })
	return dir
}

// dump returns the contents of a record: the header line, then the
// kmsg lines.
func dump(header string, lines ...string) []byte {
	return []byte(header + "\n" + strings.Join(lines, "\n") + "\n")
}

// panicPstore is a pstore with one panic in two parts. Part1 has the
// newest lines, including the panic message, and Part2 has older
// lines, as the kernel writes them.
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
		t.Fatal("parseDumpHeader refused a well-formed header, want it parsed")
	}
	if reason != "Panic" || ordinal != 3 || part != 2 {
		t.Errorf("the header is %s#%d Part%d, want Panic#3 Part2", reason, ordinal, part)
	}
	if body != "the body" {
		t.Errorf("the body is %q, want everything after the header line", body)
	}
}

func TestParseDumpHeaderToleratesTheRamoopsTimestampLine(t *testing.T) {
	_, _, _, body, ok := parseDumpHeader([]byte("====1721721600.123456-D\nOops#1 Part1\nthe body"))
	if !ok {
		t.Fatal("parseDumpHeader refused a dump with a ramoops zone header, want it parsed")
	}
	if body != "the body" {
		t.Errorf("the body is %q, want \"the body\"", body)
	}
}

func TestParseDumpHeaderRejectsGarbage(t *testing.T) {
	_, _, _, _, ok := parseDumpHeader([]byte("\x7fELF\x02\x01 not a dump at all"))
	if ok {
		t.Error("parseDumpHeader accepted an ELF header as a dump, want it refused")
	}
}

func TestCrashMessagePrefersTheKernelsPanicLine(t *testing.T) {
	msg := crashMessage("<6>[   12.0] CPU: 0 PID: 1\n" +
		"<0>[   12.3] Kernel panic - not syncing: Attempted to kill init! exitcode=0x00000100\n" +
		"<6>[   12.4] Hardware name: QEMU")
	if msg != "Kernel panic - not syncing: Attempted to kill init! exitcode=0x00000100" {
		t.Errorf("crashMessage returned %q, want the Kernel panic line", msg)
	}
}

func TestCrashMessageFallsBackToTheBugLine(t *testing.T) {
	msg := crashMessage("<6>[   12.0] some context\n" +
		"<1>[   12.3] BUG: unable to handle page fault for address: 0000000000000000\n" +
		"<1>[   12.3] Oops: 0002 [#1] SMP NOPTI")
	if msg != "BUG: unable to handle page fault for address: 0000000000000000" {
		t.Errorf("crashMessage returned %q, want the BUG line", msg)
	}
}

func TestCrashMessageFallsBackToTheFirstLine(t *testing.T) {
	msg := crashMessage("<6>[   12.0] nothing here says panic or BUG\n<6>[   12.1] more")
	if msg != "nothing here says panic or BUG" {
		t.Errorf("crashMessage returned %q, want the first line when no line names a panic or a BUG", msg)
	}
}

func TestCrashMessageBoundsAndSanitizes(t *testing.T) {
	long := "Kernel panic - not syncing: " + strings.Repeat("x", 4096) + "\x00\x01\x02"
	msg := crashMessage(long)
	if len(msg) > crashMessageCap {
		t.Errorf("the message is %d bytes, want at most %d", len(msg), crashMessageCap)
	}
	if strings.ContainsAny(msg, "\x00\x01\x02") {
		t.Errorf("the message is %q, want no control bytes", msg)
	}
}

func TestReadPstoreRecordsReadsEveryFileAndParsesOnlyPlainDmesgDumps(t *testing.T) {
	dir := fakePstore(t, map[string]pstoreFile{
		"dmesg-efi_pstore-1":       {data: dump("Panic#1 Part1", "<0>[ 1.0] a"), mtime: crashT},
		"dmesg-efi-2":              {data: dump("Panic#1 Part2", "<6>[ 0.9] b"), mtime: crashT},
		"dmesg-ramoops-0":          {data: dump("Oops#1 Part1", "<1>[ 5.0] c"), mtime: crashT.Add(-time.Hour)},
		"dmesg-efi_pstore-9.enc.z": {data: []byte{0x78, 0x9c}, mtime: crashT},
		"console-ramoops-0":        {data: []byte("console text"), mtime: crashT},
	})
	recs := readPstoreRecords(dir)
	if len(recs) != 5 {
		t.Fatalf("readPstoreRecords returned %d records, want every record, parseable or not", len(recs))
	}
	parseable := 0
	for _, r := range recs {
		if r.parseable {
			parseable++
		}
	}
	if parseable != 3 {
		t.Errorf("%d records parse, want the 3 plain dmesg records", parseable)
	}
}

func TestReadPstoreRecordsCapsARunawayRecord(t *testing.T) {
	dir := fakePstore(t, map[string]pstoreFile{
		"dmesg-efi_pstore-1": {data: bytes.Repeat([]byte("a"), crashRecordCap+4096), mtime: crashT},
	})
	recs := readPstoreRecords(dir)
	if len(recs) != 1 || len(recs[0].raw) > crashRecordCap {
		t.Errorf("readPstoreRecords returned %d records, want 1 of at most %d bytes", len(recs), crashRecordCap)
	}
}

func TestGroupCrashesJoinsPartsInChronologicalOrder(t *testing.T) {
	dir := panicPstore(t)
	groups := groupCrashes(readPstoreRecords(dir))
	if len(groups) != 1 {
		t.Fatalf("the records form %d crash groups, want 1 for two parts of one dump", len(groups))
	}
	text := groups[0].text()
	older := strings.Index(text, "an older line")
	newer := strings.Index(text, "Kernel panic")
	if older == -1 || newer == -1 || older > newer {
		t.Errorf("the dump text is %q, want part 2 first because it is older", text)
	}
}

func TestGroupCrashesSeparatesDistinctDumps(t *testing.T) {
	dir := fakePstore(t, map[string]pstoreFile{
		"dmesg-efi_pstore-1": {data: dump("Oops#1 Part1", "<1>[ 5.0] BUG: old oops"), mtime: crashT.Add(-time.Hour)},
		"dmesg-efi_pstore-2": {data: dump("Panic#1 Part1", "<0>[ 9.0] Kernel panic - not syncing: new panic"), mtime: crashT},
	})
	groups := groupCrashes(readPstoreRecords(dir))
	if len(groups) != 2 {
		t.Fatalf("the records form %d crash groups, want 2 for distinct dumps", len(groups))
	}
	if groups[0].reason != "Panic" {
		t.Errorf("the first group is %s, want the newest crash", groups[0].reason)
	}
}

func TestSettleCrashPreservesClearsAndSummarizes(t *testing.T) {
	pstore := panicPstore(t)
	stateDir := t.TempDir()

	got := settleCrashRecords(stateDir, true)

	if got == nil {
		t.Fatal("settleCrashRecords reported no crash, want one for a preserved crash")
	}
	if got.Reason != machine.CrashPanic || !strings.Contains(got.Message, "sysrq triggered crash") {
		t.Errorf("the summary is %+v, want the kernel's own message", got)
	}
	if got.Time == nil || !got.Time.Equal(crashT) {
		t.Errorf("the crash time is %+v, want the mtime of the record", got.Time)
	}
	left, _ := os.ReadDir(pstore)
	if len(left) != 0 {
		t.Errorf("%d files remain in pstore, want 0 after the copy", len(left))
	}
	preserved, err := os.ReadDir(got.Records)
	if err != nil || len(preserved) != 2 {
		t.Fatalf("the records field is wrong: error %v, %d preserved files, want the preserved directory", err, len(preserved))
	}
	raw, err := os.ReadFile(filepath.Join(got.Records, "dmesg-efi_pstore-172172172101001"))
	if err != nil || !strings.Contains(string(raw), "sysrq triggered crash") {
		t.Errorf("the preserved record cannot be read or lacks \"sysrq triggered crash\" (%v), want the pstore record's text", err)
	}
	info, err := os.Stat(filepath.Join(got.Records, "dmesg-efi_pstore-172172172101001"))
	if err != nil || !info.ModTime().Equal(crashT) {
		t.Errorf("the preserved copy has mtime %v, want the mtime of the crash", info.ModTime())
	}
}

func TestSettleCrashLeavesRecordsWhenMemoryBacked(t *testing.T) {
	pstore := panicPstore(t)
	stateDir := t.TempDir()

	got := settleCrashRecords(stateDir, false)

	if got == nil || got.Records != pstore {
		t.Fatalf("the summary is %+v, want one from pstore when no durable machineState exists", got)
	}
	left, _ := os.ReadDir(pstore)
	if len(left) != 2 {
		t.Errorf("%d files remain in pstore, want all of them when no durable machineState exists", len(left))
	}
	if entries, err := os.ReadDir(filepath.Join(stateDir, "crash")); err == nil && len(entries) > 0 {
		t.Errorf("%s has %d records on a memory-backed root, want none copied", filepath.Join(stateDir, "crash"), len(entries))
	}
}

func TestSettleCrashPrunesOlderCrashesFromPstoreWhenMemoryBacked(t *testing.T) {
	pstore := fakePstore(t, map[string]pstoreFile{
		"dmesg-efi_pstore-1": {data: dump("Oops#1 Part1", "<1>[ 5.0] BUG: old oops"), mtime: crashT.Add(-time.Hour)},
		"dmesg-efi_pstore-2": {data: dump("Panic#1 Part1", "<0>[ 9.0] Kernel panic - not syncing: new panic"), mtime: crashT},
	})

	got := settleCrashRecords(t.TempDir(), false)

	if got == nil || got.Reason != machine.CrashPanic {
		t.Fatalf("the summary is %+v, want the newest crash", got)
	}
	left, _ := os.ReadDir(pstore)
	if len(left) != 1 {
		t.Errorf("%d files remain in pstore, want only those of the newest crash", len(left))
	}
}

func TestSettleCrashKeepsPstoreWhenPreserveFails(t *testing.T) {
	pstore := panicPstore(t)
	sealed := t.TempDir()
	if err := os.Chmod(sealed, 0o555); err != nil {
		t.Fatalf("making the directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })

	got := settleCrashRecords(sealed, true)

	if got == nil || got.Records != pstore {
		t.Fatalf("the summary is %+v, want one from pstore after the copy fails", got)
	}
	left, _ := os.ReadDir(pstore)
	if len(left) != 2 {
		t.Errorf("%d files remain in pstore, want all of them after the copy fails", len(left))
	}
}

// When a boot stops partway through the copy, the directory has only
// some of the records. The next boot must finish the copy before it
// clears pstore, or the clear deletes the only copy of the missing
// records.
func TestSettleCrashFinishesACopyThatAPriorBootLeftPartial(t *testing.T) {
	pstore := panicPstore(t)
	stateDir := t.TempDir()
	partial := filepath.Join(stateDir, "crash", crashT.UTC().Format(crashDirFormat))
	if err := os.MkdirAll(partial, 0o755); err != nil {
		t.Fatalf("creating the test directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(partial, "dmesg-efi_pstore-172172172101001"), []byte("Panic#1 Pa"), 0o644); err != nil {
		t.Fatalf("writing the test file: %v", err)
	}
	want := pstoreContents(t, pstore)

	settleCrashRecords(stateDir, true)

	if got := pstoreContents(t, partial); !maps.Equal(got, want) {
		t.Errorf("the preserved files are %q, want every record of the batch, whole", slices.Sorted(maps.Keys(got)))
	}
}

// pstoreContents returns the contents of each file in dir, by file
// name.
func pstoreContents(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the test directory: %v", err)
	}
	contents := map[string]string{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading the test file: %v", err)
		}
		contents[e.Name()] = string(data)
	}
	return contents
}

// When a previous boot copied the whole batch but stopped before the
// clear, the same records are in pstore and in the crash directory.
// This boot keeps the copy and clears pstore.
func TestSettleCrashRetriesTheClearOfAPreservedBatch(t *testing.T) {
	pstore := panicPstore(t)
	stateDir := t.TempDir()
	known := filepath.Join(stateDir, "crash", crashT.UTC().Format(crashDirFormat))
	want := pstoreContents(t, pstore)
	if err := os.CopyFS(known, os.DirFS(pstore)); err != nil {
		t.Fatalf("copying the records: %v", err)
	}

	got := settleCrashRecords(stateDir, true)

	if got == nil || got.Records != known {
		t.Fatalf("the summary is %+v, want the one built from the existing crash directory", got)
	}
	if preserved := pstoreContents(t, known); !maps.Equal(preserved, want) {
		t.Errorf("the preserved files are %q, want the batch unchanged", slices.Sorted(maps.Keys(preserved)))
	}
	if left, _ := os.ReadDir(pstore); len(left) != 0 {
		t.Errorf("%d files remain in pstore, want 0 after the retried delete", len(left))
	}
}

func TestSettleCrashBuildsTheSummaryFromPreservedRecords(t *testing.T) {
	fakePstore(t, map[string]pstoreFile{})
	stateDir := t.TempDir()
	preserved := filepath.Join(stateDir, "crash", crashT.UTC().Format(crashDirFormat))
	if err := os.MkdirAll(preserved, 0o755); err != nil {
		t.Fatalf("creating the test directory: %v", err)
	}
	path := filepath.Join(preserved, "dmesg-efi_pstore-172172172101001")
	if err := os.WriteFile(path, dump("Panic#1 Part1", "<0>[ 12.3] Kernel panic - not syncing: sysrq triggered crash"), 0o644); err != nil {
		t.Fatalf("writing the test file: %v", err)
	}
	if err := os.Chtimes(path, crashT, crashT); err != nil {
		t.Fatalf("setting the file time: %v", err)
	}

	got := settleCrashRecords(stateDir, true)

	if got == nil {
		t.Fatal("settleCrashRecords reported no crash, want one from the preserved records")
	}
	if got.Reason != machine.CrashPanic || !strings.Contains(got.Message, "sysrq triggered crash") || got.Records != preserved {
		t.Errorf("the summary is %+v, want the one from the original records", got)
	}
}

func TestSettleCrashPrefersTheNewestCrash(t *testing.T) {
	panicPstore(t)
	stateDir := t.TempDir()
	oldT := crashT.Add(-24 * time.Hour)
	preserved := filepath.Join(stateDir, "crash", oldT.UTC().Format(crashDirFormat))
	if err := os.MkdirAll(preserved, 0o755); err != nil {
		t.Fatalf("creating the test directory: %v", err)
	}
	path := filepath.Join(preserved, "dmesg-efi_pstore-9")
	if err := os.WriteFile(path, dump("Oops#1 Part1", "<1>[ 5.0] BUG: yesterday's oops"), 0o644); err != nil {
		t.Fatalf("writing the test file: %v", err)
	}
	if err := os.Chtimes(path, oldT, oldT); err != nil {
		t.Fatalf("setting the file time: %v", err)
	}

	got := settleCrashRecords(stateDir, true)

	if got == nil || got.Reason != machine.CrashPanic {
		t.Fatalf("the summary is %+v, want the newer crash", got)
	}
}

func TestSettleCrashWithNothingReportsNothing(t *testing.T) {
	fakePstore(t, map[string]pstoreFile{})
	if got := settleCrashRecords(t.TempDir(), true); got != nil {
		t.Errorf("the summary is %+v, want none for a machine with no crash history", got)
	}
}

func TestPruneCrashStoreKeepsTheNewest(t *testing.T) {
	store := t.TempDir()
	for i := range 12 {
		name := crashT.Add(time.Duration(i) * time.Hour).UTC().Format(crashDirFormat)
		if err := os.MkdirAll(filepath.Join(store, name), 0o755); err != nil {
			t.Fatalf("creating the test directory: %v", err)
		}
	}

	pruneCrashStore(store, crashKeep)

	entries, err := os.ReadDir(store)
	if err != nil || len(entries) != crashKeep {
		t.Fatalf("%d directories remain, want the newest %d: error %v", len(entries), crashKeep, err)
	}
	oldest := crashT.UTC().Format(crashDirFormat)
	for _, e := range entries {
		if e.Name() == oldest {
			t.Error("a newer directory was deleted, want the oldest ones")
		}
	}
}
