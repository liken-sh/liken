package main

// what these tests read: the one call the operator makes against a
// metadata provider each pass, the Ready condition each answer
// produces, and which provider a Library's sources resolve to.

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// a provider with a Secret the cluster holds, over a server the test
// answers for.
func seedProvider(cluster *fakeCluster, name, namespace string, facts ...string) *MetadataProvider {
	provider := &MetadataProvider{
		Metadata: ObjectMeta{Name: name, Namespace: namespace, UID: name + "-uid", Generation: 2},
		Spec: MetadataProviderSpec{
			TMDb:  &ProviderTMDb{SecretRef: SecretKeyRef{Name: name + "-key", Key: "token"}},
			Facts: facts,
		},
	}
	cluster.providers[name] = provider
	return provider
}

// the operator on a provider server of the test's own, so no check
// reaches the internet.
func providerOperator(t *testing.T, cluster *fakeCluster, handler http.Handler) *operator {
	t.Helper()
	provider := httptest.NewServer(handler)
	t.Cleanup(provider.Close)
	operator := testOperator(t, cluster)
	operator.providerBase = provider.URL
	return operator
}

// the check asks for the configuration with the key as a bearer token,
// and the answer is the Ready condition.
func TestProviderCheckReadsEachAnswer(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		secret  *Secret
		want    ConditionStatus
		reason  string
		refusal bool
	}{
		{name: "the provider answers", status: http.StatusOK,
			secret: tmdbSecret("token", "the-key"), want: ConditionTrue, reason: reasonReachable},
		{name: "the provider refuses the key", status: http.StatusUnauthorized,
			secret: tmdbSecret("token", "the-key"), want: ConditionFalse, reason: reasonRefused, refusal: true},
		{name: "the namespace holds no Secret", status: http.StatusOK,
			want: ConditionFalse, reason: reasonNoSecret},
		{name: "the Secret holds no such key", status: http.StatusOK,
			secret: tmdbSecret("other", "the-key"), want: ConditionFalse, reason: reasonNoSecret},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			provider := seedProvider(cluster, "tmdb", "house", factIdentity)
			if one.secret != nil {
				cluster.secrets["tmdb-key"] = one.secret
			}
			operator := providerOperator(t, cluster, tokenServer(t, one.status, "the-key"))

			operator.checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

			written := cluster.heldProvider("tmdb")
			ready := conditionNamed(written.Status.Conditions, conditionReady)
			if ready.Status != one.want || ready.Reason != one.reason {
				t.Errorf("Ready = %s/%s, want %s/%s", ready.Status, ready.Reason, one.want, one.reason)
			}
			if written.Status.LastRefusal.IsZero() == one.refusal {
				t.Errorf("lastRefusal = %v, with a refusal of %v", written.Status.LastRefusal, one.refusal)
			}
		})
	}
}

// the facts a provider reports are the operator's table for its block,
// narrowed by the facts its spec names, and none at all while its check has
// not reached it.
func TestProviderStatusReportsTheFactsItServesNow(t *testing.T) {
	cases := []struct {
		name   string
		facts  []string
		status int
		want   []string
	}{
		{name: "a spec that names no fact serves the whole table",
			status: http.StatusOK, want: providerFacts[providerBlockTMDb]},
		{name: "a spec narrows the table to what it names",
			facts: []string{factIdentity}, status: http.StatusOK, want: []string{factIdentity}},
		{name: "a spec that names a fact outside the table serves none",
			facts: []string{"poster"}, status: http.StatusOK},
		{name: "a provider the check cannot reach serves none",
			facts: []string{factIdentity}, status: http.StatusUnauthorized},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			provider := seedProvider(cluster, "tmdb", "house", one.facts...)
			cluster.secrets["tmdb-key"] = tmdbSecret("token", "the-key")
			operator := providerOperator(t, cluster, tokenServer(t, one.status, "the-key"))

			operator.checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

			if got := cluster.heldProvider("tmdb").Status.Facts; !slices.Equal(got, one.want) {
				t.Errorf("status.facts = %v, want %v", got, one.want)
			}
		})
	}
}

// a provider that answers neither 200 nor 401 keeps the verdict it
// carried, because the operator learned nothing.
func TestProviderCheckKeepsTheLastVerdictOnAFailure(t *testing.T) {
	cluster := newFakeCluster()
	provider := seedProvider(cluster, "tmdb", "house", factIdentity)
	provider.Status.Conditions = []Condition{{
		Type: conditionReady, Status: ConditionTrue, Reason: reasonReachable,
	}}
	cluster.secrets["tmdb-key"] = tmdbSecret("token", "the-key")
	operator := providerOperator(t, cluster,
		tokenServer(t, http.StatusInternalServerError, "the-key"))

	set := operator.checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

	if got := conditionNamed(set[libraryKey("house", "tmdb")].Status.Conditions, conditionReady); got.Status != ConditionTrue {
		t.Errorf("Ready = %s, want the verdict the provider carried", got.Status)
	}
	if cluster.countRequests(http.MethodPut, "metadataproviders") != 0 {
		t.Error("a failed check wrote a status")
	}
}

// a MetadataProvider as a pass leaves it: the block that gives it a row in
// the operator's table, the facts its spec narrows that row to, and the
// verdict of its own check. An empty reason is a provider no check has
// reported on yet.
func checkedProvider(name string, facts []string, reason string) *MetadataProvider {
	provider := &MetadataProvider{
		Metadata: ObjectMeta{Name: name, Namespace: "house"},
		Spec: MetadataProviderSpec{
			TMDb:  &ProviderTMDb{SecretRef: SecretKeyRef{Name: name + "-key"}},
			Facts: facts,
		},
	}
	if reason == "" {
		return provider
	}
	status := ConditionFalse
	if reason == reasonReachable {
		status = ConditionTrue
	}
	provider.Status.Conditions = []Condition{{Type: conditionReady, Status: status, Reason: reason}}
	return provider
}

// a provider the operator has checked and found reachable serves the
// facts it lists, and one that failed its check serves none.
func TestProviderSetServesTheFirstReadyProvider(t *testing.T) {
	set := providerSet{
		libraryKey("house", "tmdb"):   checkedProvider("tmdb", []string{factIdentity}, reasonReachable),
		libraryKey("house", "fanart"): checkedProvider("fanart", []string{factIdentity}, reasonRefused),
		libraryKey("house", "art"):    checkedProvider("art", []string{"poster"}, reasonReachable),
	}

	cases := []struct {
		name    string
		sources []string
		want    string
	}{
		{name: "no sources at all"},
		{name: "a source that does not exist", sources: []string{"tvdb"}},
		{name: "a source that is not ready", sources: []string{"fanart"}},
		{name: "a source that serves another fact", sources: []string{"art"}},
		{name: "the first ready source that serves it", sources: []string{"art", "tmdb"}, want: "tmdb"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := set.serving("house", one.sources, factIdentity)
			if one.want == "" {
				if got != nil {
					t.Errorf("serving = %s, want none", got.Metadata.Name)
				}
				return
			}
			if got == nil || got.Metadata.Name != one.want {
				t.Errorf("serving = %+v, want %s", got, one.want)
			}
		})
	}
}

// the sources a Library names are reported on the Library: a provider
// that does not exist, and a list where none serves identity.
func TestSourcesVerdictNamesWhatIsWrong(t *testing.T) {
	set := providerSet{
		libraryKey("house", "art"):       checkedProvider("art", []string{"poster"}, reasonReachable),
		libraryKey("house", "tmdb"):      checkedProvider("tmdb", []string{factIdentity}, reasonReachable),
		libraryKey("house", "fanart"):    checkedProvider("fanart", []string{factIdentity}, reasonRefused),
		libraryKey("house", "unchecked"): checkedProvider("unchecked", []string{factIdentity}, ""),
		libraryKey("house", "whole"):     checkedProvider("whole", nil, reasonReachable),
	}
	cases := []struct {
		name    string
		sources []string
		reason  string
	}{
		{name: "a library that names no source"},
		{name: "a source that does not exist", sources: []string{"tvdb"}, reason: reasonProviderNotFound},
		{name: "sources that serve no fact this library needs",
			sources: []string{"art"}, reason: reasonFactNotServed},
		{name: "the one source that serves identity is not Ready",
			sources: []string{"art", "fanart"}, reason: reasonProviderNotReady},
		{name: "the one source that serves identity has no check yet",
			sources: []string{"unchecked"}, reason: reasonProviderNotReady},
		{name: "a source that serves identity", sources: []string{"art", "tmdb"}, reason: reasonSourcesReady},
		{name: "a source that names no fact serves every fact in the table",
			sources: []string{"whole"}, reason: reasonSourcesReady},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library := studioMovies()
			library.Spec.Sources = one.sources

			if got := checkSources(library, set).reason; got != one.reason {
				t.Errorf("reason = %q, want %q", got, one.reason)
			}
		})
	}
}

// The Sources message names the provider that failed its own check and the
// reason that check wrote, so a person repairs the Secret named on the
// Library and reads no second object.
func TestASourceThatIsNotReadyNamesTheProviderAndItsReason(t *testing.T) {
	set := providerSet{
		libraryKey("house", "tmdb"): checkedProvider("tmdb", []string{factIdentity}, reasonNoSecret),
	}
	library := studioMovies()
	library.Spec.Sources = []string{"tmdb"}

	verdict := checkSources(library, set)

	if verdict.reason != reasonProviderNotReady {
		t.Errorf("reason = %q, want %q", verdict.reason, reasonProviderNotReady)
	}
	if !strings.Contains(verdict.message, "tmdb") || !strings.Contains(verdict.message, reasonNoSecret) {
		t.Errorf("message = %q, want the provider and the reason its check wrote", verdict.message)
	}
}

// a Secret as the API server serves one, base64 behind the wire type.
func tmdbSecret(key, value string) *Secret {
	return &Secret{
		Metadata: ObjectMeta{Name: "tmdb-key", Namespace: "house"},
		Data:     map[string][]byte{key: []byte(value)},
	}
}

// a provider that answers the configuration call with one status, and
// fails the test on any other path or a missing bearer token.
func tokenServer(t *testing.T, status int, token string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != tmdbConfigurationPath {
			t.Errorf("the check asked for %s, want %s", r.URL.Path, tmdbConfigurationPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("authorization = %q, want the key as a bearer token", got)
		}
		w.WriteHeader(status)
	})
}

// one condition out of a list, by type, so a test reads the verdict
// without a loop of its own.
func conditionNamed(conditions []Condition, conditionType string) Condition {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition
		}
	}
	return Condition{}
}

// the key name is the provider's own, or the default where it names
// none, and a provider whose Secret read fails is reported.
func TestProviderCheckReadsTheKeyItNames(t *testing.T) {
	cases := []struct {
		name   string
		key    string
		secret string
		want   ConditionStatus
	}{
		{name: "the default key", secret: defaultProviderSecretKey, want: ConditionTrue},
		{name: "a key of its own", key: "api-key", secret: "api-key", want: ConditionTrue},
		{name: "a key the Secret does not hold", key: "api-key", secret: "token", want: ConditionFalse},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			provider := seedProvider(cluster, "tmdb", "house", factIdentity)
			provider.Spec.TMDb.SecretRef.Key = one.key
			cluster.secrets["tmdb-key"] = tmdbSecret(one.secret, "the-key")
			operator := providerOperator(t, cluster, tokenServer(t, http.StatusOK, "the-key"))

			operator.checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

			got := conditionNamed(cluster.heldProvider("tmdb").Status.Conditions, conditionReady)
			if got.Status != one.want {
				t.Errorf("Ready = %s, want %s", got.Status, one.want)
			}
		})
	}
}

// a provider with no block names no account, and a Secret the API
// server will not serve leaves the verdict alone.
func TestProviderCheckWithNothingToRead(t *testing.T) {
	cluster := newFakeCluster()
	blockless := seedProvider(cluster, "empty", "house", factIdentity)
	blockless.Spec.TMDb = nil
	broken := seedProvider(cluster, "tmdb", "house", factIdentity)
	cluster.broken[secretPath("house", "tmdb-key")] = http.StatusInternalServerError
	operator := providerOperator(t, cluster, tokenServer(t, http.StatusOK, "the-key"))

	operator.checkProviders(t.Context(),
		[]MetadataProvider{*blockless, *broken}, testNow)

	if got := conditionNamed(cluster.heldProvider("empty").Status.Conditions, conditionReady); got.Reason != reasonNoSecret {
		t.Errorf("the provider with no block reads %q, want %s", got.Reason, reasonNoSecret)
	}
	if got := cluster.heldProvider("tmdb").Status.Conditions; len(got) != 0 {
		t.Errorf("conditions = %+v, want none written for a Secret that could not be read", got)
	}
}

// a status write the API server refuses is reported, and the provider
// keeps the verdict the server holds.
func TestProviderCheckReportsARefusedWrite(t *testing.T) {
	cluster := newFakeCluster()
	provider := seedProvider(cluster, "tmdb", "house", factIdentity)
	cluster.secrets["tmdb-key"] = tmdbSecret("token", "the-key")
	cluster.broken[metadataProviderPath("house", "tmdb")+"/status"] = http.StatusInternalServerError
	operator := providerOperator(t, cluster, tokenServer(t, http.StatusOK, "the-key"))

	set := operator.checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

	if !set[libraryKey("house", "tmdb")].ready() {
		t.Error("the check left the provider not ready in the set it answered")
	}
}

// A check that gets no HTTP answer at all writes Unreachable, so no verdict
// from an earlier pass stands as the reason.
func TestProviderCheckOnAProviderThatDoesNotAnswer(t *testing.T) {
	cluster := newFakeCluster()
	provider := seedProvider(cluster, "tmdb", "house", factIdentity)
	provider.Status.Conditions = []Condition{{
		Type: conditionReady, Status: ConditionFalse, Reason: reasonNoSecret,
	}}
	cluster.secrets["tmdb-key"] = tmdbSecret("token", "the-key")
	operator := testOperator(t, cluster)
	operator.providerBase = "http://127.0.0.1:1"

	operator.checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

	got := conditionNamed(cluster.heldProvider("tmdb").Status.Conditions, conditionReady)
	if got.Status != ConditionFalse || got.Reason != reasonUnreachable {
		t.Errorf("Ready = %s/%s, want %s/%s", got.Status, got.Reason, ConditionFalse, reasonUnreachable)
	}
	if got.Message == "" {
		t.Error("the condition carries no message, want the error the check read")
	}
}

// a provider is ready only on its own Ready condition, whatever else
// it carries.
func TestProviderReadsItsOwnReadyCondition(t *testing.T) {
	cases := []struct {
		name       string
		conditions []Condition
		want       bool
	}{
		{name: "no condition at all"},
		{name: "another condition alone",
			conditions: []Condition{{Type: conditionBound, Status: ConditionTrue}}},
		{name: "the Ready condition", want: true,
			conditions: []Condition{{Type: conditionBound, Status: ConditionFalse},
				{Type: conditionReady, Status: ConditionTrue}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			provider := &MetadataProvider{Status: MetadataProviderStatus{Conditions: one.conditions}}

			if got := provider.ready(); got != one.want {
				t.Errorf("ready = %v, want %v", got, one.want)
			}
		})
	}
}

// The recorder keeps the credential of the request the check made, in both of
// the forms a key can travel in.
type credentialRecorder struct {
	mutex  sync.Mutex
	header string
	query  string
}

func (c *credentialRecorder) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.header = r.Header.Get("Authorization")
	c.query = r.URL.Query().Get("api_key")
}

func (c *credentialRecorder) read() (string, string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.header, c.query
}

// The check sends the key in the form its shape names, the same decision the
// identity client makes.
func TestTheCheckSendsTheKeyInTheFormItsShapeNames(t *testing.T) {
	cases := []struct {
		name       string
		key        string
		wantHeader string
		wantQuery  string
	}{
		{name: "a v3 api key travels as a query parameter",
			key: tmdbV3APIKey, wantQuery: tmdbV3APIKey},
		{name: "a v4 read access token travels as a bearer token",
			key: tmdbV4AccessToken, wantHeader: "Bearer " + tmdbV4AccessToken},
		{name: "thirty-two characters that are not hex travel as a bearer token",
			key: tmdbKeyOfNoKnownForm, wantHeader: "Bearer " + tmdbKeyOfNoKnownForm},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cluster := newFakeCluster()
			provider := seedProvider(cluster, "tmdb", "house", factIdentity)
			cluster.secrets["tmdb-key"] = tmdbSecret("token", test.key)
			recorder := &credentialRecorder{}
			operator := providerOperator(t, cluster, recorder)

			operator.checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

			header, query := recorder.read()
			if header != test.wantHeader || query != test.wantQuery {
				t.Errorf("the key arrived as %q and %q, want %q and %q",
					header, query, test.wantHeader, test.wantQuery)
			}
		})
	}
}
