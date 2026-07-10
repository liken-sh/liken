package main

// The cursor is how a restarted relay picks up where it left off
// instead of re-shipping everything it can see. It lives in the pod's
// emptyDir, and that choice is the design: an emptyDir survives
// container restarts (the common failure, a crash or an OOM kill) but
// dies with the pod and with the machine. Those deaths are exactly
// the moments a cursor must not survive. After a reboot the kernel's
// sequence numbers restart and the log files have rotated, so a
// persisted cursor would point into a previous boot; a recreated pod
// replaying from the head is the correct behavior, and the seq field
// in every envelope is what lets a consumer deduplicate the replay.
//
// Durable stores were considered and refused. Checkpointing through
// the cluster API would push node-local state that changes every
// batch through etcd, where each server pays a consensus round and a
// disk write for every update; and a cursor file on the host would
// give a read-only relay write access it otherwise never needs.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const cursorFile = "cursor.json"

// checkpointInterval throttles cursor writes: being at most a second
// behind re-ships a second of records after a container restart,
// which beats a disk write per record. A package variable rather
// than a constant so tests can checkpoint on every record.
var checkpointInterval = time.Second

// loadCursor reads the cursor into the given struct, reporting
// whether a usable cursor existed. Missing or corrupt cursors are a
// fresh start, never an error: the worst outcome of losing a cursor
// is one deduplicable replay.
func loadCursor(dir string, into any) bool {
	data, err := os.ReadFile(filepath.Join(dir, cursorFile))
	if err != nil {
		return false
	}
	return json.Unmarshal(data, into) == nil
}

// saveCursor writes the cursor with a temp-file-and-rename so a crash
// mid-write leaves the previous cursor intact rather than a torn one.
// The rename is atomic because both names live in the same emptyDir.
// There is deliberately no fsync: the emptyDir doesn't outlive the
// pod, so durability past a crash of the whole machine buys nothing.
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
