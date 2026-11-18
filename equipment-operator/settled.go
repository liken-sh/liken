package main

// The operator compares each setting that the receiver reports with
// the reported value, so a restart loses nothing for those settings. A
// setting that the receiver does not report can only be compared with
// what the operator sent earlier, and a restart clears that memory. So
// the operator saves a digest of each block it sent in
// status.settledSettings, and after a restart sends a block's
// unreported settings again only when the block's digest differs.
//
// The record is keyed on a digest of each block, not on
// metadata.generation, because the generation also increases on edits
// that change no setting, such as a label or a spec.power change.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"sync"
)

// The paths of the declared blocks, used as the keys of the record and
// of the send budget.
const (
	denonSettingsBlock = "spec.denon.settings"
	wiimSettingsBlock  = "spec.wiim.settings"
	zonesBlock         = "spec.zones"
)

// settledRecord maps the path of each block that the operator has sent
// to the digest of the block that it sent.
type settledRecord struct {
	mutex   sync.Mutex
	digests map[string]string
}

// newSettledRecord builds the record from the Receiver's status. A
// status with settingsGeneration and no settledSettings was written by
// an earlier operator version, which had already sent the declared
// blocks. So the record starts with the digest of each block in the
// current spec, and an upgrade sends nothing. A status with neither
// field starts an empty record, so the operator sends the unreported
// settings once.
func newSettledRecord(stored ReceiverStoredStatus, spec ReceiverSpec) *settledRecord {
	record := &settledRecord{digests: maps.Clone(stored.SettledSettings)}
	if record.digests == nil {
		record.digests = map[string]string{}
	}
	if stored.SettledSettings == nil && stored.SettingsGeneration != 0 {
		for block, declared := range declaredBlocks(spec) {
			record.digests[block] = digest(declared)
		}
	}
	return record
}

func declaredBlocks(spec ReceiverSpec) map[string]any {
	blocks := map[string]any{zonesBlock: spec.Zones}
	if spec.Denon != nil {
		blocks[denonSettingsBlock] = spec.Denon.Settings
	}
	if spec.Wiim != nil {
		blocks[wiimSettingsBlock] = spec.Wiim.Settings
	}
	return blocks
}

// digest returns the first 8 bytes of the SHA-256 of a block's JSON, in
// hex. json.Marshal writes struct fields in declaration order and sorts
// map keys, so equal blocks have the same digest.
func digest(block any) string {
	encoded, _ := json.Marshal(block)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:8])
}

// holds reports whether the digest recorded at path matches block.
func (r *settledRecord) holds(path string, block any) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	recorded, found := r.digests[path]
	return found && recorded == digest(block)
}

// settle records block as sent at path. It returns true when the
// record changed, so the caller must write the status.
func (r *settledRecord) settle(path string, block any) bool {
	sum := digest(block)
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.digests[path] == sum {
		return false
	}
	r.digests[path] = sum
	return true
}

// snapshot returns a copy of the record for the status, or nil when the
// record is empty.
func (r *settledRecord) snapshot() map[string]string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if len(r.digests) == 0 {
		return nil
	}
	return maps.Clone(r.digests)
}
