package main

// The pass reads the collections the operator watches from the
// informers. A test of a pass changes the fake cluster by hand between
// passes, and no watch would carry that change, so the operator a test
// builds reads each collection with a list instead, the way the API
// server answers it at that moment. watch_test.go proves that the
// informers answer the pass with the same objects.

import (
	"context"
	"net/http"
	"net/url"
)

// The pod list the pass would send for the catalog member pods, which a
// test breaks by its whole request line.
const catalogMemberQuery = "labelSelector=" + memberLabelKey + "%3D" + memberLabelValue

// listReads answers every collection with one list from the API server.
type listReads struct {
	client *Client
}

func readList[L any](c *Client, path string) (*L, error) {
	return readListWith[L](context.Background(), c, path)
}

func readListWith[L any](ctx context.Context, c *Client, path string) (*L, error) {
	list := new(L)
	if err := c.RequestJSON(ctx, http.MethodGet, path, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

func (r listReads) readLibraries(ctx context.Context) (*LibraryList, error) {
	return readListWith[LibraryList](ctx, r.client, librariesPath)
}

func (r listReads) readCatalogs(ctx context.Context) (*CatalogList, error) {
	return readListWith[CatalogList](ctx, r.client, catalogsPath)
}

func (r listReads) readMemberPods() (*PodList, error) {
	return readList[PodList](r.client, podsAllPath+"?"+catalogMemberQuery)
}

func (r listReads) readPlayers() (*PlayerList, error) {
	return readList[PlayerList](r.client, playersPath)
}

func (r listReads) readMediaPreferences() (*MediaPreferencesList, error) {
	return readList[MediaPreferencesList](r.client, mediaPreferencesPath)
}

func (r listReads) readMetadataProviders(ctx context.Context) (*MetadataProviderList, error) {
	return readListWith[MetadataProviderList](ctx, r.client, metadataProvidersPath)
}

func (r listReads) readPlays() (*PlayList, error) {
	return readList[PlayList](r.client, playsAllPath)
}

func (r listReads) readPeople() (*PersonList, error) {
	return readList[PersonList](r.client, peoplePath)
}

func (r listReads) readClaims() (*PersistentVolumeClaimList, error) {
	return readList[PersistentVolumeClaimList](r.client, claimsAllPath)
}

func (r listReads) readVolumes() (*PersistentVolumeList, error) {
	return readList[PersistentVolumeList](r.client, volumesPath)
}

func (r listReads) readStoodPods() (*PodList, error) {
	return readList[PodList](r.client, podsAllPath+"?labelSelector="+url.QueryEscape(stoodPodsSelector))
}

func (r listReads) readProgressMembers() (*PodList, error) {
	return readList[PodList](r.client, podsAllPath+"?labelSelector="+url.QueryEscape(progressMemberSelector))
}

func (r listReads) readWorkerJobs(ctx context.Context) (*JobList, error) {
	return readListWith[JobList](ctx, r.client, jobsAllPath+"?labelSelector="+url.QueryEscape(workerJobsSelector))
}

func (r listReads) readNodes() (*NodeList, error) {
	return readList[NodeList](r.client, nodesPath)
}

func (r listReads) readService(ctx context.Context, namespace, name string) (*Service, error) {
	return GetService(ctx, r.client, namespace, name)
}

func (r listReads) readEndpointSlice(ctx context.Context, namespace, name string) (*EndpointSlice, error) {
	return GetEndpointSlice(ctx, r.client, namespace, name)
}

func (r listReads) readClaimTemplate(ctx context.Context, namespace, name string) (*ResourceClaimTemplate, error) {
	return GetResourceClaimTemplate(ctx, r.client, namespace, name)
}

func (r listReads) readConfigMap(ctx context.Context, namespace, name string) (*ConfigMap, error) {
	return GetConfigMap(ctx, r.client, namespace, name)
}
