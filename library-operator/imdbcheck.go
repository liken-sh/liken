package main

// imdbcheck.go is the check of an imdb provider. IMDb has no API to call, so
// the check sends one HEAD request for each dataset file the provider's served
// facts read. The answer says whether IMDb serves the files, and its headers
// say when IMDb last replaced each one. The status reports those headers, so
// a person reads when IMDb stopped publishing a file.

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"
)

// What the check of an imdb provider read: one entry for each file, and the
// oldest of their times, which the UPDATED column prints, because no printer
// column can take the least of a list.
type IMDbStatus struct {
	Datasets []IMDbDataset `json:"datasets,omitempty"`
	Updated  time.Time     `json:"updated,omitzero"`
}

// One file as IMDb's answer to the HEAD request described it: when IMDb last
// replaced it, its ETag, and its size in bytes.
type IMDbDataset struct {
	Name         string    `json:"name"`
	LastModified time.Time `json:"lastModified,omitzero"`
	ETag         string    `json:"etag,omitempty"`
	Size         int64     `json:"size,omitempty"`
}

// The dataset of one name, or false where the status holds none.
func (s *IMDbStatus) dataset(name string) (IMDbDataset, bool) {
	if s == nil {
		return IMDbDataset{}, false
	}
	for _, dataset := range s.Datasets {
		if dataset.Name == name {
			return dataset, true
		}
	}
	return IMDbDataset{}, false
}

// The two conditions an imdb provider carries beside Ready. Stale says IMDb
// has not replaced a file for more than three days, which it otherwise does
// every day. Cached says whether the files have a claim to be kept on.
// Neither changes Ready: a file from last week still gives correct ratings,
// and a run with no cache reads each file from IMDb.
const (
	conditionStale  = "Stale"
	conditionCached = "Cached"

	reasonNotReplaced = "NotReplaced"
	reasonReplaced    = "Replaced"

	reasonPerNodeClass   = "PerNodeClass"
	reasonNoPerNodeClass = "NoPerNodeClass"
	reasonClaimFailed    = "ClaimFailed"
)

// How old a file may be before the check writes Stale.
const datasetStaleAge = 3 * 24 * time.Hour

// The check of a datasets block: one HEAD request for each file, in the order
// the facts read them. Every file must answer 200. The first file that does
// not is the verdict, and its message names the file. The cache is stood on
// the same call, because the claim follows the provider and not a run.
func (o *operator) checkDatasets(ctx context.Context, provider *MetadataProvider) providerVerdict {
	verdict := o.headDatasets(ctx, provider)
	cached := o.standDatasetsCache(ctx, provider)
	verdict.cached = &cached
	return verdict
}

func (o *operator) headDatasets(ctx context.Context, provider *MetadataProvider) providerVerdict {
	var read []IMDbDataset
	for _, name := range datasetsFor(provider.servedFacts()) {
		dataset, status, err := o.headDataset(ctx, provider, name)
		if err != nil {
			return providerVerdict{reason: reasonUnreachable, message: err.Error()}
		}
		if status != http.StatusOK {
			return providerVerdict{reason: reasonUnavailable,
				message: fmt.Sprintf("IMDb answered %d for %s", status, name)}
		}
		read = append(read, dataset)
	}
	return providerVerdict{reason: reasonReachable,
		message: "IMDb answered for every dataset file", datasets: read}
}

// One HEAD request. It transfers no file, so the check costs a few hundred
// bytes whatever the file's size.
func (o *operator) headDataset(ctx context.Context, provider *MetadataProvider,
	name string) (IMDbDataset, int, error) {
	asking, done := context.WithTimeout(ctx, providerCheckTimeout)
	defer done()
	request, err := http.NewRequestWithContext(asking, http.MethodHead,
		datasetURL(o.providerBase(provider), name), nil)
	if err != nil {
		return IMDbDataset{}, 0, err
	}
	response, err := o.providerClient.Do(request)
	if err != nil {
		return IMDbDataset{}, 0, err
	}
	drain(response.Body)
	dataset := IMDbDataset{Name: name, ETag: response.Header.Get("ETag")}
	if modified, err := http.ParseTime(response.Header.Get("Last-Modified")); err == nil {
		dataset.LastModified = modified.UTC()
	}
	if size, err := strconv.ParseInt(response.Header.Get("Content-Length"), 10, 64); err == nil {
		dataset.Size = size
	}
	return dataset, response.StatusCode, nil
}

// The imdb part of a provider's status. A check that read the files writes
// their entries and the Stale verdict on them. A check that failed leaves the
// entries and the Stale verdict of the last check that read them, because the
// last version IMDb published is still a fact. The Cached verdict is the
// check's own, whatever IMDb answered.
func deriveDatasetStatus(status *MetadataProviderStatus, provider *MetadataProvider,
	verdict providerVerdict, now time.Time) {
	if !blockOf(provider.block()).datasets {
		return
	}
	status.IMDb = provider.Status.IMDb
	if verdict.reason == reasonReachable {
		status.IMDb = &IMDbStatus{Datasets: verdict.datasets, Updated: oldestDataset(verdict.datasets)}
		status.Conditions = SetCondition(status.Conditions, staleCondition(verdict.datasets,
			provider.Metadata.Generation, now), now)
	}
	if verdict.cached != nil {
		cached := *verdict.cached
		cached.ObservedGeneration = provider.Metadata.Generation
		status.Conditions = SetCondition(status.Conditions, cached, now)
	}
}

// The least lastModified of the list, or the zero time for a list with none.
func oldestDataset(datasets []IMDbDataset) time.Time {
	oldest := time.Time{}
	for _, dataset := range datasets {
		if oldest.IsZero() || dataset.LastModified.Before(oldest) {
			oldest = dataset.LastModified
		}
	}
	return oldest
}

// Stale is True where any file is older than three days, and its message
// names the first such file and its date.
func staleCondition(datasets []IMDbDataset, generation int64, now time.Time) Condition {
	for _, dataset := range datasets {
		if now.Sub(dataset.LastModified) > datasetStaleAge {
			return Condition{Type: conditionStale, Status: ConditionTrue, ObservedGeneration: generation,
				Reason: reasonNotReplaced,
				Message: fmt.Sprintf("IMDb last replaced %s on %s", dataset.Name,
					dataset.LastModified.Format(time.DateOnly))}
		}
	}
	return Condition{Type: conditionStale, Status: ConditionFalse, ObservedGeneration: generation,
		Reason: reasonReplaced, Message: "IMDb replaced every file in the last three days"}
}

// Whether the provider's Stale condition is True.
func (p *MetadataProvider) stale() bool {
	for _, condition := range p.Status.Conditions {
		if condition.Type == conditionStale {
			return condition.Status == ConditionTrue
		}
	}
	return false
}

// Whether the provider's files have a claim, by its Cached condition.
func (p *MetadataProvider) cached() bool {
	for _, condition := range p.Status.Conditions {
		if condition.Type == conditionCached {
			return condition.Status == ConditionTrue
		}
	}
	return false
}

// The imdb provider that answers the rating of a Library, or nil where the
// first Ready source that serves the fact is another block. The rating's
// episodes and its 30-day refresh follow that answerer alone.
func imdbRatingAnswerer(library *Library, providers providerSet) *MetadataProvider {
	provider := providers.serving(library.Metadata.Namespace, library.Spec.Sources, factRatingIMDb)
	if provider == nil || provider.block() != providerBlockIMDb {
		return nil
	}
	return provider
}

// The first imdb provider the Library's Ready sources name, whichever facts
// it answers first, or nil.
func imdbSource(library *Library, providers providerSet) *MetadataProvider {
	for _, name := range library.Spec.Sources {
		provider, held := providers[libraryKey(library.Metadata.Namespace, name)]
		if held && provider.ready() && provider.block() == providerBlockIMDb {
			return provider
		}
	}
	return nil
}

// The names of the per-node classes the cluster serves, sorted, so every
// pass takes the same one first.
func perNodeClassNames(classes []StorageClass) []string {
	var names []string
	for _, class := range classes {
		if class.Provisioner == perNodeProvisioner {
			names = append(names, class.Metadata.Name)
		}
	}
	slices.Sort(names)
	return names
}
