package main

// what these tests read: the check of an imdb provider sends one HEAD request
// for each file its facts read, reports what IMDb answered in status.imdb,
// writes Stale for a file IMDb stopped replacing, and stands the cache claim
// only on a per-node class.

import (
	"net/http"
	"slices"
	"testing"
	"time"
)

// an imdb provider, which takes no Secret.
func seedIMDbProvider(cluster *fakeCluster, name string, facts ...string) *MetadataProvider {
	provider := &MetadataProvider{
		Metadata: ObjectMeta{Name: name, Namespace: "house", UID: name + "-uid", Generation: 1},
		Spec:     MetadataProviderSpec{IMDb: &ProviderIMDb{}, Facts: facts},
	}
	cluster.providers[name] = provider
	return provider
}

// the operator with every provider base on the dataset server.
func datasetOperator(t *testing.T, cluster *fakeCluster, server *datasetServer) *operator {
	t.Helper()
	return operatorOnProviderBase(testOperator(t, cluster), server.URL)
}

func TestTheIMDbCheckHeadsEachFileAndReportsItsHeaders(t *testing.T) {
	cluster := newFakeCluster()
	provider := seedIMDbProvider(cluster, "imdb")
	modified := testNow.Add(-2 * time.Hour)
	server := newDatasetServer(t, modified)

	datasetOperator(t, cluster, server).checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

	written := cluster.heldProvider("imdb")
	if ready := conditionNamed(written.Status.Conditions, conditionReady); ready.Reason != reasonReachable {
		t.Fatalf("Ready = %s/%s, want Reachable", ready.Status, ready.Reason)
	}
	want := []string{"HEAD title.episode 200", "HEAD title.ratings 200",
		"HEAD title.principals 200", "HEAD name.basics 200"}
	if got := server.log(); !slices.Equal(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	ratings, held := written.Status.IMDb.dataset(datasetTitleRatings)
	if !held || !ratings.LastModified.Equal(modified) || ratings.ETag != server.etag(datasetTitleRatings) ||
		ratings.Size != fixtureSize(t, datasetTitleRatings) {
		t.Errorf("title.ratings = %+v, want the headers the server sent", ratings)
	}
	if !written.Status.IMDb.Updated.Equal(modified) {
		t.Errorf("updated = %v, want %v", written.Status.IMDb.Updated, modified)
	}
	if got := written.Status.Facts; !slices.Equal(got, []string{factRatingIMDb, factCredits}) {
		t.Errorf("facts = %v, want the rating and the credits", got)
	}
}

// A file IMDb does not serve is Unavailable, and the check keeps the entries
// the last check read, because that version is still what IMDb published.
func TestAFailedIMDbCheckNamesTheFileAndKeepsTheEntries(t *testing.T) {
	cluster := newFakeCluster()
	provider := seedIMDbProvider(cluster, "imdb")
	earlier := IMDbDataset{Name: datasetTitleRatings, ETag: `"old"`, LastModified: testNow.Add(-time.Hour)}
	provider.Status.IMDb = &IMDbStatus{Datasets: []IMDbDataset{earlier}}
	server := newDatasetServer(t, testNow)
	server.statuses[datasetTitleRatings] = http.StatusForbidden

	datasetOperator(t, cluster, server).checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

	written := cluster.heldProvider("imdb")
	ready := conditionNamed(written.Status.Conditions, conditionReady)
	if ready.Reason != reasonUnavailable || ready.Message != "IMDb answered 403 for title.ratings" {
		t.Errorf("Ready = %s: %q, want Unavailable naming the file", ready.Reason, ready.Message)
	}
	if got, _ := written.Status.IMDb.dataset(datasetTitleRatings); got.ETag != earlier.ETag {
		t.Errorf("title.ratings = %+v, want the entry the last check read", got)
	}
}

// Stale reads the age of each file, and it leaves Ready as it is.
func TestAnOldFileIsStaleAndTheProviderStaysReady(t *testing.T) {
	cases := []struct {
		name   string
		age    time.Duration
		status ConditionStatus
	}{
		{name: "replaced yesterday", age: 24 * time.Hour, status: ConditionFalse},
		{name: "replaced four days ago", age: 4 * 24 * time.Hour, status: ConditionTrue},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			provider := seedIMDbProvider(cluster, "imdb")
			server := newDatasetServer(t, testNow.Add(-one.age))

			datasetOperator(t, cluster, server).checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

			written := cluster.heldProvider("imdb")
			if stale := conditionNamed(written.Status.Conditions, conditionStale); stale.Status != one.status {
				t.Errorf("Stale = %s: %q, want %s", stale.Status, stale.Message, one.status)
			}
			if !written.ready() {
				t.Error("the provider is not Ready, want Ready whatever the files' age")
			}
		})
	}
}

// The cache claim stands only on a per-node class, and with none the check
// says so in Cached and makes no claim.
func TestTheCacheClaimStandsOnlyOnAPerNodeClass(t *testing.T) {
	cases := []struct {
		name        string
		provisioner string
		cached      ConditionStatus
		reason      string
	}{
		{name: "a per-node class", provisioner: perNodeProvisioner,
			cached: ConditionTrue, reason: reasonPerNodeClass},
		{name: "no per-node class", provisioner: "rancher.io/local-path",
			cached: ConditionFalse, reason: reasonNoPerNodeClass},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			seedStorageClass(cluster, "shared", one.provisioner)
			provider := seedIMDbProvider(cluster, "imdb")
			server := newDatasetServer(t, testNow)

			datasetOperator(t, cluster, server).checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

			cached := conditionNamed(cluster.heldProvider("imdb").Status.Conditions, conditionCached)
			if cached.Status != one.cached || cached.Reason != one.reason {
				t.Errorf("Cached = %s/%s, want %s/%s", cached.Status, cached.Reason, one.cached, one.reason)
			}
			claim := cluster.heldClaim("imdb-datasets")
			if (claim != nil) != (one.cached == ConditionTrue) {
				t.Fatalf("claim = %+v, want one only on a per-node class", claim)
			}
		})
	}
}

// The claim is the provider's, 3Gi, and ReadWriteMany on a volume the
// operator writes, as every per-node claim is.
func TestTheCacheClaimIsOwnedByTheProvider(t *testing.T) {
	cluster := newFakeCluster()
	seedStorageClass(cluster, "per-node", perNodeProvisioner)
	provider := seedIMDbProvider(cluster, "imdb")
	server := newDatasetServer(t, testNow)

	datasetOperator(t, cluster, server).checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

	claim := cluster.heldClaim("imdb-datasets")
	if claim == nil {
		t.Fatal("no claim, want imdb-datasets")
	}
	if owner := claim.Metadata.OwnerReferences; len(owner) != 1 || owner[0].Kind != "MetadataProvider" ||
		owner[0].UID != "imdb-uid" {
		t.Errorf("owners = %+v, want the provider", owner)
	}
	if claim.Spec.Resources.Requests["storage"] != "3Gi" || claim.Spec.StorageClassName != "per-node" ||
		!slices.Equal(claim.Spec.AccessModes, []string{accessModeReadWriteMany}) {
		t.Errorf("spec = %+v, want 3Gi ReadWriteMany on per-node", claim.Spec)
	}
	if cluster.heldVolume(perNodeVolumeName("house", "imdb-datasets")) == nil {
		t.Error("no volume, want the per-node volume the claim names")
	}
}

// A dataset server that gives no answer at all is Unreachable, and its
// message is the error the check read.
func TestAnIMDbThatGivesNoAnswerIsUnreachable(t *testing.T) {
	cluster := newFakeCluster()
	provider := seedIMDbProvider(cluster, "imdb")
	server := newDatasetServer(t, testNow)
	operator := datasetOperator(t, cluster, server)
	server.Close()

	operator.checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

	if ready := conditionNamed(cluster.heldProvider("imdb").Status.Conditions, conditionReady); ready.Reason != reasonUnreachable {
		t.Errorf("Ready = %s/%s, want Unreachable", ready.Status, ready.Reason)
	}
}

// An API server that refuses the list of classes or the claim is a Cached
// verdict of ClaimFailed, and Ready stays as IMDb's answer made it.
func TestACacheTheClusterRefusesIsClaimFailed(t *testing.T) {
	cases := []struct {
		name   string
		broken string
	}{
		{name: "the list of classes", broken: storageClassesPath},
		{name: "the claim", broken: "POST " + claimsPath("house")},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			seedStorageClass(cluster, "per-node", perNodeProvisioner)
			cluster.broken[one.broken] = http.StatusInternalServerError
			provider := seedIMDbProvider(cluster, "imdb")
			server := newDatasetServer(t, testNow)

			datasetOperator(t, cluster, server).checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

			written := cluster.heldProvider("imdb")
			if cached := conditionNamed(written.Status.Conditions, conditionCached); cached.Reason != reasonClaimFailed {
				t.Errorf("Cached = %s/%s, want ClaimFailed", cached.Status, cached.Reason)
			}
			if !written.ready() {
				t.Error("the provider is not Ready, want Ready whatever the cache")
			}
		})
	}
}

// A provider that spec.facts narrows to one fact checks only that fact's
// files.
func TestANarrowedIMDbProviderChecksItsOwnFiles(t *testing.T) {
	cases := []struct {
		fact string
		want []string
	}{
		{fact: factRatingIMDb, want: []string{"HEAD title.episode 200", "HEAD title.ratings 200"}},
		{fact: factCredits, want: []string{"HEAD title.principals 200", "HEAD name.basics 200"}},
	}
	for _, one := range cases {
		t.Run(one.fact, func(t *testing.T) {
			cluster := newFakeCluster()
			provider := seedIMDbProvider(cluster, "imdb", one.fact)
			server := newDatasetServer(t, testNow)

			datasetOperator(t, cluster, server).checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

			if got := server.log(); !slices.Equal(got, one.want) {
				t.Errorf("requests = %v, want %v", got, one.want)
			}
		})
	}
}

// A provider no check has read reports no dataset, and one with no Stale
// condition is not stale.
func TestAnUncheckedIMDbProviderHoldsNothing(t *testing.T) {
	provider := &MetadataProvider{Spec: MetadataProviderSpec{IMDb: &ProviderIMDb{}}}

	if _, held := provider.Status.IMDb.dataset(datasetTitleRatings); held || provider.stale() || provider.cached() {
		t.Error("the provider reports a dataset or a condition, want none")
	}
}

// A status that holds other files reports none of the one asked for.
func TestAStatusWithoutTheFileReportsNone(t *testing.T) {
	status := &IMDbStatus{Datasets: []IMDbDataset{{Name: datasetTitleEpisode}}}

	if _, held := status.dataset(datasetTitleRatings); held {
		t.Error("held = true, want false")
	}
}

// An address the check cannot make a request of is Unreachable.
func TestAnIMDbAddressThatDoesNotParseIsUnreachable(t *testing.T) {
	cluster := newFakeCluster()
	provider := seedIMDbProvider(cluster, "imdb")
	operator := testOperator(t, cluster)
	operator.providerBases[providerBlockIMDb] = "http://%zz"

	operator.checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

	if ready := conditionNamed(cluster.heldProvider("imdb").Status.Conditions, conditionReady); ready.Reason != reasonUnreachable {
		t.Errorf("Ready = %s/%s, want Unreachable", ready.Status, ready.Reason)
	}
}

// A status write the API server refuses as a conflict waits for the next
// pass, and any other refusal is the check's error.
func TestARefusedStatusWriteIsAnErrorUnlessItConflicts(t *testing.T) {
	cases := []struct {
		name   string
		status int
		failed bool
	}{
		{name: "a conflict", status: http.StatusConflict},
		{name: "a server error", status: http.StatusInternalServerError, failed: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			provider := seedIMDbProvider(cluster, "imdb")
			cluster.broken["PUT "+metadataProviderPath("house", "imdb")+"/status"] = one.status
			server := newDatasetServer(t, testNow)

			err := datasetOperator(t, cluster, server).checkProvider(t.Context(), provider, testNow)

			if (err != nil) != one.failed {
				t.Errorf("check = %v, want failed %v", err, one.failed)
			}
		})
	}
}
