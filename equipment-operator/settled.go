package main

// The record of the declared blocks the operator has sent. A field the
// receiver reports is compared with the report, so a restart needs no
// record for it. A field the receiver does not report can only be
// compared with what the operator sent before, and a restart starts
// with nothing in memory. So the operator keeps a digest of each block
// it sent in status.settledSettings, and after a restart it sends a
// block's unreported fields only when the block's digest differs.
//
// The digest covers one block and nothing else. metadata.generation
// counts every spec edit, including a label, spec.power, and a session
// that an older media operator writes in the spec, so a record keyed on
// it sends the unreported fields again after edits that change none of
// them.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"sync"
)

// The paths of the declared blocks, which key the record and the send
// budget.
const (
	denonSettingsBlock = "spec.denon.settings"
	wiimSettingsBlock  = "spec.wiim.settings"
	zonesBlock         = "spec.zones"
)

// settledRecord holds the digest of each block the operator has sent.
type settledRecord struct {
	mutex   sync.Mutex
	digests map[string]string
}

// newSettledRecord reads the record from the status the operator finds
// when it starts a unit. A status from an earlier operator holds
// settingsGeneration and no settledSettings. That operator had sent the
// declared blocks, so the record adopts each block the spec declares
// now, and an upgrade sends nothing. An empty status is a Receiver no
// operator has settled, and the record holds nothing, so the operator
// sends the unreported fields once.
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

// declaredBlocks answers each block of the spec the operator drives, by
// its path.
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

// digest answers a short hash of a block's JSON. JSON writes a struct's
// fields in one order and a map's keys sorted, so one block always has
// one digest.
func digest(block any) string {
	encoded, _ := json.Marshal(block)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:8])
}

// holds answers whether the record holds the block as it is declared
// now.
func (r *settledRecord) holds(path string, block any) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	held, found := r.digests[path]
	return found && held == digest(block)
}

// settle records the block as sent, and answers whether the record
// changed, which asks for a status write.
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

// snapshot answers a copy of the record for the status, and nil when it
// is empty.
func (r *settledRecord) snapshot() map[string]string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if len(r.digests) == 0 {
		return nil
	}
	return maps.Clone(r.digests)
}
