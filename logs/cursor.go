package main

// The cursor lets a restarted relay resume from its last position
// instead of resending everything it can see. It lives in the pod's
// emptyDir, and that choice is deliberate. An emptyDir survives
// container restarts, which is the common failure (a crash or an OOM
// kill), but the emptyDir is removed with the pod and with the
// machine. The cursor must not survive those two events. After a
// reboot, the kernel's sequence numbers start over and the log files
// have rotated, so a saved cursor would point into a previous boot.
// A recreated pod that replays from the start is the correct
// behavior, and the seq field in every envelope is what lets a
// consumer remove the duplicate records from that replay.
//
// This file considered durable stores and rejected them.
// Checkpointing through the cluster API would push node-local state
// that changes on every batch through etcd, where each server pays
// for a consensus round and a disk write on every update. A cursor
// file on the host would give a read-only relay a write access it
// otherwise never needs.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const cursorFile = "cursor.json"

// checkpointInterval limits how often the relay writes the cursor.
// Being at most a second behind means the relay resends at most a
// second of records after a container restart, which costs less
// than a disk write for every record. This is a package variable,
// not a constant, so tests can checkpoint on every record.
var checkpointInterval = time.Second

// loadCursor reads the cursor into the given struct, and reports
// whether a usable cursor existed. A missing or corrupt cursor means
// a fresh start, never an error. The worst outcome of losing a
// cursor is one replay that the consumer can deduplicate.
func loadCursor(dir string, into any) bool {
	data, err := os.ReadFile(filepath.Join(dir, cursorFile))
	if err != nil {
		return false
	}
	return json.Unmarshal(data, into) == nil
}

// saveCursor writes the cursor using a temp-file-and-rename. If a
// crash happens mid-write, the previous cursor stays intact instead
// of becoming a torn cursor. The rename is atomic because both names
// live in the same emptyDir. This function does not call fsync on
// purpose: the emptyDir does not survive the pod, so durability
// across a crash of the whole machine gives no benefit.
func saveCursor(dir string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, cursorFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, cursorFile))
}
