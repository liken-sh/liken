package main

import (
	"context"
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
// append the rehearsal recorded.
type applyRig struct {
	applier
	rig     *rehearsalRig
	efivars *fakeStore
}

func newApplyRig(t *testing.T) *applyRig {
	t.Helper()
	r := newRehearsalRig(t)
	efivars := &fakeStore{held: immutableFlag, merged: []byte{1, 2, 3, 4, 5, 6, 7, 8}}
	return &applyRig{rig: r, efivars: efivars,
		applier: applier{rehearser: r.rehearser, appendSHA256: sum(authAppend), store: efivars.store()}}
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

	got := a.apply(context.Background())

	want := ApplyResult{Result: resultWritten, Variable: dbxFile, SHA256: sum(authAppend), Before: 68, After: 76,
		Message: "wrote the 36-byte append to " + dbxFile + ", which grew from 68 to 76 bytes; the firmware merges the signed list into the stored one"}
	if got != want {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if want := []string{"flags", "setFlags 0x0", "write 36", "setFlags 0x10"}; !slices.Equal(a.efivars.calls, want) {
		t.Errorf("the calls are %q, want the flag cleared, one write, and the flag restored: %q", a.efivars.calls, want)
	}
}

func TestAnApplyLeavesAVariableWithNoImmutableFlagAlone(t *testing.T) {
	a := newApplyRig(t)
	a.efivars.held = 0

	a.apply(context.Background())

	if want := []string{"flags", "write 36"}; !slices.Equal(a.efivars.calls, want) {
		t.Errorf("the calls are %q, want %q", a.efivars.calls, want)
	}
}

// When the rehearsal before the write gives another append, or does
// not pass, the variables changed since the operator recorded the
// append, and the apply writes nothing.
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

			got := a.apply(context.Background())

			if got.Result != resultFailed || got.Message != c.want {
				t.Errorf("got %+v, want Failed: %s", got, c.want)
			}
			if len(a.efivars.calls) != 0 || !slices.Equal(a.machineDBX(t), dbxBefore) {
				t.Errorf("the apply touched the machine's dbx with %q", a.efivars.calls)
			}
		})
	}
}

// The kernel reports the firmware's EFI_SECURITY_VIOLATION as EACCES,
// so a refused append is the firmware's answer, not a fault of the pod.
func TestTheFirmwaresRefusalIsRefused(t *testing.T) {
	a := newApplyRig(t)
	a.efivars.fail = map[string]error{"write": syscall.EACCES}

	got := a.apply(context.Background())

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
			"reading the flags of " + dbxFile + ": inappropriate ioctl for device", []string{"flags"}},
		{"no capability to clear the flag", map[string]error{"setFlags": syscall.EPERM},
			"clearing the immutable flag of " + dbxFile + ": operation not permitted", []string{"flags", "setFlags 0x0"}},
		{"a write the kernel refuses", map[string]error{"write": syscall.EIO},
			"writing the append to " + dbxFile + ": input/output error", []string{"flags", "setFlags 0x0", "write 36", "setFlags 0x10"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newApplyRig(t)
			a.efivars.fail = c.fail

			got := a.apply(context.Background())

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

	got := a.apply(context.Background())

	if got.Result != resultWritten || !strings.HasSuffix(got.Message, "; setting the immutable flag again failed: the inode is busy") {
		t.Errorf("got %+v, want Written with the flag's failure", got)
	}
}

func TestAnApplierReadsItsEnvironment(t *testing.T) {
	env := map[string]string{"CABINET_URL": testCabinetURL, "DEVICE_ID": dbxID, "APPEND_SHA256": "cf901743"}

	a := applierFromEnv(func(key string) string { return env[key] })

	if a.appendSHA256 != "cf901743" || a.cabinetURL != testCabinetURL || a.deviceID != dbxID {
		t.Errorf("the applier is %+v, want the append and the rehearsal's environment", a)
	}
}
