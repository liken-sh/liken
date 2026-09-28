package main

// The pass reads the Libraries, the Catalogs, and the MetadataProviders
// from the watches' stores, through the same types and functions every
// liken-sh operator reads its stores with. This file and objectcache.go
// hold them together. The functions are split across the two files only
// because of the pod build: the pods and Jobs run the same program with
// the build tag pod, which links no client-go (operate_pod.go says why).
// The writes that note the memo are in both builds, so the memo, the
// fresh read, and the status write are here. The store is a client-go
// type, so what reads a store is in objectcache.go, in the operator's
// build alone.
//
// A copy in a store can be older than this operator's own last write,
// because the watch delivers the write a moment after the API server
// answers it, and later still while the watch is down. The operator
// writes the status of all three kinds, and a pass acts on what that
// status holds:
//
//   - The pass compares the status it derives with the copy's status,
//     and writes only a difference. An older copy can hold the status
//     the pass derives while the API server holds the write after it, so
//     the pass would skip the write and leave the wrong status.
//   - The pass reads status.jellyfin on a Catalog to learn whether the
//     backfill finished. After the finished backfill Job is deleted, a
//     copy from before the write of Finished would make the pass create
//     the Job again.
//
// So the operator remembers the resourceVersion of each object's newest
// copy that it wrote or read from the API server (versionMemo), and
// reads an object from the API server when the store's copy has another
// version. Once the watch delivers the write, the versions match and the
// store answers again.
//
// A status write from a copy that another writer changed since carries
// an older resourceVersion, and the API server answers 409 Conflict.
// settleStatus then reads the object from the API server, composes the
// status again from the fresh copy, and writes once more if the fresh
// copy still needs the write.
//
// This operator's Client takes a context on every request, because a
// pass bounds all of its requests with one deadline (passTimeout). So
// every function here that sends a request takes that context first.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
)

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

// metaObject is a resource this operator writes and reads back from a
// store, which carries its metadata in ObjectMeta.
type metaObject interface{ meta() *ObjectMeta }

func (l *Library) meta() *ObjectMeta          { return &l.Metadata }
func (c *NamespaceCatalog) meta() *ObjectMeta { return &c.Metadata }
func (p *MetadataProvider) meta() *ObjectMeta { return &p.Metadata }
func (s *Service) meta() *ObjectMeta          { return &s.Metadata }
func (s *EndpointSlice) meta() *ObjectMeta    { return &s.Metadata }
func (m *ConfigMap) meta() *ObjectMeta        { return &m.Metadata }
func (j *Job) meta() *ObjectMeta              { return &j.Metadata }
func (t *ResourceClaimTemplate) meta() *ObjectMeta {
	return &t.Metadata
}

// storeKey is the key a store holds an object under: namespace/name for
// a namespaced object and the name for a cluster-scoped one.
func storeKey(meta *ObjectMeta) string {
	if meta.Namespace == "" {
		return meta.Name
	}
	return meta.Namespace + "/" + meta.Name
}

// readFresh reads one object from the API server and notes its version.
func readFresh[T any, P interface {
	*T
	metaObject
}](ctx context.Context, c *Client, versions *versionMemo, key, path string) (*T, error) {
	var fresh *T
	err := versions.send(key, func() (string, error) {
		var err error
		if fresh, err = get[T](ctx, c, path); err != nil {
			return "", err
		}
		return P(fresh).meta().ResourceVersion, nil
	})
	return fresh, err
}

// stale reports whether a write failed because the copy it was made
// from is not the API server's copy.
func stale(err error) bool {
	return errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound)
}

// settleStatus writes the status that apply sets on a copy of an
// object. apply composes the status from the copy it is given, sets it,
// and reports whether the copy needs the write. A write refused because
// the copy is older than the API server's, or because the object is
// gone, reads the object again, applies again to the fresh copy, and
// writes once more. It reports whether a write landed, and an object
// that is gone answers ErrNotFound. Each copy the API server answers
// is noted in versions. After an error, held carries the status that
// apply set, which the API server did not take.
func settleStatus[T any, P interface {
	*T
	metaObject
}](ctx context.Context, c *Client, versions *versionMemo, path string, held *T, apply func(*T) bool) (bool, error) {
	key := storeKey(P(held).meta())
	write := func() error {
		return versions.send(key, func() (string, error) {
			if err := replaceStatus(ctx, c, path, held); err != nil {
				return "", err
			}
			return P(held).meta().ResourceVersion, nil
		})
	}
	if !apply(held) {
		return false, nil
	}
	err := write()
	if !stale(err) {
		return err == nil, err
	}
	current, err := readFresh[T, P](ctx, c, versions, key, path)
	if err != nil {
		return false, err
	}
	*held = *current
	if !apply(held) {
		return false, nil
	}
	if err := write(); err != nil {
		return false, err
	}
	return true, nil
}

// get reads one object from the API server.
func get[T any](ctx context.Context, c *Client, path string) (*T, error) {
	out := new(T)
	if err := c.RequestJSON(ctx, http.MethodGet, path, nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

// replaceStatus writes an object's status subresource from the copy it
// holds, and puts the API server's answer, with its new
// resourceVersion, back on that copy.
func replaceStatus[T any](ctx context.Context, c *Client, path string, object *T) error {
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	stored := new(T)
	if err := c.RequestJSON(ctx, http.MethodPut, path+"/status", body, stored); err != nil {
		return err
	}
	*object = *stored
	return nil
}

// objectVersions is the memo of each kind this process writes and then
// reads back from a store to decide whether to write again: the status of
// the Libraries, the Catalogs, and the MetadataProviders, the whole of the
// Services, the EndpointSlices, and the people ConfigMaps it stands, and
// the worker Jobs and the trickplay templates it creates and deletes. A library Job's name holds the
// time it was created, so a second create of one never meets a 409, and
// the pass must read each Job it created before the watch delivers it
// (currentList on a whole store).
//
// The operator writes the Plays and the people only by a patch that
// states the resourceVersion of the copy it read, so a patch from an
// older copy fails with a 409 and the next pass sends it again. It
// creates and deletes the claims, the volumes, and the pods by a name
// that does not change: a create that meets one already there is a 409
// every stand reads as success, and a delete names the uid it read where
// a copy of the same name can follow. Their stores need no memo.
type objectVersions struct {
	libraries, catalogs, providers       *versionMemo
	services, endpointSlices, configMaps *versionMemo
	jobs, claimTemplates                 *versionMemo
}

func newObjectVersions() objectVersions {
	return objectVersions{libraries: newVersionMemo(), catalogs: newVersionMemo(), providers: newVersionMemo(),
		services: newVersionMemo(), endpointSlices: newVersionMemo(), configMaps: newVersionMemo(),
		jobs: newVersionMemo(), claimTemplates: newVersionMemo()}
}

// written sends one write of an object, and notes the version of the
// copy the API server answered. A write that fails answers no copy.
func written[T any, P interface {
	*T
	metaObject
}](versions *versionMemo, key string, request func() (*T, error)) (*T, error) {
	var answer *T
	err := versions.send(key, func() (string, error) {
		copied, err := request()
		if err != nil {
			return "", err
		}
		answer = copied
		return P(copied).meta().ResourceVersion, nil
	})
	return answer, err
}
