package main

// These tests cover the trust store: every sibling's CA is an anchor,
// an absent sibling is tolerated, every certificate in a rotating
// file is an anchor, and the watch keeps the pool current.

import (
	"context"
	"crypto/x509"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func testAuthority(t *testing.T) *authority {
	t.Helper()
	ca, err := mintAuthority(time.Now())
	mustSucceed(t, err)
	return ca
}

func caConfigMap(name string, certificates ...[]byte) *ConfigMap {
	file := ""
	for _, each := range certificates {
		file += string(each)
	}
	return &ConfigMap{
		Metadata: ObjectMeta{Name: name, Namespace: "liken-system", ResourceVersion: "11"},
		Data:     map[string]string{apiCACertKey: file},
	}
}

// chainsTo checks the outcome the pool is for: a serving certificate
// this CA signed verifies against it for a sibling's Service name.
func chainsTo(t *testing.T, ca *authority, pool *x509.CertPool) error {
	t.Helper()
	certPEM, _, err := ca.mintLeaf(time.Now(), "sibling.liken-system.svc")
	mustSucceed(t, err)
	leaf, err := parseCertificate(certPEM)
	mustSucceed(t, err)
	_, err = leaf.Verify(x509.VerifyOptions{DNSName: "sibling.liken-system.svc", Roots: pool})
	return err
}

func TestTrustStoreCarriesEverySiblingsAnchor(t *testing.T) {
	api := newCoreAPI()
	display, audio := testAuthority(t), testAuthority(t)
	api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, display.certPEM))
	api.seed(t, "configmaps/"+audioCAConfigMapName, caConfigMap(audioCAConfigMapName, audio.certPEM))

	store := newTrustStore(testAPIClient(t, api.handler()), "liken-system",
		displayCAConfigMapName, audioCAConfigMapName)
	mustSucceed(t, store.load())

	mustSucceed(t, chainsTo(t, display, store.pool()))
	mustSucceed(t, chainsTo(t, audio, store.pool()))
}

// A cluster may run one sibling and not the other, so an absent
// ConfigMap leaves the other anchor in place and fails nothing.
func TestTrustStoreToleratesAnAbsentConfigMap(t *testing.T) {
	api := newCoreAPI()
	display, audio := testAuthority(t), testAuthority(t)
	api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, display.certPEM))

	store := newTrustStore(testAPIClient(t, api.handler()), "liken-system",
		displayCAConfigMapName, audioCAConfigMapName)
	mustSucceed(t, store.load())

	mustSucceed(t, chainsTo(t, display, store.pool()))
	mustFail(t, chainsTo(t, audio, store.pool()))
}

// A rotation appends the new CA to ca.crt, so both certificates in
// the file are anchors and a leaf under either verifies.
func TestTrustStoreTakesEveryCertificateInTheFile(t *testing.T) {
	api := newCoreAPI()
	retiring, coming := testAuthority(t), testAuthority(t)
	api.seed(t, "configmaps/"+displayCAConfigMapName,
		caConfigMap(displayCAConfigMapName, retiring.certPEM, coming.certPEM))

	store := newTrustStore(testAPIClient(t, api.handler()), "liken-system", displayCAConfigMapName)
	mustSucceed(t, store.load())

	mustSucceed(t, chainsTo(t, retiring, store.pool()))
	mustSucceed(t, chainsTo(t, coming, store.pool()))
}

func TestTrustStoreCarriesTheServersFailure(t *testing.T) {
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "the server said no")
	}))
	store := newTrustStore(client, "liken-system", displayCAConfigMapName)

	err := store.load()
	mustFail(t, err)
	if !strings.Contains(err.Error(), "the server said no") {
		t.Errorf("the error lost the server's words: %v", err)
	}
}

// The watch keeps the pool current, taking up an appended anchor and
// dropping a deleted one. It opens from the read's resourceVersion.
func TestTrustStoreWatchTakesUpANewAnchor(t *testing.T) {
	api := newCoreAPI()
	was, next := testAuthority(t), testAuthority(t)
	api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, was.certPEM))

	events := make(chan string, 1)
	defer close(events)
	watched := make(chan *url.URL, 4)
	store := newTrustStore(testAPIClient(t, watchingAPI(api, events, watched)), "liken-system",
		displayCAConfigMapName)
	store.retry = time.Millisecond
	mustSucceed(t, store.load())

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	store.watch(ctx)

	opened := <-watched
	mustMatch(t, opened.Query().Get("fieldSelector"), "metadata.name="+displayCAConfigMapName)
	mustMatch(t, opened.Query().Get("resourceVersion"), "11")

	events <- objectEvent(t, "MODIFIED", caConfigMap(displayCAConfigMapName, was.certPEM, next.certPEM))
	until(t, "the pool never took up the anchor the watch delivered", func() bool {
		return chainsTo(t, next, store.pool()) == nil
	})

	events <- objectEvent(t, "DELETED", caConfigMap(displayCAConfigMapName, was.certPEM))
	until(t, "the pool kept an anchor the watch deleted", func() bool {
		return chainsTo(t, was, store.pool()) != nil
	})
}

// A sibling this cluster has not deployed yet is one line at load, and
// its ConfigMap is trusted when the watch delivers it, with one more
// line that says so.
func TestTrustStoreReportsASiblingThatAppears(t *testing.T) {
	api := newCoreAPI()
	events := make(chan string, 1)
	defer close(events)
	watched := make(chan *url.URL, 4)
	store := newTrustStore(testAPIClient(t, watchingAPI(api, events, watched)), "liken-system",
		displayCAConfigMapName)
	var lines []string
	var said sync.Mutex
	store.report = func(line string) {
		said.Lock()
		defer said.Unlock()
		lines = append(lines, line)
	}
	mustSucceed(t, store.load())

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	store.watch(ctx)
	opened := <-watched
	mustMatch(t, opened.Query().Get("resourceVersion"), "")

	authority := testAuthority(t)
	events <- objectEvent(t, "ADDED", caConfigMap(displayCAConfigMapName, authority.certPEM))
	until(t, "the pool never took up the sibling that appeared", func() bool {
		return chainsTo(t, authority, store.pool()) == nil
	})

	said.Lock()
	defer said.Unlock()
	mustMatch(t, len(lines), 2)
	mustMatch(t, strings.Contains(lines[0], "composes no stream through that API"), true)
	mustMatch(t, strings.Contains(lines[1], "is present and media-api trusts it"), true)
}
