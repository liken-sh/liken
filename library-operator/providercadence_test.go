package main

// What these tests read: how often the operator calls a provider's check,
// and how often it reads the provider's Secret. Every pass reads the
// providers, and a pass runs at least every ten seconds, so the call goes
// out only on the first pass, on an edit of the provider, and when the
// interval its last verdict earned has passed. The Secret is read only for
// a call that goes out.

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// One pass of the check, at the time the test names, with the provider as
// the cluster holds it after the step's edit.
type cadenceStep struct {
	after      time.Duration
	generation int64
}

func TestTheCheckCallsAProviderOnlyWhenItsVerdictCanChange(t *testing.T) {
	quick := 10 * time.Second
	cases := []struct {
		name   string
		status int
		steps  []cadenceStep
		want   int32
	}{
		{
			name: "passes inside the window make one call", status: http.StatusOK,
			steps: []cadenceStep{{}, {after: quick}, {after: 2 * quick}, {after: 30 * time.Minute}},
			want:  1,
		},
		{
			name: "an edit of the provider makes one more", status: http.StatusOK,
			steps: []cadenceStep{{}, {after: quick, generation: 2}, {after: 2 * quick, generation: 2}},
			want:  2,
		},
		{
			name: "a Ready provider is called again after its interval", status: http.StatusOK,
			steps: []cadenceStep{{}, {after: providerReadyInterval - time.Second}, {after: providerReadyInterval}},
			want:  2,
		},
		{
			name:   "a provider that is down is called again after the shorter interval",
			status: http.StatusServiceUnavailable,
			steps: []cadenceStep{{}, {after: providerDownInterval - time.Second}, {after: providerDownInterval},
				{after: providerDownInterval + quick}},
			want: 2,
		},
		{
			name:   "a refused key is called again after the shorter interval",
			status: http.StatusUnauthorized,
			steps: []cadenceStep{{}, {after: providerDownInterval - time.Second}, {after: providerDownInterval},
				{after: providerDownInterval + quick}},
			want: 2,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cluster := newFakeCluster()
			var calls atomic.Int32
			operator := providerOperator(t, cluster, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(test.status)
			}))
			provider := providerOfBlock("tmdb", providerBlockTMDb)
			cluster.providers["tmdb"] = provider
			cluster.secrets["tmdb-key"] = &Secret{
				Metadata: ObjectMeta{Name: "tmdb-key", Namespace: "house"},
				Data:     map[string][]byte{defaultProviderSecretKey: []byte("the-key")},
			}

			for _, step := range test.steps {
				provider.Metadata.Generation = max(step.generation, 1)
				operator.checkProviders(t.Context(), []MetadataProvider{*provider}, testNow.Add(step.after))
			}

			if got := calls.Load(); got != test.want {
				t.Errorf("the provider was called %d times, want %d", got, test.want)
			}
		})
	}
}

// A provider that takes no Secret is called on the same cadence, and an
// operator that starts again calls every provider once, because it holds no
// verdict of its own yet.
func TestAnOperatorThatStartsAgainCallsEveryProviderOnce(t *testing.T) {
	cluster := newFakeCluster()
	var calls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	provider := providerOfBlock("tvmaze", providerBlockTVmaze)
	cluster.providers["tvmaze"] = provider

	first := providerOperator(t, cluster, handler)
	first.checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)
	first.checkProviders(t.Context(), []MetadataProvider{*provider}, testNow.Add(time.Minute))
	second := providerOperator(t, cluster, handler)
	second.checkProviders(t.Context(), []MetadataProvider{*provider}, testNow.Add(2*time.Minute))

	if got := calls.Load(); got != 2 {
		t.Errorf("the provider was called %d times, want one for each operator", got)
	}
}

// A steady pass sends the API server no read of the Secret. Thirty passes
// ten seconds apart fall inside the hour a Reachable verdict earns, so only
// the first pass calls the provider, and only that call reads the key.
func TestSteadyPassesReadTheSecretOnlyForTheCall(t *testing.T) {
	cluster := newFakeCluster()
	provider := seedProvider(cluster, "tmdb", "house", factIdentity)
	cluster.secrets["tmdb-key"] = tmdbSecret("token", "the-key")
	operator := providerOperator(t, cluster, tokenServer(t, http.StatusOK, "the-key"))

	for pass := range 30 {
		operator.checkProviders(t.Context(), []MetadataProvider{*provider},
			testNow.Add(time.Duration(pass)*10*time.Second))
	}

	if got := cluster.countRequests(http.MethodGet, "secrets/"); got != 1 {
		t.Errorf("thirty passes read the Secret %d times, want once", got)
	}
}

// A key or a Secret that a person repairs is not an edit of the provider,
// so the pass sees no change and waits for the next call that is due. A
// refused key, a missing Secret, and a missing key each take the shorter
// interval, so the repair shows as Reachable within five minutes.
func TestARepairedKeyIsReachableAtTheShortInterval(t *testing.T) {
	quick := 10 * time.Second
	cases := []struct {
		name   string
		before *Secret
		reason string
	}{
		{name: "a refused key that is fixed", before: tmdbSecret("token", "a-wrong-key"), reason: reasonRefused},
		{name: "a Secret that is created later", reason: reasonNoSecret},
		{name: "a key that is added to the Secret later", before: tmdbSecret("other", "the-key"),
			reason: reasonNoSecret},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cluster := newFakeCluster()
			seedProvider(cluster, "tmdb", "house", factIdentity)
			if test.before != nil {
				cluster.secrets["tmdb-key"] = test.before
			}
			operator := providerOperator(t, cluster, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer the-key" {
					w.WriteHeader(http.StatusUnauthorized)
				}
			}))
			pass := func(after time.Duration) string {
				held := *cluster.heldProvider("tmdb")
				operator.checkProviders(t.Context(), []MetadataProvider{held}, testNow.Add(after))
				return conditionNamed(cluster.heldProvider("tmdb").Status.Conditions, conditionReady).Reason
			}

			first := pass(0)
			cluster.mutex.Lock()
			cluster.secrets["tmdb-key"] = tmdbSecret("token", "the-key")
			cluster.mutex.Unlock()
			waiting := pass(quick)
			due := pass(providerDownInterval)

			if first != test.reason || waiting != test.reason || due != reasonReachable {
				t.Errorf("the reasons were %s, %s, %s; want %s, %s, %s",
					first, waiting, due, test.reason, test.reason, reasonReachable)
			}
			if got := cluster.countRequests(http.MethodGet, "secrets/"); got != 2 {
				t.Errorf("the passes read the Secret %d times, want once for each call", got)
			}
		})
	}
}

// A NoSecret verdict costs the provider nothing, so a status write that
// fails leaves no note of it, and the next pass writes the verdict again.
// Without that, every Job of a Library that names the provider waits five
// minutes for a verdict that the next pass could write.
func TestANoSecretVerdictTheWriteLostIsWrittenOnTheNextPass(t *testing.T) {
	cluster := newFakeCluster()
	seedProvider(cluster, "tmdb", "house", factIdentity)
	operator := providerOperator(t, cluster, tokenServer(t, http.StatusOK, "the-key"))
	status := "PUT " + metadataProviderPath("house", "tmdb") + "/status"

	cluster.mutex.Lock()
	cluster.broken[status] = http.StatusInternalServerError
	cluster.mutex.Unlock()
	operator.checkProviders(t.Context(), []MetadataProvider{*cluster.heldProvider("tmdb")}, testNow)
	cluster.mutex.Lock()
	delete(cluster.broken, status)
	cluster.mutex.Unlock()
	operator.checkProviders(t.Context(), []MetadataProvider{*cluster.heldProvider("tmdb")}, testNow.Add(10*time.Second))

	got := conditionNamed(cluster.heldProvider("tmdb").Status.Conditions, conditionReady).Reason
	if got != reasonNoSecret {
		t.Errorf("the reason after the second pass is %q, want %s", got, reasonNoSecret)
	}
}
