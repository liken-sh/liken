package main

// What these tests read: how the check tells an OMDb key past its daily
// limit from a key OMDb refuses. OMDb answers both with a 401, and only the
// body tells them apart. The bodies are the ones OMDb sends, as users of
// its API have reported them in its public issue tracker.

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	omdbLimitBody      = `{"Response":"False","Error":"Request limit reached!"}`
	omdbInvalidKeyBody = `{"Response":"False","Error":"Invalid API key!"}`
)

// An OMDb provider whose Secret holds a key, over a server that answers
// every call with one status and one body, and counts the calls.
func omdbOperator(t *testing.T, status int, body string) (*operator, *fakeCluster, *atomic.Int32) {
	t.Helper()
	cluster := newFakeCluster()
	cluster.providers["omdb"] = providerOfBlock("omdb", providerBlockOMDb)
	cluster.providers["omdb"].Metadata.Generation = 1
	cluster.secrets["omdb-key"] = &Secret{
		Metadata: ObjectMeta{Name: "omdb-key", Namespace: "house"},
		Data:     map[string][]byte{defaultProviderSecretKey: []byte("the-key")},
	}
	calls := &atomic.Int32{}
	operator := providerOperator(t, cluster, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return operator, cluster, calls
}

func TestAnOMDbKeyPastItsLimitIsNotARefusedKey(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		reason  string
		words   string
		refusal bool
	}{
		{name: "a key past its daily limit", body: omdbLimitBody,
			reason: reasonLimitReached, words: "Request limit reached!"},
		{name: "a key OMDb does not accept", body: omdbInvalidKeyBody,
			reason: reasonRefused, words: "Invalid API key!", refusal: true},
		{name: "a 401 with no body", reason: reasonRefused, refusal: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			operator, cluster, _ := omdbOperator(t, http.StatusUnauthorized, test.body)

			operator.checkProviders(t.Context(), []MetadataProvider{*cluster.heldProvider("omdb")}, testNow)

			written := cluster.heldProvider("omdb")
			ready := conditionNamed(written.Status.Conditions, conditionReady)
			if ready.Status != ConditionFalse || ready.Reason != test.reason {
				t.Errorf("Ready = %s/%s, want False/%s", ready.Status, ready.Reason, test.reason)
			}
			if !strings.Contains(ready.Message, test.words) {
				t.Errorf("the message %q does not carry OMDb's words %q", ready.Message, test.words)
			}
			if written.Status.LastRefusal.IsZero() == test.refusal {
				t.Errorf("lastRefusal = %v, with a refusal of %v", written.Status.LastRefusal, test.refusal)
			}
		})
	}
}

// A key past its limit is asked again after the hour, not after five
// minutes, so the check does not spend the calls the limit counts. OMDb
// does not publish when its limit resets, so the hour is the interval.
func TestALimitedKeyIsAskedAgainAfterTheHour(t *testing.T) {
	operator, cluster, calls := omdbOperator(t, http.StatusUnauthorized, omdbLimitBody)

	for _, after := range []time.Duration{0, providerDownInterval, providerReadyInterval - time.Second,
		providerReadyInterval} {
		operator.checkProviders(t.Context(), []MetadataProvider{*cluster.heldProvider("omdb")},
			testNow.Add(after))
	}

	if got := calls.Load(); got != 2 {
		t.Errorf("OMDb was called %d times in the hour and at its end, want 2", got)
	}
}

// A provider that echoes the request can echo the key in its refusal, and
// the status is readable by more people than the Secret, so the message
// carries the provider's words with the key taken out.
func TestARefusalMessageNeverCarriesTheKey(t *testing.T) {
	operator, cluster, _ := omdbOperator(t, http.StatusUnauthorized,
		`{"Response":"False","Error":"Invalid API key: the-key"}`)

	operator.checkProviders(t.Context(), []MetadataProvider{*cluster.heldProvider("omdb")}, testNow)

	message := conditionNamed(cluster.heldProvider("omdb").Status.Conditions, conditionReady).Message
	if strings.Contains(message, "the-key") || !strings.Contains(message, "Invalid API key") {
		t.Errorf("the message %q must carry OMDb's words and not the key", message)
	}
}

// The key is taken out in every form a provider can echo it, and before
// the message is cut, so no part of it reaches the status.
func TestTheProviderWordsLeaveTheKeyOut(t *testing.T) {
	key := "a key/with+marks"
	cases := []struct {
		name string
		body string
	}{
		{name: "the key as sent", body: "bad key " + key},
		{name: "the key as a query carries it", body: "bad key a+key%2Fwith%2Bmarks"},
		{name: "the key across the cut", body: strings.Repeat("x", providerWordsLimit-4) + key},
		{name: "the key in a JSON string", body: `{"error":"bad key a key\/with+marks"}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := withProviderWords("refused", []byte(test.body), key)

			if strings.Contains(got, "key/") || strings.Contains(got, "%2F") || strings.Contains(got, "a k") {
				t.Errorf("the message %q carries part of the key", got)
			}
		})
	}
}

// A 401 page from a proxy or a web server is markup, not the provider's
// words, so the message is the check's own sentence alone.
func TestARefusalPageIsLeftOutOfTheMessage(t *testing.T) {
	got := withProviderWords("refused", []byte("<html><body>401</body></html>"), "the-key")

	if got != "refused" {
		t.Errorf("the message is %q, want the check's own sentence", got)
	}
}
