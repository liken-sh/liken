// Package memo records the resourceVersion of the newest copy of each
// object that an operator wrote or read from the API server.
//
// An operator reads the objects that a watch holds from the watch's
// store, not from the API server. A store's copy can be older than the
// operator's own last write, because the watch delivers the write a
// moment after the API server answers it, and later still while the
// watch is down. A pass that acts on that copy acts again on a change it
// already made: it opens a pairing window again, sends a receiver a
// setting again, or skips a status write that the older copy hides. So
// the operator records the version of each copy the API server answered,
// and reads an object from the API server when the store's copy has
// another version. When the watch delivers the write, the versions
// match and the store answers again.
//
// The package imports nothing from k8s.io, so a program that must not
// link client-go can record its writes too.
package memo

import "sync"

// Versions records, for each object by its store key, the
// resourceVersion of the newest copy the operator wrote or read from
// the API server. It compares versions only for equality, because the
// API server gives them no order. A nil *Versions records nothing, and
// every copy in a store is current to it.
type Versions struct {
	mu   sync.Mutex
	seen map[string]string

	// notes counts every note, and notedAt holds the count at each
	// key's last note. A list compares the two to find the keys that a
	// request noted while the list was in flight (SendList).
	notes   uint64
	notedAt map[string]uint64

	// requests holds, for each object, one request at a time with the
	// note of its answer, so the notes follow the order in which the API
	// server answered. Two goroutines that write one object could
	// otherwise note the older answer last, and a store's copy at that
	// older version would then count as current. Requests about other
	// objects do not wait.
	requests map[string]*sync.Mutex
}

// New answers an empty memo.
func New() *Versions {
	return &Versions{seen: map[string]string{}, notedAt: map[string]uint64{}, requests: map[string]*sync.Mutex{}}
}

// requestsOf answers the lock of one object's requests.
func (m *Versions) requestsOf(key string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	held, ok := m.requests[key]
	if !ok {
		held = &sync.Mutex{}
		m.requests[key] = held
	}
	return held
}

// Current reports whether a store's copy at this version is at least as
// new as every copy the operator wrote or read. An object the memo has
// not noted is current at any version.
func (m *Versions) Current(key, version string) bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	seen, noted := m.seen[key]
	return !noted || seen == version
}

// Note records the version of a copy the API server answered. An empty
// version records an object the API server no longer holds, or one
// that another writer changed, and no copy in a store matches it.
func (m *Versions) Note(key, version string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.note(key, version)
}

// note records one version. The caller holds mu.
func (m *Versions) note(key, version string) {
	m.notes++
	m.seen[key] = version
	m.notedAt[key] = m.notes
}

// KeyGetter reads one object from a store by its key. client-go's
// cache.Store satisfies it.
type KeyGetter interface {
	GetByKey(key string) (item any, exists bool, err error)
}

// Unheld answers each key the memo noted at a version that the store
// does not hold. These are objects the operator created or wrote a
// moment ago, which the watch has not delivered yet.
func (m *Versions) Unheld(store KeyGetter) []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for key, version := range m.seen {
		if _, held, _ := store.GetByKey(key); !held && version != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

// Send runs one request about one object, and notes the version of the
// copy the API server answered. A failed request notes the empty
// version: after a 404 or a 409 the operator holds no copy of the API
// server's, and a write whose answer was lost may have landed. The next
// read of the object then goes to the API server.
func (m *Versions) Send(key string, request func() (version string, err error)) error {
	if m == nil {
		_, err := request()
		return err
	}
	requests := m.requestsOf(key)
	requests.Lock()
	defer requests.Unlock()
	version, err := request()
	if err != nil {
		version = ""
	}
	m.Note(key, version)
	return err
}

// SendList runs one list request, and notes the version of each object
// the API server answered, by its key. A pass that lists before a
// watch's store is ready must note these versions. The store can then
// become ready at an older version, because the reflector's first read
// can come from the API server's watch cache, which runs behind etcd.
// Without the notes, the next pass reads that older copy as current and
// acts on an object older than one it already read.
//
// An object that a request noted while the list was in flight keeps the
// request's version. The list can hold a copy from before the request's
// answer, and a note of that copy would make a store's copy at the older
// version current. When the request's version is the older one instead,
// the store's copy at the list's version is not current, and the next
// read of the object goes to the API server once. That costs one
// request and never answers an older copy.
//
// A list replaces the version of each object that the memo noted before
// the list was sent. That is correct only for a list that etcd answers,
// which holds every write the API server answered before the list. A
// list with resourceVersion=0 comes from the watch cache, which can be
// older than such a write, so the caller must not send one.
//
// A list that fails notes nothing, because it names no object.
func (m *Versions) SendList(request func() (versions map[string]string, err error)) error {
	if m == nil {
		_, err := request()
		return err
	}
	m.mu.Lock()
	start := m.notes
	m.mu.Unlock()
	listed, err := request()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, version := range listed {
		if m.notedAt[key] <= start {
			m.note(key, version)
		}
	}
	return nil
}

// ForgetGone drops the record of each object that a list did not
// answer and the store does not hold. The API server answered 404 for
// such an object, and its watch delivered the delete. An operator whose
// objects come and go, such as a Play for each film or a Job for each
// run, calls it after each list, or the memo would keep a record of
// every object for the life of the process. A key the store still holds
// keeps its record, so a copy of an object the API server already
// deleted is not current again before the watch removes it.
func (m *Versions) ForgetGone(store KeyGetter, listed map[string]bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.seen {
		if listed[key] {
			continue
		}
		if _, held, _ := store.GetByKey(key); held {
			continue
		}
		delete(m.seen, key)
		delete(m.notedAt, key)
		delete(m.requests, key)
	}
}

// Forget drops the record of one object. An operator that reads an
// object by name from a store forgets it once the API server answers
// 404, so the next read of the name answers from the store and sends
// nothing.
//
// Forget and ForgetAt also drop the object's request lock (Send). A
// Send that holds the lock at that moment runs to its end and notes its
// answer, but a Send that starts after the forget takes a new lock, so
// the two can run at once and note their answers out of order. An
// operator that sends requests about one object from more than one
// goroutine forgets the object only while none of them runs.
func (m *Versions) Forget(key string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.seen, key)
	delete(m.notedAt, key)
	delete(m.requests, key)
}

// Noted reports whether the memo holds a record of the key at any
// version, the empty one included. A store that holds a whole selection
// and not the key answers that no such object exists, unless the memo
// noted it: the operator created it a moment ago, or a request about it
// failed.
func (m *Versions) Noted(key string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, noted := m.seen[key]
	return noted
}

// ForgetAt drops the record of one object when the record holds this
// version, and keeps any other record. A caller passes the version of
// the copy a store holds: once the store holds the version the memo
// noted, every later copy the store holds is newer, so the record
// guards nothing. Without it, each later write from another writer
// makes the store's copy differ from the record, and costs one read
// from the API server. The check and the delete are one step, so a
// write that notes a newer version in the meantime keeps its record.
func (m *Versions) ForgetAt(key, version string) {
	if m == nil || version == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if seen, noted := m.seen[key]; noted && seen == version {
		delete(m.seen, key)
		delete(m.notedAt, key)
		delete(m.requests, key)
	}
}
