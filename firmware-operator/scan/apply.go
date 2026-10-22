package main

// An apply writes an approved Secure Boot database update to the
// machine's real variable, inside the reboot turn that the operator
// asked for. It runs in a pod on the drained machine, which mounts
// /sys/firmware read-write.
//
// The bytes the apply writes are the one append that the rehearsal
// recorded, and only those. So the apply first runs the rehearsal
// again, against a fresh copy in memory, and writes nothing unless the
// rehearsal passes with the same SHA-256 that the operator recorded. A
// different SHA-256 means the machine's variables changed since the
// rehearsal, and the record in status no longer describes the write.
//
// The firmware verifies the append's signature against KEK, or PK for
// a KEK update, and merges the signed list into the stored one. So the
// variable grows by less than the append, and an append that does not
// verify changes nothing. A write cannot be undone.

import (
	"context"
	"errors"
	"fmt"
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
	store        variableStore
}

func applierFromEnv(getenv func(string) string) applier {
	return applier{rehearser: rehearserFromEnv(getenv), appendSHA256: getenv("APPEND_SHA256")}
}

// apply rehearses again, checks the append, and writes it. Every
// outcome is an ApplyResult for the operator to read.
func (a applier) apply(ctx context.Context) ApplyResult {
	rehearsal, appended := a.rehearseAppend(ctx)
	if rehearsal.Result != resultPassed {
		return applyFailed("the rehearsal before the write did not pass, so nothing was written: %s", rehearsal.Message)
	}
	if rehearsal.SHA256 != a.appendSHA256 {
		return applyFailed("the append's SHA-256 is now %s, and the rehearsal recorded %s: the machine's variables changed since the rehearsal, so nothing was written",
			rehearsal.SHA256, a.appendSHA256)
	}
	variable := rehearsal.Variable
	path := filepath.Join(a.hostDir, "efi", "efivars", variable)
	before, err := os.ReadFile(path)
	if err != nil {
		return applyFailed("reading %s before the write: %v", variable, err)
	}

	flags, err := a.store.flags(path)
	if err != nil {
		return applyFailed("reading the flags of %s: %v", variable, err)
	}
	immutable := flags&immutableFlag != 0
	if immutable {
		if err := a.store.setFlags(path, flags&^immutableFlag); err != nil {
			return applyFailed("clearing the immutable flag of %s: %v", variable, err)
		}
	}
	written := a.store.write(path, appended)
	var restored error
	if immutable {
		restored = a.store.setFlags(path, flags)
	}

	var result ApplyResult
	switch {
	case errors.Is(written, syscall.EACCES):
		result = ApplyResult{Result: resultRefused, Message: fmt.Sprintf("the firmware refused the append to %s: %v", variable, written)}
	case written != nil:
		result = applyFailed("writing the append to %s: %v", variable, written)
	default:
		result = a.written(path, variable, len(appended), int64(len(before)))
	}
	if restored != nil {
		result.Message += "; setting the immutable flag again failed: " + restored.Error()
	}
	return result
}

// written answers the result of an append the firmware accepted, with
// the variable's size before and after.
func (a applier) written(path, variable string, length int, before int64) ApplyResult {
	result := ApplyResult{Result: resultWritten, Variable: variable, SHA256: a.appendSHA256, Before: before}
	after, err := os.ReadFile(path)
	if err != nil {
		result.Message = fmt.Sprintf("wrote the %d-byte append to %s, and reading it back failed: %v", length, variable, err)
		return result
	}
	result.After = int64(len(after))
	result.Message = fmt.Sprintf("wrote the %d-byte append to %s, which grew from %d to %d bytes; the firmware merges the signed list into the stored one",
		length, variable, result.Before, result.After)
	return result
}
