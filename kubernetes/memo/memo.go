// Package memo records the resourceVersion that the API server returned
// for each object an operator wrote or read.
//
// An operator reads most objects from a watch's local store, and the
// store can be behind the operator's own last write: the watch delivers
// the write some time after the API server responds to it, and not at
// all while the watch is down. A pass that acts on the older copy
// repeats a change it already made, such as opening a pairing window or
// sending a receiver a setting, or skips a status write it still needs.
//
// So the operator records the version in each response, and reads an
// object from the API server while the store's copy has a different
// version. When the watch delivers the write, the versions match and
// reads go back to the store.
//
// The package imports nothing from k8s.io, so a program that must not
// link client-go can use it.
package memo

import (
	"errors"
	"sync"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// Versions records, for each object by its store key, the
// resourceVersion of the last copy the operator wrote or read. A nil
// *Versions records nothing, and every copy in a store counts as
// current.
type Versions struct {
	mu      sync.Mutex
	records map[string]objectRecord

	// recordCount counts every call to setRecord, and recordedAt
	// has the value of recordCount at each key's latest record.
	// SendList compares them to find the keys that a request
	// recorded while the list was in flight.
	recordCount uint64
	recordedAt  map[string]uint64

	// requestLocks has one lock for each object. Send holds it from
	// the request until it records the response, so the records
	// follow the order of the API server's responses. Without it,
	// two goroutines that write one object could record the older
	// version last, and a store's copy at that version would count
	// as current.
	requestLocks map[string]*sync.Mutex
}

// objectRecord is the memo's record of one object. version is the
// version of the copy the API server last returned. An empty version
// with unknown false means the operator has no copy: it deleted the
// object, the API server returned 404, or the response had no version.
// unknown is true after a request failed with any other error, because
// the object may then exist at a version the operator never read. No
// store copy is current against either kind of record.
type objectRecord struct {
	version string
	unknown bool
}

// New returns an empty memo.
func New() *Versions {
	return &Versions{records: map[string]objectRecord{}, recordedAt: map[string]uint64{}, requestLocks: map[string]*sync.Mutex{}}
}

func (m *Versions) lockFor(key string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, ok := m.requestLocks[key]
	if !ok {
		lock = &sync.Mutex{}
		m.requestLocks[key] = lock
	}
	return lock
}

// Current reports whether a store's copy at this version matches the
// memo's record of the object. resourceVersions have no order, so only
// the recorded version is current. An object with no record is current
// at any version.
func (m *Versions) Current(key, version string) bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, noted := m.records[key]
	return !noted || (!record.unknown && record.version == version)
}

// Note records the version of a copy the API server returned. An empty
// version records that the operator has no copy, because it deleted the
// object or the API server returned 404, and no copy in a store is
// current against it.
func (m *Versions) Note(key, version string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setRecord(key, objectRecord{version: version})
}

// setRecord replaces the record of one object. The caller holds mu.
func (m *Versions) setRecord(key string, r objectRecord) {
	m.recordCount++
	m.records[key] = r
	m.recordedAt[key] = m.recordCount
}

// KeyGetter reads one object from a store by its key. client-go's
// cache.Store satisfies it.
type KeyGetter interface {
	GetByKey(key string) (item any, exists bool, err error)
}

// Unheld returns each key that is missing from the store and that the
// memo recorded with a version or as unknown: an object the operator
// wrote that the watch has not delivered yet, or one whose last request
// failed and which may exist. It leaves out a key recorded with no
// version, because that object is gone.
func (m *Versions) Unheld(store KeyGetter) []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for key, record := range m.records {
		if _, stored, _ := store.GetByKey(key); !stored && (record.unknown || record.version != "") {
			keys = append(keys, key)
		}
	}
	return keys
}

// Send runs one request about one object and records the version that
// request returns. A delete returns an empty version, and a 404 records
// an empty version too, because the object is gone. Any other error
// records the object as unknown. That includes a 409, which means the
// object exists, and a lost response to a create that may have
// succeeded. After any of these, no store copy is current, so the next
// read of the object goes to the API server.
func (m *Versions) Send(key string, request func() (version string, err error)) error {
	if m == nil {
		_, err := request()
		return err
	}
	lock := m.lockFor(key)
	lock.Lock()
	defer lock.Unlock()
	version, err := request()
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case err == nil:
		m.setRecord(key, objectRecord{version: version})
	case errors.Is(err, apiclient.ErrNotFound):
		m.setRecord(key, objectRecord{})
	default:
		m.setRecord(key, objectRecord{unknown: true})
	}
	return err
}

// SendList runs one list request and records the version of each
// object it returns, by key. A pass lists from the API server while a
// watch's store is not ready. The reflector's first list sets
// resourceVersion=0, so the API server's watch cache can serve it, and
// that cache can be behind etcd. The store can then become ready with
// copies older than the ones the pass listed, and the records keep the
// next pass from treating those copies as current.
//
// The caller must not set resourceVersion, so that the list is a
// consistent read and has every write that the API server acknowledged
// before it. Only then is it safe for the list to replace the versions
// recorded before it was sent.
//
// When a Send records an object while the list is in flight, the memo
// keeps the Send's version, because the list can hold a copy from
// before that write. If the Send's version is in fact the older one,
// the cost is one extra read from the API server.
//
// A failed list records nothing.
func (m *Versions) SendList(request func() (versions map[string]string, err error)) error {
	if m == nil {
		_, err := request()
		return err
	}
	m.mu.Lock()
	start := m.recordCount
	m.mu.Unlock()
	listed, err := request()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, version := range listed {
		if m.recordedAt[key] <= start {
			m.setRecord(key, objectRecord{version: version})
		}
	}
	return nil
}

// ForgetGone drops the record of each object that is missing from both
// listed and the store. An operator whose objects come and go, such as
// a Play for each film or a Job for each run, calls it after each list,
// or the memo keeps a record of every object for the life of the
// process.
//
// listed must hold the keys of a list that read Unheld's keys from the
// API server, as informer.CurrentList does for a whole store. Otherwise
// ForgetGone drops the record of an object the operator created that
// the watch has not delivered yet.
//
// A key that the store still has keeps its record, so the store's copy
// of a deleted object does not become current before the watch removes
// it. An unknown record stays too, because the object may exist and a
// pass without the record could create it again. The next list reads
// that object from the API server, which gives the record a version or
// none.
func (m *Versions) ForgetGone(store KeyGetter, listed map[string]bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, record := range m.records {
		if listed[key] || record.unknown {
			continue
		}
		if _, stored, _ := store.GetByKey(key); stored {
			continue
		}
		m.drop(key)
	}
}

// Forget drops the record of one object. An operator that reads an
// object by name from a store calls it after the API server returns
// 404, so the next read of the name comes from the store and sends no
// request.
//
// Forget and ForgetAt also drop the object's request lock. A Send that
// holds the old lock and a Send that starts afterwards with a new lock
// can run at once and record their versions out of order. So an
// operator that sends requests about one object from more than one
// goroutine must forget the object only while none of them is running.
func (m *Versions) Forget(key string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.drop(key)
}

// Noted reports whether the memo has any record of the key. A caller
// whose store has every object it can read treats an object missing
// from the store as absent only when Noted is false, because a record
// means the operator may have created the object a moment ago, or a
// request about it may have failed.
func (m *Versions) Noted(key string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, noted := m.records[key]
	return noted
}

// ForgetAt drops the record of one object only when the record has this
// version, which the caller reads from the store's copy. Once the store
// has caught up to the record, the record protects nothing, and if it
// stayed, each later write by another client would make the store's
// copy differ from it and cost a read from the API server. The
// comparison and the delete run under mu, so a Send that records a
// newer version in the meantime keeps its record.
func (m *Versions) ForgetAt(key, version string) {
	if m == nil || version == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if record, noted := m.records[key]; noted && !record.unknown && record.version == version {
		m.drop(key)
	}
}

// drop removes the record, the entry in recordedAt, and the request
// lock of one object. The caller holds mu.
func (m *Versions) drop(key string) {
	delete(m.records, key)
	delete(m.recordedAt, key)
	delete(m.requestLocks, key)
}
