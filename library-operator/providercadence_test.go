package main

// What these tests read: how often the operator calls a provider's check.
// Every pass reads the providers, and a pass runs at least every ten
// seconds, so the call goes out only on the first pass, on an edit of the
// provider or of its Secret, and when the interval its last verdict earned
// has passed.

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
	secret     string
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
			name: "an edit of the Secret makes one more", status: http.StatusOK,
			steps: []cadenceStep{{}, {after: quick, secret: "2"}, {after: 2 * quick, secret: "2"}},
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
			name:   "a refused key waits the Ready interval, because it will not repair itself",
			status: http.StatusUnauthorized,
			steps: []cadenceStep{{}, {after: providerDownInterval}, {after: providerReadyInterval - time.Second},
				{after: providerReadyInterval}},
			want: 2,
		},
		{
			name:   "a refused key is called again at once when its Secret changes",
			status: http.StatusUnauthorized,
			steps:  []cadenceStep{{}, {after: quick, secret: "2"}},
			want:   2,
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
			secret := &Secret{
				Metadata: ObjectMeta{Name: "tmdb-key", Namespace: "house", ResourceVersion: "1"},
				Data:     map[string][]byte{defaultProviderSecretKey: []byte("the-key")},
			}
			cluster.secrets["tmdb-key"] = secret

			for _, step := range test.steps {
				provider.Metadata.Generation = max(step.generation, 1)
				secret.Metadata.ResourceVersion = "1"
				if step.secret != "" {
					secret.Metadata.ResourceVersion = step.secret
				}
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
