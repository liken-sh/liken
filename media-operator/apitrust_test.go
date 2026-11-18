package main

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

// chainsTo returns an error unless a serving certificate that ca signs
// verifies against pool.
func chainsTo(t *testing.T, ca *authority, pool *x509.CertPool) error {
	t.Helper()
	certPEM, _, err := ca.mintLeaf(time.Now(), "sibling.liken-system.svc")
	mustSucceed(t, err)
	leaf, err := parseCertificate(certPEM)
	mustSucceed(t, err)
	_, err = leaf.Verify(x509.VerifyOptions{DNSName: "sibling.liken-system.svc", Roots: pool})
	return err
}

// followTrust runs the trust store's watches until the test ends and
// returns the channels of follow. The caller runs in a synctest bubble.
// Cleanup waits for the watches to stop before the fake server closes.
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

// followedTrust is followTrust, and it fails the test unless every
// ConfigMap had its first read.
func followedTrust(t *testing.T, api *coreAPI, store *trustStore) {
	t.Helper()
	mustMatch(t, awaitSynced(t.Context(), followTrust(t, api, store)...), true)
}

func TestTrustStoreTrustsEverySiblingsCA(t *testing.T) {
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

func TestTrustStoreTrustsEveryCertificateInTheFile(t *testing.T) {
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

// When the API server fails the read, the first read never completes.
func TestTrustStoreTrustsNothingFromAFailedRead(t *testing.T) {
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

func TestTrustStoreWatchFollowsConfigMapChanges(t *testing.T) {
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
		mustMatch(t, strings.Contains(lines.lines[0], "does not trust its CA"), true)
		mustMatch(t, strings.Contains(lines.lines[1], "is present again"), true)
	})
}
