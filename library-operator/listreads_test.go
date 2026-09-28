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
)

// The pod list the pass would send for the catalog member pods, which a
// test breaks by its whole request line.
const catalogMemberQuery = "labelSelector=" + memberLabelKey + "%3D" + memberLabelValue

// listReads answers every collection with one list from the API server.
type listReads struct {
	client *Client
}

func readList[L any](c *Client, path string) (*L, error) {
	list := new(L)
	if err := c.RequestJSON(context.Background(), http.MethodGet, path, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

func (r listReads) readLibraries() (*LibraryList, error) {
	return readList[LibraryList](r.client, librariesPath)
}

func (r listReads) readCatalogs() (*CatalogList, error) {
	return readList[CatalogList](r.client, catalogsPath)
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

func (r listReads) readMetadataProviders() (*MetadataProviderList, error) {
	return readList[MetadataProviderList](r.client, metadataProvidersPath)
}

func (r listReads) readPlays() (*PlayList, error) {
	return readList[PlayList](r.client, playsAllPath)
}

func (r listReads) readPeople() (*PersonList, error) {
	return readList[PersonList](r.client, peoplePath)
}
