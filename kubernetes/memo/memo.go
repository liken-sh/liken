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
	return &Versions{seen: map[string]string{}, requests: map[string]*sync.Mutex{}}
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
	m.seen[key] = version
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
		delete(m.requests, key)
	}
}

// Forget drops the record of one object. An operator that reads an
// object by name from a store forgets it once the API server answers
// 404, so the next read of the name answers from the store and sends
// nothing.
func (m *Versions) Forget(key string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.seen, key)
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
