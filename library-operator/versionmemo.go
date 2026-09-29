package main

// The pass reads the Libraries, the Catalogs, and the MetadataProviders
// from the watches' stores, through the shared informer package. This
// file and objectcache.go hold what the operator adds to it. The two
// files are split only because of the pod build: the pods and Jobs run
// the same program with the build tag pod, which links no client-go
// (operate_pod.go says why). The writes that note the memo are in both
// builds, so they go through the shared memo package, which imports
// nothing from k8s.io, and this file holds the memos of each kind. The
// store is a client-go type, so what reads a store is in objectcache.go,
// in the operator's build alone.
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
// So the memo of each kind records the version of the newest copy this
// operator wrote or read, and the shared reads read an object from the
// API server when the store's copy has another version. Once the watch
// delivers the write, the versions match and the store answers again.
//
// A status write from a copy that another writer changed since carries
// an older resourceVersion, and the API server answers 409 Conflict.
// memo.SettleStatus then reads the object from the API server, composes
// the status again from the fresh copy, and writes once more if the
// fresh copy still needs the write.
//
// Every request carries a context, because a pass bounds all of its
// requests with one deadline (passTimeout). So a caller hands the shared
// functions the client that apiclient.Client.WithContext answers.

import (
	"github.com/liken-sh/liken/kubernetes/memo"
)

// The three getters are the metadata the shared memo and cache read
// from each kind this operator writes and reads back from a store.
func (m *ObjectMeta) GetName() string            { return m.Name }
func (m *ObjectMeta) GetNamespace() string       { return m.Namespace }
func (m *ObjectMeta) GetResourceVersion() string { return m.ResourceVersion }

func (l *Library) GetObjectMeta() memo.Meta               { return &l.Metadata }
func (c *NamespaceCatalog) GetObjectMeta() memo.Meta      { return &c.Metadata }
func (p *MetadataProvider) GetObjectMeta() memo.Meta      { return &p.Metadata }
func (s *Service) GetObjectMeta() memo.Meta               { return &s.Metadata }
func (s *EndpointSlice) GetObjectMeta() memo.Meta         { return &s.Metadata }
func (m *ConfigMap) GetObjectMeta() memo.Meta             { return &m.Metadata }
func (j *Job) GetObjectMeta() memo.Meta                   { return &j.Metadata }
func (t *ResourceClaimTemplate) GetObjectMeta() memo.Meta { return &t.Metadata }

// objectVersions is the memo of each kind this process writes and then
// reads back from a store to decide whether to write again: the status of
// the Libraries, the Catalogs, and the MetadataProviders, the whole of the
// Services, the EndpointSlices, and the people ConfigMaps it stands, and
// the worker Jobs and the trickplay templates it creates and deletes. A library Job's name holds the
// time it was created, so a second create of one never meets a 409, and
// the pass must read each Job it created before the watch delivers it
// (informer.CurrentList on a whole store).
//
// The operator writes the Plays and the people only by a patch that
// states the resourceVersion of the copy it read, so a patch from an
// older copy fails with a 409 and the next pass sends it again. It
// creates and deletes the claims, the volumes, and the pods by a name
// that does not change: a create that meets one already there is a 409
// every stand reads as success, and a delete names the uid it read where
// a copy of the same name can follow. Their stores need no memo.
type objectVersions struct {
	libraries, catalogs, providers       *memo.Versions
	services, endpointSlices, configMaps *memo.Versions
	jobs, claimTemplates                 *memo.Versions
}

func newObjectVersions() objectVersions {
	return objectVersions{libraries: memo.New(), catalogs: memo.New(), providers: memo.New(),
		services: memo.New(), endpointSlices: memo.New(), configMaps: memo.New(),
		jobs: memo.New(), claimTemplates: memo.New()}
}
