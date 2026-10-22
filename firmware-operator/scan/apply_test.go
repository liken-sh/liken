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

// fakeStore plays the kernel's efivarfs under an apply. It records each
// call the apply makes, and its write plays the firmware, which merges
// the signed list into the stored one: the variable grows by merged,
// not by the whole append.
type fakeStore struct {
	held   uint32
	calls  []string
	merged []byte
	// fail answers an error for the call of that name, and restore for
	// the setFlags call that sets the immutable flag again.
	fail map[string]error
}

func (s *fakeStore) store() variableStore {
	return variableStore{
		flags: func(string) (uint32, error) {
			s.calls = append(s.calls, "flags")
			return s.held, s.fail["flags"]
		},
		setFlags: func(_ string, flags uint32) error {
			s.calls = append(s.calls, fmt.Sprintf("setFlags 0x%x", flags))
			if err := s.fail["setFlags"]; err != nil {
				return err
			}
			if err := s.fail["restore"]; err != nil && flags&immutableFlag != 0 {
				return err
			}
			s.held = flags
			return nil
		},
		write: func(path string, data []byte) error {
			s.calls = append(s.calls, fmt.Sprintf("write %d", len(data)))
			if err := s.fail["write"]; err != nil {
				return err
			}
			held, _ := os.ReadFile(path)
			return os.WriteFile(path, append(held, s.merged...), 0o644)
		},
	}
}

// applyRig is a rehearsalRig whose machine holds dbx with the
// immutable flag that efivarfs sets, and an apply that expects the
// append the rehearsal recorded. Its signal arrives at once, and is
// recorded among the store's calls, so a test reads whether a call to
// the store came before it. waited holds what the apply had printed
// when it began to wait.
type applyRig struct {
	applier
	rig      *rehearsalRig
	efivars  *fakeStore
	progress bytes.Buffer
	waited   string
	// arrives runs when the signal arrives, and its error ends the wait
	// with no signal.
	arrives func() error
}

func newApplyRig(t *testing.T) *applyRig {
	t.Helper()
	r := newRehearsalRig(t)
	efivars := &fakeStore{held: immutableFlag, merged: []byte{1, 2, 3, 4, 5, 6, 7, 8}}
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

// run runs the apply, with its lines printed to the rig's progress.
func (a *applyRig) run() ApplyResult {
	return a.apply(context.Background(), &a.progress)
}

func (a *applyRig) machineDBX(t *testing.T) []byte {
	t.Helper()
	held, err := os.ReadFile(filepath.Join(a.hostDir, "efi", "efivars", dbxFile))
	if err != nil {
		t.Fatal(err)
	}
	return held
}

func TestAnApplyWritesTheRehearsedAppendInOneWrite(t *testing.T) {
	a := newApplyRig(t)

	got := a.run()

	want := ApplyResult{Result: resultWritten, Variable: dbxFile, SHA256: sum(authAppend), Before: 68, After: 76,
		Message: "wrote the 36-byte append to " + dbxFile + ", which grew from 68 to 76 bytes; the firmware merges the signed list into the stored one"}
	if got != want {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if want := []string{"signal", "flags", "setFlags 0x0", "write 36", "setFlags 0x10"}; !slices.Equal(a.efivars.calls, want) {
		t.Errorf("the calls are %q, want the signal, the flag cleared, one write, and the flag restored: %q", a.efivars.calls, want)
	}
}

func TestAnApplyLeavesAVariableWithNoImmutableFlagAlone(t *testing.T) {
	a := newApplyRig(t)
	a.efivars.held = 0

	a.run()

	if want := []string{"signal", "flags", "write 36"}; !slices.Equal(a.efivars.calls, want) {
		t.Errorf("the calls are %q, want %q", a.efivars.calls, want)
	}
}

// When the rehearsal before the write gives another append, or does
// not pass, the variables changed since the operator recorded the
// append, and the apply writes nothing, and does not wait for the
// signal.
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
				t.Errorf("got %+v, want Failed: %s", got, c.want)
			}
			if len(a.efivars.calls) != 0 || !slices.Equal(a.machineDBX(t), dbxBefore) {
				t.Errorf("the apply touched the machine's dbx with %q", a.efivars.calls)
			}
			if isPrepared(a.marker) {
				t.Error("the marker of a finished prepare exists, want none after a prepare that failed")
			}
		})
	}
}

// A catalog that moved since the scan is the operator's to read again,
// not a failure of the apply, so the prepare answers CatalogMoved. It
// writes nothing, marks nothing, and does not wait for the signal.
func TestAPrepareOfAMovedCatalogAnswersCatalogMoved(t *testing.T) {
	a := newApplyRig(t)
	a.rig.web[testCatalogURL+".jcat"] = jcatFor(t, "firmware-09139-stable.xml.zst", sum(testCatalog))

	got := a.run()

	if got.Result != resultCatalogMoved || got.Message != "the jcat now names firmware-09139-stable.xml.zst, so nothing was written" {
		t.Errorf("got %+v, want CatalogMoved with the new file", got)
	}
	if isPrepared(a.marker) || a.waited != "" || len(a.efivars.calls) != 0 {
		t.Errorf("the apply marked its prepare, waited, or touched the store with %q", a.efivars.calls)
	}
}

// The kernel reports the firmware's EFI_SECURITY_VIOLATION as EACCES,
// so a refused append is the firmware's answer, not a fault of the pod.
func TestTheFirmwaresRefusalIsRefused(t *testing.T) {
	a := newApplyRig(t)
	a.efivars.fail = map[string]error{"write": syscall.EACCES}

	got := a.run()

	if got.Result != resultRefused || got.Message != "the firmware refused the append to "+dbxFile+": permission denied" {
		t.Errorf("got %+v, want Refused", got)
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
			"writing the append to " + dbxFile + ": input/output error", []string{"signal", "flags", "setFlags 0x0", "write 36", "setFlags 0x10"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newApplyRig(t)
			a.efivars.fail = c.fail

			got := a.run()

			if got.Result != resultFailed || got.Message != c.want {
				t.Errorf("got %+v, want Failed: %s", got, c.want)
			}
			if !slices.Equal(a.efivars.calls, c.calls) {
				t.Errorf("the calls are %q, want %q", a.efivars.calls, c.calls)
			}
		})
	}
}

// A write that landed is Written even when the flag cannot be set
// again, because the variable holds the append either way.
func TestAWrittenAppendWhoseFlagStaysClearIsWritten(t *testing.T) {
	a := newApplyRig(t)
	a.efivars.fail = map[string]error{"restore": errors.New("the inode is busy")}

	got := a.run()

	if got.Result != resultWritten || !strings.HasSuffix(got.Message, "; setting the immutable flag again failed: the inode is busy") {
		t.Errorf("got %+v, want Written with the flag's failure", got)
	}
}

// The apply prepares everything that needs the network, prints that it
// is prepared, and only then waits for the signal. Nothing reaches the
// variable store until the signal arrives.
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
			t.Error("the machine's dbx changed before the signal")
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

// The operator takes the last line of the log that has a result as the
// apply's answer, so the progress line carries no result.
func TestTheProgressLineIsNotAResult(t *testing.T) {
	a := newApplyRig(t)

	a.run()

	lines := strings.Split(strings.TrimSpace(a.progress.String()), "\n")
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if _, has := first["result"]; has || len(lines) != 1 {
		t.Errorf("the apply printed %q, want one progress line with no result", lines)
	}
}

// A wait that ends without the signal, such as the pod's stop, writes
// nothing.
func TestAnApplyWithNoSignalWritesNothing(t *testing.T) {
	a := newApplyRig(t)
	a.arrives = func() error { return context.Canceled }

	got := a.run()

	if got.Result != resultFailed || got.Message != "waiting for the signal to write: context canceled, so nothing was written" {
		t.Errorf("got %+v, want Failed with nothing written", got)
	}
	if len(a.efivars.calls) != 0 || !slices.Equal(a.machineDBX(t), dbxBefore) {
		t.Errorf("the apply touched the machine's dbx with %q", a.efivars.calls)
	}
}

// The append extends the variable as the rehearsal copied it. A
// variable that changed while the pod waited for its turn, such as by
// another write, no longer matches, so the apply writes nothing.
func TestAVariableThatChangedDuringTheWaitIsNotWritten(t *testing.T) {
	a := newApplyRig(t)
	a.arrives = func() error {
		writeFile(t, filepath.Join(a.hostDir, "efi", "efivars", dbxFile), append(bytes.Clone(dbxBefore), 0xcc))
		return nil
	}

	got := a.run()

	if want := "the variable " + dbxFile + " changed while the apply waited for its turn, so nothing was written"; got.Result != resultFailed || got.Message != want {
		t.Errorf("got %+v, want Failed: %s", got, want)
	}
	if want := []string{"signal"}; !slices.Equal(a.efivars.calls, want) {
		t.Errorf("the calls are %q, want only the signal", a.efivars.calls)
	}
}

// The readiness probe runs `scan prepared`, which answers whether the
// marker exists.
func TestIsPreparedReadsTheMarker(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "prepared")
	if isPrepared(marker) {
		t.Error("isPrepared answers true with no marker")
	}
	writeFile(t, marker, nil)
	if !isPrepared(marker) {
		t.Error("isPrepared answers false with the marker")
	}
}

// A marker that cannot be written leaves the pod unready for good, so
// the apply ends before it waits, and the operator asks for no turn.
func TestAnApplyThatCannotMarkItsPrepareWritesNothing(t *testing.T) {
	a := newApplyRig(t)
	a.marker = filepath.Join(t.TempDir(), "absent", "prepared")

	got := a.run()

	if got.Result != resultFailed || !strings.HasPrefix(got.Message, "marking the prepare as done: ") || !strings.HasSuffix(got.Message, ", so nothing was written") {
		t.Errorf("got %+v, want Failed with nothing written", got)
	}
	if a.waited != "" || len(a.efivars.calls) != 0 {
		t.Errorf("the apply waited or touched the store with %q", a.efivars.calls)
	}
}

func TestAnApplierReadsItsEnvironment(t *testing.T) {
	env := map[string]string{"CABINET_URL": testCabinetURL, "DEVICE_ID": dbxID, "APPEND_SHA256": "cf901743"}

	a := applierFromEnv(func(key string) string { return env[key] })

	if a.appendSHA256 != "cf901743" || a.cabinetURL != testCabinetURL || a.deviceID != dbxID {
		t.Errorf("the applier is %+v, want the append and the rehearsal's environment", a)
	}
}
