package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// fakeStore records each call that an apply makes to the variable
// store. Its write imitates the firmware's merge of the signed list, so
// the variable grows by the length of merged, not by the length of the
// write.
type fakeStore struct {
	// flags is the variable's inode flags.
	flags  uint32
	calls  []string
	merged []byte
	// fail makes the call of that name return its error. The name
	// "restore" fails only the setFlags call that sets the immutable
	// flag again.
	fail map[string]error
	// failWrite makes the write with that number, counted from 1, return
	// its error.
	failWrite map[int]error
	writes    int
}

func (s *fakeStore) store() variableStore {
	return variableStore{
		flags: func(string) (uint32, error) {
			s.calls = append(s.calls, "flags")
			return s.flags, s.fail["flags"]
		},
		setFlags: func(_ string, flags uint32) error {
			s.calls = append(s.calls, fmt.Sprintf("setFlags 0x%x", flags))
			if err := s.fail["setFlags"]; err != nil {
				return err
			}
			if err := s.fail["restore"]; err != nil && flags&immutableFlag != 0 {
				return err
			}
			s.flags = flags
			return nil
		},
		write: func(path string, data []byte) error {
			s.calls = append(s.calls, fmt.Sprintf("write %d", len(data)))
			if err := s.fail["write"]; err != nil {
				return err
			}
			s.writes++
			if err := s.failWrite[s.writes]; err != nil {
				return err
			}
			current, _ := os.ReadFile(path)
			return os.WriteFile(path, append(current, s.merged...), 0o644)
		},
	}
}

// applyRig is an apply on a rehearsalRig whose dbx variable has the
// immutable flag set, and whose recorded SHA-256 matches the
// rehearsal's append. The signal arrives at once, and the rig records
// it in the store's calls, so a test can check that no store call came
// before it.
type applyRig struct {
	applier
	rig      *rehearsalRig
	efivars  *fakeStore
	progress bytes.Buffer
	// waited is what the apply had printed when it began to wait.
	waited string
	// arrives runs when the signal would arrive. An error ends the wait
	// with that error and no signal.
	arrives func() error
}

func newApplyRig(t *testing.T) *applyRig {
	t.Helper()
	r := newRehearsalRig(t)
	efivars := &fakeStore{flags: immutableFlag, merged: []byte{1, 2, 3, 4, 5, 6, 7, 8}}
	a := &applyRig{rig: r, efivars: efivars, arrives: func() error { return nil }}
	a.applier = applier{rehearser: r.rehearser, appendSHA256: sum(authAppend), store: efivars.store(),
		marker: filepath.Join(t.TempDir(), "prepared"),
		signal: func(context.Context) error {
			a.waited = a.progress.String()
			if err := a.arrives(); err != nil {
				return err
			}
			efivars.calls = append(efivars.calls, "signal")
			return nil
		}}
	return a
}

func (a *applyRig) run() ApplyResult {
	return a.apply(context.Background(), &a.progress)
}

func (a *applyRig) machineDBX(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(a.hostDir, "efi", "efivars", dbxFile))
	if err != nil {
		t.Fatalf("reading the machine's dbx: %v", err)
	}
	return data
}

func TestAnApplyWritesTheRehearsedAppendInOneWrite(t *testing.T) {
	a := newApplyRig(t)

	got := a.run()

	want := ApplyResult{Result: resultWritten, Variable: dbxFile, SHA256: sum(authAppend), Before: 68, After: 76,
		Message: "wrote the 128-byte append to " + dbxFile + ", which grew from 68 to 76 bytes because the firmware merges the signed lists into the stored ones"}
	if got != want {
		t.Errorf("the result is %+v, want %+v", got, want)
	}
	if want := []string{"signal", "flags", "setFlags 0x0", "write 128", "setFlags 0x10"}; !slices.Equal(a.efivars.calls, want) {
		t.Errorf("the calls are %q, want the signal, the flag cleared, one write, and the flag restored: %q", a.efivars.calls, want)
	}
}

// newTwoWriteApplyRig is an applyRig whose install appends two
// authenticated writes.
func newTwoWriteApplyRig(t *testing.T) *applyRig {
	t.Helper()
	a := newApplyRig(t)
	a.rig.tool.effects["install"] = func() {
		a.rig.appendTo(t, dbxFile, authAppend)
		a.rig.appendTo(t, dbxFile, secondAppend)
	}
	a.appendSHA256 = sum(append(bytes.Clone(authAppend), secondAppend...))
	return a
}

// Each authenticated write has its own signature over its own data, so
// the firmware cannot verify two writes sent in one call.
func TestAnApplyWritesEachAuthenticatedWriteInItsOwnCall(t *testing.T) {
	a := newTwoWriteApplyRig(t)

	got := a.run()

	if got.Result != resultWritten || got.Message != "wrote the 304-byte append to "+dbxFile+" in 2 authenticated writes, which grew from 68 to 84 bytes because the firmware merges the signed lists into the stored ones" {
		t.Errorf("the result is %+v, want Written in 2 writes", got)
	}
	if want := []string{"signal", "flags", "setFlags 0x0", "write 128", "write 176", "setFlags 0x10"}; !slices.Equal(a.efivars.calls, want) {
		t.Errorf("the calls are %q, want each write in its own call: %q", a.efivars.calls, want)
	}
}

// A write that the firmware accepted cannot be undone, so when a later
// write fails, the variable has still changed and the machine must
// reboot.
func TestAnApplyThatStopsAfterAnAcceptedWriteIsUncertain(t *testing.T) {
	a := newTwoWriteApplyRig(t)
	a.efivars.failWrite = map[int]error{2: syscall.EACCES}

	got := a.run()

	want := "the variable " + dbxFile + " changed, so the machine must reboot: the firmware accepted 1 of the 2 authenticated writes, and write 2 returned: permission denied"
	if got.Result != resultUncertain || got.Message != want {
		t.Errorf("the result is %+v, want Uncertain: %s", got, want)
	}
	if last := a.efivars.calls[len(a.efivars.calls)-1]; last != "setFlags 0x10" {
		t.Errorf("the last call is %q, want the immutable flag restored", last)
	}
}

// When the firmware refuses the first write, nothing changed, and the
// apply sends no later write.
func TestTheFirmwaresRefusalOfTheFirstWriteIsRefused(t *testing.T) {
	a := newTwoWriteApplyRig(t)
	a.efivars.failWrite = map[int]error{1: syscall.EACCES}

	got := a.run()

	if got.Result != resultRefused || slices.Contains(a.efivars.calls, "write 176") {
		t.Errorf("the result is %+v after the calls %q, want Refused with no second write", got, a.efivars.calls)
	}
}

func TestAnApplyLeavesAVariableWithNoImmutableFlagAlone(t *testing.T) {
	a := newApplyRig(t)
	a.efivars.flags = 0

	a.run()

	if want := []string{"signal", "flags", "write 128"}; !slices.Equal(a.efivars.calls, want) {
		t.Errorf("the calls are %q, want %q", a.efivars.calls, want)
	}
}

// When the prepare's rehearsal produces a different append, or does not
// pass, the apply writes nothing and does not wait for the signal.
func TestAnApplyWritesNothingWhenTheVariablesChanged(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, a *applyRig)
		want  string
	}{
		{"another append", func(t *testing.T, a *applyRig) { a.appendSHA256 = "cf901743" },
			"the append's SHA-256 is now " + sum(authAppend) + ", and the rehearsal recorded cf901743: the machine's variables changed since the rehearsal, so nothing was written"},
		{"a rehearsal that does not pass", func(t *testing.T, a *applyRig) {
			a.rig.tool.effects["install"] = func() {}
		}, "the rehearsal before the write did not pass, so nothing was written: the install did not change " + dbxFile},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newApplyRig(t)
			c.setup(t, a)

			got := a.run()

			if got.Result != resultFailed || got.Message != c.want {
				t.Errorf("the result is %+v, want Failed: %s", got, c.want)
			}
			if len(a.efivars.calls) != 0 || !slices.Equal(a.machineDBX(t), dbxBefore) {
				t.Errorf("the apply touched the machine's dbx with %q, want no calls", a.efivars.calls)
			}
			if isPrepared(a.marker) {
				t.Error("the marker of a finished prepare exists, want none after a prepare that failed")
			}
		})
	}
}

// A catalog that moved since the scan is not a failure. The operator
// must read the jcat again, so the apply returns CatalogMoved.
func TestAPrepareOfAMovedCatalogReturnsCatalogMoved(t *testing.T) {
	a := newApplyRig(t)
	a.rig.web[testCatalogURL+".jcat"] = jcatFor(t, "firmware-09139-stable.xml.zst", sum(testCatalog))

	got := a.run()

	if got.Result != resultCatalogMoved || got.Message != "the jcat now lists firmware-09139-stable.xml.zst, so nothing was written" {
		t.Errorf("the result is %+v, want CatalogMoved with the new file", got)
	}
	if isPrepared(a.marker) || a.waited != "" || len(a.efivars.calls) != 0 {
		t.Errorf("the apply marked its prepare, waited, or touched the store with %q, want none of these", a.efivars.calls)
	}
}

// The kernel reports the firmware's EFI_SECURITY_VIOLATION as EACCES,
// so the apply treats EACCES as the firmware refusing the append and
// not as a fault in the pod.
func TestTheFirmwaresRefusalIsRefused(t *testing.T) {
	a := newApplyRig(t)
	a.efivars.fail = map[string]error{"write": syscall.EACCES}

	got := a.run()

	if got.Result != resultRefused || got.Message != "the firmware refused the append to "+dbxFile+": permission denied" {
		t.Errorf("the result is %+v, want Refused", got)
	}
	if last := a.efivars.calls[len(a.efivars.calls)-1]; last != "setFlags 0x10" {
		t.Errorf("the last call is %q, want the immutable flag restored", last)
	}
}

func TestAnApplyFails(t *testing.T) {
	cases := []struct {
		name  string
		fail  map[string]error
		want  string
		calls []string
	}{
		{"unreadable flags", map[string]error{"flags": syscall.ENOTTY},
			"reading the flags of " + dbxFile + ": inappropriate ioctl for device", []string{"signal", "flags"}},
		{"no capability to clear the flag", map[string]error{"setFlags": syscall.EPERM},
			"clearing the immutable flag of " + dbxFile + ": operation not permitted", []string{"signal", "flags", "setFlags 0x0"}},
		{"a write the kernel refuses", map[string]error{"write": syscall.EIO},
			"writing the append to " + dbxFile + ": input/output error", []string{"signal", "flags", "setFlags 0x0", "write 128", "setFlags 0x10"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newApplyRig(t)
			a.efivars.fail = c.fail

			got := a.run()

			if got.Result != resultFailed || got.Message != c.want {
				t.Errorf("the result is %+v, want Failed: %s", got, c.want)
			}
			if !slices.Equal(a.efivars.calls, c.calls) {
				t.Errorf("the calls are %q, want %q", a.efivars.calls, c.calls)
			}
		})
	}
}

// The firmware accepted the write, so the result is Written even when
// the immutable flag cannot be set again.
func TestAWrittenAppendWhoseFlagStaysClearIsWritten(t *testing.T) {
	a := newApplyRig(t)
	a.efivars.fail = map[string]error{"restore": errors.New("the inode is busy")}

	got := a.run()

	if got.Result != resultWritten || !strings.HasSuffix(got.Message, ", and the immutable flag was not set again: the inode is busy") {
		t.Errorf("the result is %+v, want Written with the flag's failure", got)
	}
}

func TestAnApplyWaitsForTheSignalBeforeItTouchesTheStore(t *testing.T) {
	a := newApplyRig(t)
	a.arrives = func() error {
		if len(a.efivars.calls) != 0 {
			t.Errorf("the store saw %q before the signal, want nothing", a.efivars.calls)
		}
		if !isPrepared(a.marker) {
			t.Error("the apply waits with no marker of its finished prepare, want the marker for the readiness probe")
		}
		if !slices.Equal(a.machineDBX(t), dbxBefore) {
			t.Error("the machine's dbx changed before the signal, want it unchanged")
		}
		return nil
	}

	a.run()

	var progress map[string]any
	if err := json.Unmarshal([]byte(a.waited), &progress); err != nil {
		t.Fatalf("before the wait the apply printed %q, want one progress line: %v", a.waited, err)
	}
	if progress["phase"] != phasePrepared || progress["sha256"] != sum(authAppend) || progress["variable"] != dbxFile {
		t.Errorf("the progress line is %v, want the prepared append to %s", progress, dbxFile)
	}
}

// The operator reads the last log line that has a result key as the
// result.
func TestTheProgressLineIsNotAResult(t *testing.T) {
	a := newApplyRig(t)

	a.run()

	lines := strings.Split(strings.TrimSpace(a.progress.String()), "\n")
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("parsing the progress line: %v", err)
	}
	if _, has := first["result"]; has || len(lines) != 1 {
		t.Errorf("the apply printed %q, want one progress line with no result", lines)
	}
}

func TestAnApplyWithNoSignalWritesNothing(t *testing.T) {
	a := newApplyRig(t)
	a.arrives = func() error { return context.Canceled }

	got := a.run()

	if got.Result != resultFailed || got.Message != "waiting for the signal to write: context canceled, so nothing was written" {
		t.Errorf("the result is %+v, want Failed with nothing written", got)
	}
	if len(a.efivars.calls) != 0 || !slices.Equal(a.machineDBX(t), dbxBefore) {
		t.Errorf("the apply touched the machine's dbx with %q, want no calls", a.efivars.calls)
	}
}

// The append extends the variable as the rehearsal copied it, so a
// variable that changed during the wait is not written.
func TestAVariableThatChangedDuringTheWaitIsNotWritten(t *testing.T) {
	a := newApplyRig(t)
	a.arrives = func() error {
		writeFile(t, filepath.Join(a.hostDir, "efi", "efivars", dbxFile), append(bytes.Clone(dbxBefore), 0xcc))
		return nil
	}

	got := a.run()

	if want := "the variable " + dbxFile + " changed while the apply waited for its turn, so nothing was written"; got.Result != resultFailed || got.Message != want {
		t.Errorf("the result is %+v, want Failed: %s", got, want)
	}
	if want := []string{"signal"}; !slices.Equal(a.efivars.calls, want) {
		t.Errorf("the calls are %q, want only the signal", a.efivars.calls)
	}
}

func TestIsPreparedReadsTheMarker(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "prepared")
	if isPrepared(marker) {
		t.Error("isPrepared returns true with no marker, want false")
	}
	writeFile(t, marker, nil)
	if !isPrepared(marker) {
		t.Error("isPrepared returns false with the marker, want true")
	}
}

// If the marker cannot be written, the pod never becomes Ready and the
// operator never requests a turn, so the apply ends instead of waiting.
func TestAnApplyThatCannotMarkItsPrepareWritesNothing(t *testing.T) {
	a := newApplyRig(t)
	a.marker = filepath.Join(t.TempDir(), "absent", "prepared")

	got := a.run()

	if got.Result != resultFailed || !strings.HasPrefix(got.Message, "marking the prepare as done: ") || !strings.HasSuffix(got.Message, ", so nothing was written") {
		t.Errorf("the result is %+v, want Failed with nothing written", got)
	}
	if a.waited != "" || len(a.efivars.calls) != 0 {
		t.Errorf("the apply waited or touched the store with %q, want neither", a.efivars.calls)
	}
}

func TestAnApplierReadsItsEnvironment(t *testing.T) {
	env := map[string]string{"CABINET_URL": testCabinetURL, "DEVICE_ID": dbxID, "APPEND_SHA256": "cf901743"}

	a := applierFromEnv(func(key string) string { return env[key] })

	if a.appendSHA256 != "cf901743" || a.cabinetURL != testCabinetURL || a.deviceID != dbxID {
		t.Errorf("the applier is %+v, want the append and the rehearsal's environment", a)
	}
}
