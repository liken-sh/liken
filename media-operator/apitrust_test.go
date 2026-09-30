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
	"testing/synctest"
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
// answers the channels that close on the first read of each ConfigMap.
// The caller runs in a synctest bubble. When the test ends, the watches
// stop, and the bubble waits for them to end, before the server closes.
func followTrust(t *testing.T, api *coreAPI, store *trustStore) []<-chan struct{} {
	t.Helper()
	watcher := testWatcher(t, api.handler())
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(func() {
		stop()
		synctest.Wait()
	})
	read := store.follow(ctx, watcher)
	synctest.Wait()
	return read
}

// followedTrust runs the trust store's watches until the test ends, and
// checks that every ConfigMap had its first read, the way the api role
// waits for them before it serves.
func followedTrust(t *testing.T, api *coreAPI, store *trustStore) {
	t.Helper()
	mustMatch(t, awaitSynced(t.Context(), followTrust(t, api, store)...), true)
}

func TestTrustStoreCarriesEverySiblingsAnchor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := newCoreAPI()
		display, audio := testAuthority(t), testAuthority(t)
		api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, display.certPEM))
		api.seed(t, "configmaps/"+audioCAConfigMapName, caConfigMap(audioCAConfigMapName, audio.certPEM))
		store := newTrustStore("liken-system", displayCAConfigMapName, audioCAConfigMapName)

		followedTrust(t, api, store)

		mustSucceed(t, chainsTo(t, display, store.pool()))
		mustSucceed(t, chainsTo(t, audio, store.pool()))
	})
}

// A cluster may run one sibling and not the other, so an absent
// ConfigMap leaves the other anchor in place and fails nothing.
func TestTrustStoreToleratesAnAbsentConfigMap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := newCoreAPI()
		display, audio := testAuthority(t), testAuthority(t)
		api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, display.certPEM))
		store := newTrustStore("liken-system", displayCAConfigMapName, audioCAConfigMapName)

		followedTrust(t, api, store)

		mustSucceed(t, chainsTo(t, display, store.pool()))
		mustFail(t, chainsTo(t, audio, store.pool()))
	})
}

// A rotation appends the new CA to ca.crt, so both certificates in
// the file are anchors and a leaf under either verifies.
func TestTrustStoreTakesEveryCertificateInTheFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := newCoreAPI()
		retiring, coming := testAuthority(t), testAuthority(t)
		api.seed(t, "configmaps/"+displayCAConfigMapName,
			caConfigMap(displayCAConfigMapName, retiring.certPEM, coming.certPEM))
		store := newTrustStore("liken-system", displayCAConfigMapName)

		followedTrust(t, api, store)

		mustSucceed(t, chainsTo(t, retiring, store.pool()))
		mustSucceed(t, chainsTo(t, coming, store.pool()))
	})
}

// An API server that refuses the read leaves the ConfigMap unread, and
// the pool trusts nothing from it.
func TestTrustStoreTrustsNothingItCouldNotRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := newCoreAPI()
		authority := testAuthority(t)
		api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, authority.certPEM))
		api.refusal = http.StatusInternalServerError
		store := newTrustStore("liken-system", displayCAConfigMapName)

		read := followTrust(t, api, store)

		mustMatch(t, received(read[0]), false)
		mustFail(t, chainsTo(t, authority, store.pool()))
	})
}

// The watch keeps the pool current, taking up an appended anchor and
// dropping a deleted one.
func TestTrustStoreWatchTakesUpANewAnchor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := newCoreAPI()
		was, next := testAuthority(t), testAuthority(t)
		api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, was.certPEM))
		store := newTrustStore("liken-system", displayCAConfigMapName)
		followedTrust(t, api, store)

		api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, was.certPEM, next.certPEM))
		synctest.Wait()
		mustSucceed(t, chainsTo(t, next, store.pool()))

		api.remove("configmaps/" + displayCAConfigMapName)
		synctest.Wait()
		mustFail(t, chainsTo(t, was, store.pool()))
	})
}

// A sibling this cluster has not deployed yet is one line at the first
// read, and its ConfigMap is trusted when the watch delivers it, with
// one more line that says so.
func TestTrustStoreReportsASiblingThatAppears(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := newCoreAPI()
		store := newTrustStore("liken-system", displayCAConfigMapName)
		lines := &reportedLines{}
		store.report = lines.report
		followedTrust(t, api, store)

		authority := testAuthority(t)
		api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, authority.certPEM))
		synctest.Wait()
		mustSucceed(t, chainsTo(t, authority, store.pool()))

		lines.mu.Lock()
		defer lines.mu.Unlock()
		mustMatch(t, len(lines.lines), 2)
		mustMatch(t, strings.Contains(lines.lines[0], "composes no stream through that API"), true)
		mustMatch(t, strings.Contains(lines.lines[1], "is present and media-api trusts it"), true)
	})
}
