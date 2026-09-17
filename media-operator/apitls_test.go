package main

// These tests cover the serving pair: the first mint, the published
// CA, the lost create race, the renewal near the end of a leaf's
// life, and an owner's pair the keeper cannot re-sign. They run
// against a real HTTP server in place of the API server, so the
// keeper's requests are the bytes a cluster would receive.

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// coreAPI stands in for the core API: the Secrets and ConfigMaps the
// namespace holds, and every write it took, so a test reads what the
// keeper wrote.
type coreAPI struct {
	mu      sync.Mutex
	objects map[string]json.RawMessage
	hidden  map[string]bool
	writes  []string
	version int
}

func newCoreAPI() *coreAPI {
	return &coreAPI{objects: map[string]json.RawMessage{}, hidden: map[string]bool{}}
}

// handler answers the secrets and configmaps paths, told apart by the
// kind segment, with a get, a create, and an update on each.
func (a *coreAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		kind, name := coreTarget(r.URL.Path)
		switch r.Method {
		case http.MethodGet:
			a.answer(w, kind+"/"+name)
		case http.MethodPost:
			a.take(w, r, kind, true)
		case http.MethodPut:
			a.take(w, r, kind, false)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func coreTarget(path string) (kind, name string) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/namespaces/"), "/")
	switch len(parts) {
	case 2:
		return parts[1], ""
	case 3:
		return parts[1], parts[2]
	}
	return "", ""
}

func (a *coreAPI) answer(w http.ResponseWriter, key string) {
	body, held := a.objects[key]
	if !held || a.hidden[key] {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = w.Write(body)
}

// take handles a create or an update. A create against a hidden
// object is the lost race: it answers 409, and the hidden object
// appears for the read that follows.
func (a *coreAPI) take(w http.ResponseWriter, r *http.Request, kind string, create bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	object := map[string]any{}
	if err := json.Unmarshal(body, &object); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	metadata, _ := object["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	key := kind + "/" + name
	a.writes = append(a.writes, r.Method+" "+key)
	if create && a.hidden[key] {
		a.hidden[key] = false
		w.WriteHeader(http.StatusConflict)
		return
	}
	a.version++
	metadata["resourceVersion"] = strconv.Itoa(a.version)
	stored, err := json.Marshal(object)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	a.objects[key] = stored
	_, _ = w.Write(stored)
}

func (a *coreAPI) holds(t *testing.T, key string, out any) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	body, held := a.objects[key]
	if !held {
		t.Fatalf("the server holds no %s", key)
	}
	mustSucceed(t, json.Unmarshal(body, out))
}

func (a *coreAPI) seed(t *testing.T, key string, object any) {
	t.Helper()
	body, err := json.Marshal(object)
	mustSucceed(t, err)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.objects[key] = body
}

// hide takes an object out of every read until a create meets it,
// which is how a test stages the race the keeper loses.
func (a *coreAPI) hide(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hidden[key] = true
}

func (a *coreAPI) wrote() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string{}, a.writes...)
}

// verifies is the chain check every test makes: the leaf verifies for
// the Service name against the published CA at the given instant.
func verifies(pair *tls.Certificate, caPEM string, at time.Time) error {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(caPEM)) {
		return errors.New("ca.crt carries no certificates")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	_, err = leaf.Verify(x509.VerifyOptions{DNSName: apiServiceDNSName, Roots: roots, CurrentTime: at})
	return err
}

func publishedCA(t *testing.T, api *coreAPI) string {
	t.Helper()
	published := &ConfigMap{}
	api.holds(t, "configmaps/"+apiCAConfigMapName, published)
	return published.Data[apiCACertKey]
}

func TestCertificateKeeperMintsAndPublishesTheFirstPair(t *testing.T) {
	api := newCoreAPI()
	keeper := newCertificateKeeper(testAPIClient(t, api.handler()), "liken-system")

	pair, err := keeper.ensure()
	mustSucceed(t, err)

	secret := &Secret{}
	api.holds(t, "secrets/"+apiTLSSecretName, secret)
	mustMatch(t, secret.Type, apiTLSSecretType)
	mustMatch(t, string(derOf(t, secret.Data[apiTLSCertKey])), string(pair.Certificate[0]))
	mustMatch(t, string(secret.Data[apiCACertKey]), publishedCA(t, api))
	mustSucceed(t, verifies(pair, publishedCA(t, api), time.Now()))
}

func TestCertificateKeeperReadsThePublishedPair(t *testing.T) {
	api := newCoreAPI()
	client := testAPIClient(t, api.handler())

	first, err := newCertificateKeeper(client, "liken-system").ensure()
	mustSucceed(t, err)
	minted := api.wrote()

	second, err := newCertificateKeeper(client, "liken-system").ensure()
	mustSucceed(t, err)

	mustMatch(t, string(second.Certificate[0]), string(first.Certificate[0]))
	mustMatchAll(t, api.wrote(), minted)
}

func TestCertificateKeeperReadsTheWinnerOfACreate(t *testing.T) {
	api := newCoreAPI()
	client := testAPIClient(t, api.handler())

	winner, err := newCertificateKeeper(client, "liken-system").ensure()
	mustSucceed(t, err)
	api.hide("secrets/" + apiTLSSecretName)

	loser, err := newCertificateKeeper(client, "liken-system").ensure()
	mustSucceed(t, err)

	mustMatch(t, string(loser.Certificate[0]), string(winner.Certificate[0]))
}

func TestCertificateKeeperRemintsALeafNearItsEnd(t *testing.T) {
	cases := []struct {
		name     string
		age      time.Duration
		reminted bool
	}{
		{name: "over a third of its life left", age: 100 * 24 * time.Hour, reminted: false},
		{name: "under a third of its life left", age: 300 * 24 * time.Hour, reminted: true},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			api := newCoreAPI()
			client := testAPIClient(t, api.handler())
			minted := time.Now()

			first := newCertificateKeeper(client, "liken-system")
			first.now = func() time.Time { return minted }
			before, err := first.ensure()
			mustSucceed(t, err)

			later := newCertificateKeeper(client, "liken-system")
			later.now = func() time.Time { return minted.Add(each.age) }
			after, err := later.ensure()
			mustSucceed(t, err)

			changed := string(after.Certificate[0]) != string(before.Certificate[0])
			mustMatch(t, changed, each.reminted)
			mustMatch(t, after.Leaf.NotAfter.After(before.Leaf.NotAfter), each.reminted)
			mustSucceed(t, verifies(after, publishedCA(t, api), minted.Add(each.age)))
		})
	}
}

func TestExpirySecondsAnswersTheLeafsRemainingLife(t *testing.T) {
	api := newCoreAPI()
	keeper := newCertificateKeeper(testAPIClient(t, api.handler()), "liken-system")
	minted := time.Now()
	keeper.now = func() time.Time { return minted }
	_, err := keeper.ensure()
	mustSucceed(t, err)

	gone := 100 * 24 * time.Hour
	keeper.now = func() time.Time { return minted.Add(gone) }

	want := (apiLeafLife - gone).Seconds()
	if got := keeper.expirySeconds(); math.Abs(got-want) > 1 {
		t.Errorf("got %v seconds, want %v", got, want)
	}
}

// An owner's own pair has no CA key, so the keeper serves it as it
// stands and mints nothing, even when the leaf is near its end.
func TestCertificateKeeperKeepsAPairItCannotResign(t *testing.T) {
	minted := time.Now().Add(-300 * 24 * time.Hour)
	api := newCoreAPI()
	certPEM, keyPEM, caPEM := ownersPair(t, minted)
	api.seed(t, "secrets/"+apiTLSSecretName, &Secret{
		Metadata: ObjectMeta{Name: apiTLSSecretName, Namespace: "liken-system"},
		Data: map[string][]byte{
			apiTLSCertKey: certPEM,
			apiTLSKeyKey:  keyPEM,
			apiCACertKey:  caPEM,
		},
	})

	pair, err := newCertificateKeeper(testAPIClient(t, api.handler()), "liken-system").ensure()
	mustSucceed(t, err)

	mustMatch(t, string(pair.Certificate[0]), string(derOf(t, certPEM)))
	mustMatchAll(t, api.wrote(), []string{"POST configmaps/" + apiCAConfigMapName})
}

// ownersPair mints a pair at a chosen instant, the way an owner's
// installer would leave one: a leaf, its key, and the CA certificate
// with no CA key.
func ownersPair(t *testing.T, at time.Time) (certPEM, keyPEM, caPEM []byte) {
	t.Helper()
	ca, err := mintAuthority(at)
	mustSucceed(t, err)
	certPEM, keyPEM, err = ca.mintLeaf(at, apiServiceDNSName)
	mustSucceed(t, err)
	return certPEM, keyPEM, ca.certPEM
}

func derOf(t *testing.T, certPEM []byte) []byte {
	t.Helper()
	certificate, err := parseCertificate(certPEM)
	mustSucceed(t, err)
	return certificate.Raw
}

func TestCertificateKeeperCarriesTheServersFailure(t *testing.T) {
	cases := []string{
		"GET secrets",
		"POST secrets",
		"GET configmaps",
		"POST configmaps",
	}
	for _, failing := range cases {
		t.Run(failing, func(t *testing.T) {
			api := newCoreAPI()
			keeper := newCertificateKeeper(testAPIClient(t, failsOn(api, failing)), "liken-system")
			_, err := keeper.ensure()
			mustFail(t, err)
			if !strings.Contains(err.Error(), "the server said no") {
				t.Errorf("the error lost the server's words: %v", err)
			}
		})
	}
}

// failsOn wraps the core API so one method on one kind is refused in
// the server's own words and every other request is answered.
func failsOn(api *coreAPI, failing string) http.Handler {
	answer := api.handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, _ := coreTarget(r.URL.Path)
		if r.Method+" "+kind == failing {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "the server said no")
			return
		}
		answer.ServeHTTP(w, r)
	})
}

func TestCertificateKeeperCarriesAFailedUpdate(t *testing.T) {
	api := newCoreAPI()
	minted := time.Now()
	first := newCertificateKeeper(testAPIClient(t, api.handler()), "liken-system")
	first.now = func() time.Time { return minted }
	_, err := first.ensure()
	mustSucceed(t, err)

	later := newCertificateKeeper(testAPIClient(t, failsOn(api, "PUT secrets")), "liken-system")
	later.now = func() time.Time { return minted.Add(300 * 24 * time.Hour) }
	_, err = later.ensure()
	mustFail(t, err)
	if !strings.Contains(err.Error(), "the server said no") {
		t.Errorf("the error lost the server's words: %v", err)
	}
}

func TestCertificateKeeperRefusesAPairItCannotRead(t *testing.T) {
	certPEM, keyPEM, caPEM := ownersPair(t, time.Now().Add(-300*24*time.Hour))
	cases := []struct {
		name string
		data map[string][]byte
	}{
		{
			name: "a certificate that is not a certificate",
			data: map[string][]byte{apiTLSCertKey: []byte("nothing"), apiTLSKeyKey: keyPEM},
		},
		{
			name: "a CA key that is not a key",
			data: map[string][]byte{
				apiTLSCertKey: certPEM,
				apiTLSKeyKey:  keyPEM,
				apiCACertKey:  caPEM,
				apiCAKeyKey:   []byte("nothing"),
			},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			api := newCoreAPI()
			api.seed(t, "secrets/"+apiTLSSecretName, &Secret{
				Metadata: ObjectMeta{Name: apiTLSSecretName, Namespace: "liken-system"},
				Data:     each.data,
			})
			keeper := newCertificateKeeper(testAPIClient(t, api.handler()), "liken-system")
			_, err := keeper.ensure()
			mustFail(t, err)
		})
	}
}

func TestExpirySecondsAnswersZeroBeforeThePairIsLoaded(t *testing.T) {
	keeper := newCertificateKeeper(testAPIClient(t, newCoreAPI().handler()), "liken-system")
	mustMatch(t, keeper.expirySeconds(), float64(0))
}
