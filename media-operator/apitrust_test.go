package main

// These tests cover the trust store: every sibling's CA is an anchor,
// an absent sibling is tolerated, every certificate in a rotating
// file is an anchor, and the watch keeps the pool current.

import (
	"context"
	"crypto/x509"
	"net/http"
	"strings"
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

// followTrust runs the trust store's watches until the test ends, and
// waits for the first read of every ConfigMap.
func followTrust(t *testing.T, api *coreAPI, store *trustStore) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	read := store.follow(ctx, testWatcher(t, api.handler()))
	waiting, cancel := context.WithTimeout(context.Background(), watchTimeout)
	defer cancel()
	mustMatch(t, awaitSynced(waiting, read...), true)
}

func TestTrustStoreCarriesEverySiblingsAnchor(t *testing.T) {
	api := newCoreAPI()
	display, audio := testAuthority(t), testAuthority(t)
	api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, display.certPEM))
	api.seed(t, "configmaps/"+audioCAConfigMapName, caConfigMap(audioCAConfigMapName, audio.certPEM))
	store := newTrustStore("liken-system", displayCAConfigMapName, audioCAConfigMapName)

	followTrust(t, api, store)

	mustSucceed(t, chainsTo(t, display, store.pool()))
	mustSucceed(t, chainsTo(t, audio, store.pool()))
}

// A cluster may run one sibling and not the other, so an absent
// ConfigMap leaves the other anchor in place and fails nothing.
func TestTrustStoreToleratesAnAbsentConfigMap(t *testing.T) {
	api := newCoreAPI()
	display, audio := testAuthority(t), testAuthority(t)
	api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, display.certPEM))
	store := newTrustStore("liken-system", displayCAConfigMapName, audioCAConfigMapName)

	followTrust(t, api, store)

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
	store := newTrustStore("liken-system", displayCAConfigMapName)

	followTrust(t, api, store)

	mustSucceed(t, chainsTo(t, retiring, store.pool()))
	mustSucceed(t, chainsTo(t, coming, store.pool()))
}

// An API server that refuses the read leaves the ConfigMap unread, and
// the pool trusts nothing from it.
func TestTrustStoreTrustsNothingItCouldNotRead(t *testing.T) {
	api := newCoreAPI()
	authority := testAuthority(t)
	api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, authority.certPEM))
	api.refusal = http.StatusInternalServerError
	store := newTrustStore("liken-system", displayCAConfigMapName)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	read := store.follow(ctx, testWatcher(t, api.handler()))

	mustMatch(t, closedWithin(read[0], watchQuietSpell), false)
	mustFail(t, chainsTo(t, authority, store.pool()))
}

// The watch keeps the pool current, taking up an appended anchor and
// dropping a deleted one.
func TestTrustStoreWatchTakesUpANewAnchor(t *testing.T) {
	api := newCoreAPI()
	was, next := testAuthority(t), testAuthority(t)
	api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, was.certPEM))
	store := newTrustStore("liken-system", displayCAConfigMapName)
	followTrust(t, api, store)

	api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, was.certPEM, next.certPEM))
	until(t, "the pool never took up the anchor the watch delivered", func() bool {
		return chainsTo(t, next, store.pool()) == nil
	})

	api.remove("configmaps/" + displayCAConfigMapName)
	until(t, "the pool kept an anchor the watch deleted", func() bool {
		return chainsTo(t, was, store.pool()) != nil
	})
}

// A sibling this cluster has not deployed yet is one line at the first
// read, and its ConfigMap is trusted when the watch delivers it, with
// one more line that says so.
func TestTrustStoreReportsASiblingThatAppears(t *testing.T) {
	api := newCoreAPI()
	store := newTrustStore("liken-system", displayCAConfigMapName)
	lines := &reportedLines{}
	store.report = lines.report
	followTrust(t, api, store)

	authority := testAuthority(t)
	api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, authority.certPEM))
	until(t, "the pool never took up the sibling that appeared", func() bool {
		return chainsTo(t, authority, store.pool()) == nil
	})

	lines.mu.Lock()
	defer lines.mu.Unlock()
	mustMatch(t, len(lines.lines), 2)
	mustMatch(t, strings.Contains(lines.lines[0], "composes no stream through that API"), true)
	mustMatch(t, strings.Contains(lines.lines[1], "is present and media-api trusts it"), true)
}
