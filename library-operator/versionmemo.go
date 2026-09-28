package main

// A watch's store can hold a copy of an object that is older than this
// operator's own last write to it, because the watch delivers the write
// a moment after the API server answers it, and later still while the
// watch is down. A pass that acts on that copy acts again on a change it
// already made. It skips a status write because the older copy already
// holds the status the pass derives, while the API server holds the
// write before it. It reads a Catalog whose backfill has not finished,
// and creates the backfill Job again after the finished Job is gone.
//
// So the operator remembers the resourceVersion of each object's newest
// copy that it wrote or read from the API server, and objectcache.go
// reads an object from the API server when the store's copy has another
// version. Once the watch delivers the write, the versions match and the
// store answers again.
//
// The memo is in the pod build as well as the operator's, because the
// writes that note it are in both. The store it guards is in
// objectcache.go, which links client-go and is in the operator's build
// alone.

import "sync"

// versionMemo remembers, for each object by its store key, the
// resourceVersion of the newest copy this operator wrote or read from
// the API server. A version is compared only for equality, because the
// API server gives it no order. A nil memo remembers nothing, and every
// copy in a store is current to it.
type versionMemo struct {
	mu   sync.Mutex
	seen map[string]string

	// requests holds, for each object, one request at a time with the
	// note of its answer, so the memo notes the answers in the order the
	// API server gave them. Two goroutines that write one object could
	// otherwise note the older answer last, and a store's copy at that
	// older version would then count as current. Requests about other
	// objects do not wait.
	requests map[string]*sync.Mutex
}

func newVersionMemo() *versionMemo {
	return &versionMemo{seen: map[string]string{}, requests: map[string]*sync.Mutex{}}
}

// requestsOf answers the lock of one object's requests.
func (m *versionMemo) requestsOf(key string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	held, ok := m.requests[key]
	if !ok {
		held = &sync.Mutex{}
		m.requests[key] = held
	}
	return held
}

// current reports whether a store's copy at this version is at least as
// new as every copy this operator wrote or read.
func (m *versionMemo) current(key, version string) bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	seen, noted := m.seen[key]
	return !noted || seen == version
}

// note records the version of a copy the API server answered. An empty
// version records an object the API server no longer holds, or one
// another writer changed, and no copy in a store matches it.
func (m *versionMemo) note(key, version string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen[key] = version
}

// send runs one request about one object, and notes the version of the
// copy the API server answered. A failed request notes the empty
// version: after a 404 or a 409 the operator holds no copy of the API
// server's, and a write whose answer was lost may have landed. The
// next read of the object then goes to the API server.
func (m *versionMemo) send(key string, request func() (version string, err error)) error {
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
	m.note(key, version)
	return err
}

// objectVersions is one memo for each kind this operator writes and
// reads from a watch's store: the Libraries, the Catalogs, and the
// MetadataProviders. The operator writes the Plays and the people only
// by a patch that states the resourceVersion of the copy it read, and
// acts on nothing else of theirs that it wrote, so an older copy of one
// costs a 409 and a new try on the next pass, and their stores need no
// memo.
type objectVersions struct {
	libraries *versionMemo
	catalogs  *versionMemo
	providers *versionMemo
}

func newObjectVersions() objectVersions {
	return objectVersions{libraries: newVersionMemo(), catalogs: newVersionMemo(), providers: newVersionMemo()}
}

// storeKey is the key a store holds a namespaced object under.
func storeKey(namespace, name string) string { return namespace + "/" + name }
