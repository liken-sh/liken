package main

// `scan apply` installs an approved update on the machine during the
// machine's reboot turn: it writes a Secure Boot database update to the
// real EFI variable, or stages a capsule with fwupd (capsule.go). It
// runs in a pod on the machine, with /sys/firmware mounted read-write.
//
// The apply runs in two phases, because draining the machine for its
// turn can evict the cluster's DNS. The prepare does everything that
// needs the network, and then creates a marker file. The pod's
// readiness probe runs `scan prepared`, which checks for that file, and
// the operator requests the turn only after the pod is Ready. Then the
// apply waits for the operator's signal (signal.go), which the operator
// sends when the machine is drained and its turn begins. Nothing calls
// the variable store before the signal arrives.
//
// For a database update, the apply writes the authenticated writes
// that the rehearsal recorded and nothing else, each in its own write
// call, in the rehearsal's order (authwrites.go). The prepare runs the
// rehearsal again on a fresh copy of the variables, and the apply
// fails unless the append has the SHA-256 that the operator recorded.
// A different SHA-256 means the machine's variables changed since the
// rehearsal. The wait for the turn can be long, so just before the
// write the apply reads the variable again and fails if it differs
// from the copy the prepare extended.
//
// The firmware verifies each write's signature against the KEK, or
// against the PK for a KEK update, and merges the signed lists into the
// stored ones, so the variable grows by at most the size of the append.
// A write that does not verify changes nothing, and an accepted write
// cannot be undone. So when the firmware refuses or fails a write after
// it accepted an earlier one, the apply returns Uncertain. The operator
// then reboots the machine in its turn, and the next scan reads what
// the firmware stores.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Written means the firmware accepted every write. Uncertain means it
// accepted some writes and then refused or failed one. An apply also
// returns Refused when the firmware refused the first write,
// CatalogMoved, and Failed.
const (
	resultWritten   = "Written"
	resultUncertain = "Uncertain"
)

// immutableFlag is FS_IMMUTABLE_FL of linux/fs.h. efivarfs sets it on
// most variables, so that an rm cannot delete a variable the firmware
// needs. A write fails with EPERM while the flag is set.
const immutableFlag = 0x10

// ApplyResult is the line of JSON that an apply prints as its result.
// Before and After are the variable's size in bytes, so a person can
// see how much the firmware merged.
type ApplyResult struct {
	Result   string `json:"result"`
	Message  string `json:"message"`
	Variable string `json:"variable,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Before   int64  `json:"before,omitempty"`
	After    int64  `json:"after,omitempty"`
}

// phasePrepared is the Phase of the progress line that the apply prints
// when its prepare passes.
const phasePrepared = "prepared"

// ApplyProgress is a line of JSON that an apply prints before its
// result, for a person who reads the pod's log. It has no result key,
// because the operator reads the last log line that has one as the
// result.
type ApplyProgress struct {
	Phase    string `json:"phase"`
	Message  string `json:"message"`
	Variable string `json:"variable"`
	SHA256   string `json:"sha256"`
}

func applyFailed(format string, args ...any) ApplyResult {
	return ApplyResult{Result: resultFailed, Message: fmt.Sprintf(format, args...)}
}

// variableStore reads and sets the inode flags of an efivarfs variable
// file and writes to it. apply_linux.go implements it with system
// calls, and the tests use a fake.
type variableStore struct {
	flags    func(path string) (uint32, error)
	setFlags func(path string, flags uint32) error
	// write appends data to the file in one write call. efivarfs turns
	// each write call into one SetVariable and refuses a seek, so the
	// four attribute bytes and the payload must go in the same call.
	write func(path string, data []byte) error
}

// replace writes the whole variable in one call, which efivarfs sends
// to the firmware as a SetVariable that replaces it. It does not
// truncate the file, because efivarfs refuses a truncation. The
// immutable flag is cleared for the write and set again afterward.
func (s variableStore) replace(path string, data []byte) error {
	flags, err := s.flags(path)
	if err != nil {
		return err
	}
	if flags&immutableFlag != 0 {
		if err := s.setFlags(path, flags&^immutableFlag); err != nil {
			return err
		}
		defer s.setFlags(path, flags)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// applier is a rehearser that also writes what it rehearsed. For an
// applier, hostDir is the machine's /sys/firmware, mounted read-write.
type applier struct {
	rehearser
	appendSHA256 string // APPEND_SHA256, the append the rehearsal recorded
	// marker is the file that the apply creates when its prepare
	// passes.
	marker string
	// signal blocks until the operator sends the signal to write. It
	// returns nil when the signal arrives, and an error when the wait
	// ends without it.
	signal func(ctx context.Context) error
	store  variableStore
	// installer runs fwupdtool against the machine's real firmware, for
	// a capsule.
	installer fwupdtool
}

// isPrepared reports whether the marker file exists. `scan prepared`
// calls it for the readiness probe.
func isPrepared(marker string) bool {
	_, err := os.Stat(marker)
	return err == nil
}

func applierFromEnv(getenv func(string) string) applier {
	return applier{rehearser: rehearserFromEnv(getenv), appendSHA256: getenv("APPEND_SHA256")}
}

// apply returns every outcome, including a failure, as an ApplyResult
// for the operator to read. The prepare has the scan's timeout. The
// wait for the signal has none, because the turn can come hours or
// days later. The operator ends the wait by deleting the pod.
func (a applier) apply(ctx context.Context, progress io.Writer) ApplyResult {
	preparing, cancel := context.WithTimeout(ctx, scanTimeout)
	p, failure, ready := a.prepare(preparing, a.appendSHA256)
	cancel()
	if !ready {
		return failure
	}
	line := ApplyProgress{Phase: phasePrepared, Variable: p.variable, SHA256: a.appendSHA256,
		Message: fmt.Sprintf("the rehearsal passed with the recorded append to %s, and the apply waits for the operator's signal to write", p.variable)}
	if p.capsule {
		line.Message = "the rehearsal passed with the recorded writes of the capsule, and the apply waits for the operator's signal to install it"
	}
	if err := emit(progress, line); err != nil {
		return applyFailed("printing the progress line: %v, so nothing was written", err)
	}
	if err := os.WriteFile(a.marker, nil, 0o644); err != nil {
		return applyFailed("marking the prepare as done: %v, so nothing was written", err)
	}
	if err := a.signal(ctx); err != nil {
		return applyFailed("waiting for the signal to write: %v, so nothing was written", err)
	}
	if p.capsule {
		return a.stageCapsule(ctx, p)
	}
	return p.write(a.store)
}

// prepared is a rehearsal that passed with the recorded append.
type prepared struct {
	variable string
	// path is the variable's file in efivarfs.
	path string
	// before is the variable as the rehearsal copied it.
	before []byte
	writes [][]byte
	// capsule is true for a capsule update, whose apply installs cabinet
	// with fwupd (stageCapsule) instead of writing a variable.
	capsule bool
	cabinet string
}

// prepare runs the rehearsal again and checks its append against the
// SHA-256 that the operator recorded. It is a method of rehearser, and
// not of applier, so that it has no variable store to write with.
func (r rehearser) prepare(ctx context.Context, appendSHA256 string) (prepared, ApplyResult, bool) {
	rehearsal, edit := r.rehearseAppend(ctx)
	if rehearsal.Result == resultCatalogMoved {
		return prepared{}, ApplyResult{Result: resultCatalogMoved, Message: rehearsal.Message + ", so nothing was written"}, false
	}
	if rehearsal.Result != resultPassed {
		return prepared{}, applyFailed("the rehearsal before the write did not pass, so nothing was written: %s", rehearsal.Message), false
	}
	if rehearsal.SHA256 != appendSHA256 {
		return prepared{}, applyFailed("the append's SHA-256 is now %s, and the rehearsal recorded %s: the machine's variables changed since the rehearsal, so nothing was written",
			rehearsal.SHA256, appendSHA256), false
	}
	if rehearsal.Writes != nil {
		return prepared{capsule: true, cabinet: edit.cabinet}, ApplyResult{}, true
	}
	return prepared{variable: rehearsal.Variable, path: filepath.Join(r.hostDir, "efi", "efivars", rehearsal.Variable),
		before: edit.before, writes: edit.writes}, ApplyResult{}, true
}

// write checks that the variable still matches the copy that the
// prepare extended, and then sends each authenticated write. It stops
// at the first write that fails.
func (p prepared) write(store variableStore) ApplyResult {
	before, err := os.ReadFile(p.path)
	if err != nil {
		return applyFailed("reading %s before the write: %v", p.variable, err)
	}
	if !bytes.Equal(before, p.before) {
		return applyFailed("the variable %s changed while the apply waited for its turn, so nothing was written", p.variable)
	}

	flags, err := store.flags(p.path)
	if err != nil {
		return applyFailed("reading the flags of %s: %v", p.variable, err)
	}
	immutable := flags&immutableFlag != 0
	if immutable {
		if err := store.setFlags(p.path, flags&^immutableFlag); err != nil {
			return applyFailed("clearing the immutable flag of %s: %v", p.variable, err)
		}
	}
	accepted, written := 0, error(nil)
	for _, w := range p.writes {
		if written = store.write(p.path, w); written != nil {
			break
		}
		accepted++
	}
	var restored error
	if immutable {
		restored = store.setFlags(p.path, flags)
	}

	var result ApplyResult
	switch {
	case written != nil && accepted > 0:
		result = ApplyResult{Result: resultUncertain, Message: fmt.Sprintf("the variable %s changed, so the machine must reboot: the firmware accepted %d of the %d authenticated writes, and write %d returned: %v",
			p.variable, accepted, len(p.writes), accepted+1, written)}
	case errors.Is(written, syscall.EACCES):
		result = ApplyResult{Result: resultRefused, Message: fmt.Sprintf("the firmware refused the append to %s: %v", p.variable, written)}
	case written != nil:
		result = applyFailed("writing the append to %s: %v", p.variable, written)
	default:
		result = p.written(int64(len(before)))
	}
	if restored != nil {
		result.Message += ", and the immutable flag was not set again: " + restored.Error()
	}
	return result
}

func (p prepared) written(before int64) ApplyResult {
	appended := bytes.Join(p.writes, nil)
	result := ApplyResult{Result: resultWritten, Variable: p.variable, SHA256: sum(appended), Before: before}
	what := fmt.Sprintf("the %d-byte append to %s", len(appended), p.variable)
	if len(p.writes) > 1 {
		what += fmt.Sprintf(" in %d authenticated writes", len(p.writes))
	}
	after, err := os.ReadFile(p.path)
	if err != nil {
		result.Message = fmt.Sprintf("wrote %s, and reading it back failed: %v", what, err)
		return result
	}
	result.After = int64(len(after))
	result.Message = fmt.Sprintf("wrote %s, which grew from %d to %d bytes because the firmware merges the signed lists into the stored ones",
		what, result.Before, result.After)
	return result
}
