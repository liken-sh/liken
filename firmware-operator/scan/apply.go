package main

// An apply writes an approved Secure Boot database update to the
// machine's real variable, inside the reboot turn that the operator
// asked for. It runs in a pod on the machine, which mounts
// /sys/firmware read-write.
//
// The pod runs in two phases. The drain of the machine's turn can evict
// the cluster's DNS, so the first phase, the prepare, does all the work
// that needs the network, before the operator asks for the turn. When
// the prepare passes, the apply creates a marker file, and the pod's
// readiness probe runs `scan prepared`, which reads it. So the pod turns
// Ready when the prepare is done, and the operator then asks for the
// turn. The second phase writes with no network, and starts only when
// the operator sends the signal (signal.go), once the machine is drained
// and its turn holds. The pod's variable store
// is reachable only from the write, and the write runs only after the
// wait for the signal returns, so nothing is written before the signal.
//
// The bytes the apply writes are the one append that the rehearsal
// recorded, and only those. So the prepare runs the rehearsal again,
// against a fresh copy in memory, and the apply writes nothing unless
// the rehearsal passes with the same SHA-256 that the operator recorded.
// A different SHA-256 means the machine's variables changed since the
// rehearsal, and the record in status no longer describes the write.
// The wait for the turn can be long, so before the write the apply
// reads the variable again, and writes nothing unless it still holds
// the bytes that the prepare's rehearsal extended.
//
// The firmware verifies the append's signature against KEK, or PK for
// a KEK update, and merges the signed list into the stored one. So the
// variable grows by less than the append, and an append that does not
// verify changes nothing. A write cannot be undone.

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

// resultWritten is an apply whose append the firmware accepted. The
// other results of an apply are Refused, the firmware's refusal, and
// Failed, an apply that wrote nothing or could not finish.
const resultWritten = "Written"

// immutableFlag is FS_IMMUTABLE_FL of linux/fs.h. efivarfs sets it on
// every variable except the few it knows are safe to remove, so that a
// stray rm cannot delete one that the firmware needs, and a write fails
// with EPERM while it is set.
const immutableFlag = 0x10

// ApplyResult is the one line of JSON that an apply prints. Before and
// After are the variable's size around the write, which a person reads
// to see the firmware's merge.
type ApplyResult struct {
	Result   string `json:"result"`
	Message  string `json:"message"`
	Variable string `json:"variable,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Before   int64  `json:"before,omitempty"`
	After    int64  `json:"after,omitempty"`
}

// phasePrepared is the phase of the progress line that the prepare
// prints when it ends.
const phasePrepared = "prepared"

// ApplyProgress is a line of JSON that an apply prints before its
// result, for a person who reads the pod's log. It has no result key,
// so the operator, which takes the last line with a result as the
// answer, never reads it as one.
type ApplyProgress struct {
	Phase    string `json:"phase"`
	Message  string `json:"message"`
	Variable string `json:"variable"`
	SHA256   string `json:"sha256"`
}

func applyFailed(format string, args ...any) ApplyResult {
	return ApplyResult{Result: resultFailed, Message: fmt.Sprintf(format, args...)}
}

// variableStore is the kernel's interface to one variable file of
// efivarfs: its inode flags, and the write. The pod's is in
// apply_linux.go, and a test gives a fake that records each call.
type variableStore struct {
	flags    func(path string) (uint32, error)
	setFlags func(path string, flags uint32) error
	// write writes data to the file in one write call, through a file
	// opened to append. efivarfs takes each call as one SetVariable,
	// and refuses a seek, so the four attribute bytes and the payload
	// must go together.
	write func(path string, data []byte) error
}

// applier is a rehearser that writes what it rehearsed. Its hostDir is
// the machine's /sys/firmware, mounted read-write.
type applier struct {
	rehearser
	appendSHA256 string // APPEND_SHA256, the append the rehearsal recorded
	// marker is the file that the apply creates when its prepare
	// passed, which the readiness probe reads.
	marker string
	// signal returns nil once the operator sent the signal to write,
	// and an error when the wait ended without it.
	signal func(ctx context.Context) error
	store  variableStore
}

// isPrepared reports whether an apply's prepare passed: whether its
// marker exists. `scan prepared` answers it for the readiness probe.
func isPrepared(marker string) bool {
	_, err := os.Stat(marker)
	return err == nil
}

func applierFromEnv(getenv func(string) string) applier {
	return applier{rehearser: rehearserFromEnv(getenv), appendSHA256: getenv("APPEND_SHA256")}
}

// apply prepares, prints its progress line, marks the prepare as done,
// waits for the signal, and writes. Every outcome is an ApplyResult for the operator to read.
// The prepare has the scan's time limit, because a working network
// finishes it in seconds. The wait has none, because the turn can come
// hours or days later, and the operator deletes the pod when the turn
// is no longer wanted.
func (a applier) apply(ctx context.Context, progress io.Writer) ApplyResult {
	preparing, cancel := context.WithTimeout(ctx, scanTimeout)
	p, failure, ready := a.prepare(preparing, a.appendSHA256)
	cancel()
	if !ready {
		return failure
	}
	line := ApplyProgress{Phase: phasePrepared, Variable: p.variable, SHA256: a.appendSHA256,
		Message: fmt.Sprintf("the rehearsal passed with the recorded append to %s; the apply waits for the operator's signal to write", p.variable)}
	if err := emit(progress, line); err != nil {
		return applyFailed("printing the progress line: %v, so nothing was written", err)
	}
	if err := os.WriteFile(a.marker, nil, 0o644); err != nil {
		return applyFailed("marking the prepare as done: %v, so nothing was written", err)
	}
	if err := a.signal(ctx); err != nil {
		return applyFailed("waiting for the signal to write: %v, so nothing was written", err)
	}
	return p.write(a.store)
}

// prepared is a rehearsal that passed with the recorded append: the
// variable, its file in the machine's store, the bytes the rehearsal
// copied from it, and the append.
type prepared struct {
	variable string
	path     string
	before   []byte
	appended []byte
}

// prepare rehearses again and checks the append. It is the rehearser's,
// which holds no variable store, so it cannot write.
func (r rehearser) prepare(ctx context.Context, appendSHA256 string) (prepared, ApplyResult, bool) {
	rehearsal, edit := r.rehearseAppend(ctx)
	if rehearsal.Result != resultPassed {
		return prepared{}, applyFailed("the rehearsal before the write did not pass, so nothing was written: %s", rehearsal.Message), false
	}
	if rehearsal.SHA256 != appendSHA256 {
		return prepared{}, applyFailed("the append's SHA-256 is now %s, and the rehearsal recorded %s: the machine's variables changed since the rehearsal, so nothing was written",
			rehearsal.SHA256, appendSHA256), false
	}
	return prepared{variable: rehearsal.Variable, path: filepath.Join(r.hostDir, "efi", "efivars", rehearsal.Variable),
		before: edit.before, appended: edit.appended}, ApplyResult{}, true
}

// write checks that the variable still holds what the prepare
// extended, and writes the append.
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
	written := store.write(p.path, p.appended)
	var restored error
	if immutable {
		restored = store.setFlags(p.path, flags)
	}

	var result ApplyResult
	switch {
	case errors.Is(written, syscall.EACCES):
		result = ApplyResult{Result: resultRefused, Message: fmt.Sprintf("the firmware refused the append to %s: %v", p.variable, written)}
	case written != nil:
		result = applyFailed("writing the append to %s: %v", p.variable, written)
	default:
		result = p.written(int64(len(before)))
	}
	if restored != nil {
		result.Message += "; setting the immutable flag again failed: " + restored.Error()
	}
	return result
}

// written answers the result of an append the firmware accepted, with
// the variable's size before and after.
func (p prepared) written(before int64) ApplyResult {
	result := ApplyResult{Result: resultWritten, Variable: p.variable, SHA256: sum(p.appended), Before: before}
	after, err := os.ReadFile(p.path)
	if err != nil {
		result.Message = fmt.Sprintf("wrote the %d-byte append to %s, and reading it back failed: %v", len(p.appended), p.variable, err)
		return result
	}
	result.After = int64(len(after))
	result.Message = fmt.Sprintf("wrote the %d-byte append to %s, which grew from %d to %d bytes; the firmware merges the signed list into the stored one",
		len(p.appended), p.variable, result.Before, result.After)
	return result
}
