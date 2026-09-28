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
// keeper wrote. It answers client-go's watches too: a streaming list of
// one named object, and every later change to that object, so a test
// runs the api role's watches through client-go's own reflector.
type coreAPI struct {
	mu      sync.Mutex
	objects map[string]json.RawMessage
	hidden  map[string]bool
	writes  []string
	version int
	streams []*coreStream
	watches int
	// collections records each list and each watch, as the path and
	// the field selector it asked for.
	collections []string

	// revision is the collection's resourceVersion that a list
	// answers. It is apart from each object's own version, as on a
	// cluster, where an object that has not changed in a while carries
	// a version far older than the collection's.
	revision string

	// refusal, when it is not zero, is the status every list and every
	// watch answers.
	refusal int
}

// coreStream is one open watch: the object it names, and the event
// lines still to send.
type coreStream struct {
	key    string
	events chan string
}

func newCoreAPI() *coreAPI {
	return &coreAPI{objects: map[string]json.RawMessage{}, hidden: map[string]bool{}, revision: "900"}
}

// handler answers the secrets and configmaps paths, told apart by the
// kind segment, with a get, a list by name, a watch by name, a create,
// and an update on each.
func (a *coreAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, name := coreTarget(r.URL.Path)
		query := r.URL.Query()
		if r.Method == http.MethodGet && name == "" {
			a.mu.Lock()
			a.collections = append(a.collections, r.URL.Path+"?fieldSelector="+query.Get("fieldSelector"))
			a.mu.Unlock()
		}
		if r.Method == http.MethodGet && name == "" && query.Get("watch") == "true" {
			a.watch(w, r, kind, query.Get("fieldSelector"))
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && name == "":
			a.list(w, kind, query.Get("fieldSelector"))
		case r.Method == http.MethodGet:
			a.answer(w, kind+"/"+name)
		case r.Method == http.MethodPost:
			a.take(w, r, kind, true)
		case r.Method == http.MethodPut:
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

// coreKinds names each collection's kind, which client-go's decoder
// needs on every object it reads.
var coreKinds = map[string]string{"configmaps": "ConfigMap", "secrets": "Secret"}

// asCoreObject answers the stored object with its apiVersion and kind.
func asCoreObject(kind string, body json.RawMessage) json.RawMessage {
	object := map[string]any{}
	_ = json.Unmarshal(body, &object)
	object["apiVersion"] = "v1"
	object["kind"] = coreKinds[kind]
	stamped, _ := json.Marshal(object)
	return stamped
}

func (a *coreAPI) answer(w http.ResponseWriter, key string) {
	body, held := a.objects[key]
	if !held || a.hidden[key] {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = w.Write(body)
}

// list answers a list under the field selector metadata.name, the only
// list the program sends: the one object if the server holds it, and
// the collection's revision.
func (a *coreAPI) list(w http.ResponseWriter, kind, selector string) {
	if a.refusal != 0 {
		w.WriteHeader(a.refusal)
		return
	}
	name, named := strings.CutPrefix(selector, "metadata.name=")
	if !named {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	items := []json.RawMessage{}
	key := kind + "/" + name
	if body, held := a.objects[key]; held && !a.hidden[key] {
		items = append(items, asCoreObject(kind, body))
	}
	body, _ := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       coreKinds[kind] + "List",
		"metadata":   map[string]string{"resourceVersion": a.revision},
		"items":      items,
	})
	_, _ = w.Write(body)
}

// watch answers one watch under the field selector metadata.name. A
// streaming list first sends the object as it is, if the server holds
// it, and the bookmark that ends the initial events. Every watch then
// sends each change to the object until the client hangs up.
func (a *coreAPI) watch(w http.ResponseWriter, r *http.Request, kind, selector string) {
	name, _ := strings.CutPrefix(selector, "metadata.name=")
	key := kind + "/" + name
	a.mu.Lock()
	a.watches++
	if a.refusal != 0 {
		a.mu.Unlock()
		w.WriteHeader(a.refusal)
		return
	}
	stream := &coreStream{key: key, events: make(chan string, 16)}
	a.streams = append(a.streams, stream)
	var initial []string
	if r.URL.Query().Get("sendInitialEvents") == "true" {
		if body, held := a.objects[key]; held && !a.hidden[key] {
			initial = append(initial, watchLine("ADDED", asCoreObject(kind, body)))
		}
		initial = append(initial, initialEventsEnd("v1", coreKinds[kind], a.revision))
	}
	a.mu.Unlock()
	defer a.close(stream)

	w.Header().Set("Content-Type", "application/json")
	for _, line := range initial {
		_, _ = io.WriteString(w, line+"\n")
	}
	w.(http.Flusher).Flush()
	for {
		select {
		case line := <-stream.events:
			_, _ = io.WriteString(w, line+"\n")
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (a *coreAPI) close(stream *coreStream) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for index, open := range a.streams {
		if open == stream {
			a.streams = append(a.streams[:index], a.streams[index+1:]...)
			return
		}
	}
}

// publish sends one change to every watch of the object. The caller
// holds the lock.
func (a *coreAPI) publish(key, eventType string, body json.RawMessage) {
	kind, _, _ := strings.Cut(key, "/")
	line := watchLine(eventType, asCoreObject(kind, body))
	for _, stream := range a.streams {
		if stream.key == key {
			stream.events <- line
		}
	}
}

// requested answers each list and watch the server took, as the path
// and the field selector.
func (a *coreAPI) requested() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string{}, a.collections...)
}

// opened answers how many watches the server has taken.
func (a *coreAPI) opened() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.watches
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
	_, existed := a.objects[key]
	a.objects[key] = stored
	a.publish(key, changeType(existed), stored)
	_, _ = w.Write(stored)
}

func changeType(existed bool) string {
	if existed {
		return "MODIFIED"
	}
	return "ADDED"
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

// seed places an object, and sends the change to every watch of it.
func (a *coreAPI) seed(t *testing.T, key string, object any) {
	t.Helper()
	body, err := json.Marshal(object)
	mustSucceed(t, err)
	a.mu.Lock()
	defer a.mu.Unlock()
	_, existed := a.objects[key]
	a.objects[key] = body
	a.publish(key, changeType(existed), body)
}

// remove deletes an object, and sends the deletion to every watch of
// it.
func (a *coreAPI) remove(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	body, held := a.objects[key]
	if !held {
		return
	}
	delete(a.objects, key)
	a.publish(key, "DELETED", body)
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
