package main

// These tests cover the trust store: every sibling's CA is an anchor,
// an absent sibling is tolerated, every certificate in a rotating
// file is an anchor, and the watch keeps the pool current and
// recovers from an ended stream.

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
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
// dropping a deleted one, and it opens the named object's own path
// from the read's resourceVersion, which is what the Role's
// resourceNames authorize.
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
	mustMatch(t, opened.Path, "/api/v1/namespaces/liken-system/configmaps/"+displayCAConfigMapName)
	mustMatch(t, opened.Query().Get("resourceVersion"), "11")

	events <- configMapEvent(t, "MODIFIED", caConfigMap(displayCAConfigMapName, was.certPEM, next.certPEM))
	until(t, "the pool never took up the anchor the watch delivered", func() bool {
		return chainsTo(t, next, store.pool()) == nil
	})

	events <- configMapEvent(t, "DELETED", caConfigMap(displayCAConfigMapName, was.certPEM))
	until(t, "the pool kept an anchor the watch deleted", func() bool {
		return chainsTo(t, was, store.pool()) != nil
	})
}

// A stream that ends, and a read that fails once, leave the pool
// current within one pause: the next read takes up the new anchor.
func TestTrustStoreWatchRecoversFromAnEndedStream(t *testing.T) {
	api := newCoreAPI()
	was, next := testAuthority(t), testAuthority(t)
	api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, was.certPEM))

	reads := &atomic.Int32{}
	answer := api.handler()
	ending := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The watch opens and the stream ends at once, which is what a
		// dropped connection reaches this loop as.
		if r.URL.Query().Get("watch") == "true" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if reads.Add(1) == 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		answer.ServeHTTP(w, r)
	})

	store := newTrustStore(testAPIClient(t, ending), "liken-system", displayCAConfigMapName)
	store.retry = time.Millisecond
	mustSucceed(t, store.load())

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	store.watch(ctx)

	api.seed(t, "configmaps/"+displayCAConfigMapName, caConfigMap(displayCAConfigMapName, next.certPEM))
	until(t, "the pool never took up the anchor the read delivered", func() bool {
		return chainsTo(t, next, store.pool()) == nil
	})
}

// until waits for an outcome with a deadline, because the watch is
// another goroutine and a test asserts what the pool holds, not when
// the goroutine ran.
func until(t *testing.T, complaint string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(watchTimeout)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal(complaint)
		}
		time.Sleep(watchQuietSpell / 10)
	}
}

func configMapEvent(t *testing.T, kind string, object *ConfigMap) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"type": kind, "object": object})
	mustSucceed(t, err)
	return string(body)
}

// watchingAPI wraps the core API with a watch: it records each watch
// request's URL and writes the events the test sends until the
// channel closes.
func watchingAPI(api *coreAPI, events <-chan string, watched chan<- *url.URL) http.Handler {
	answer := api.handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") != "true" {
			answer.ServeHTTP(w, r)
			return
		}
		watched <- r.URL
		for event := range events {
			_, _ = io.WriteString(w, event)
			w.(http.Flusher).Flush()
		}
	})
}

// A cluster that runs one sibling and not the other answers 404 to
// every watch of the absent one. That costs one line and one request
// per backoff step, never a line and a request several times a second.
func TestTrustStoreBacksOffAnAbsentConfigMap(t *testing.T) {
	api := newCoreAPI()
	store := newTrustStore(testAPIClient(t, api.handler()), "liken-system", displayCAConfigMapName)
	mustSucceed(t, store.load())

	waits := make(chan time.Duration, 8)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var lines []string
	var said sync.Mutex
	store.report = func(line string) {
		said.Lock()
		defer said.Unlock()
		lines = append(lines, line)
	}
	store.pause = func(_ context.Context, wait time.Duration) bool {
		select {
		case waits <- wait:
			return true
		default:
			stop()
			return false
		}
	}

	store.follow(ctx, displayCAConfigMapName)

	close(waits)
	var stepped []time.Duration
	for wait := range waits {
		stepped = append(stepped, wait)
	}
	mustMatchAll(t, stepped, []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, time.Minute, time.Minute,
	})
	said.Lock()
	defer said.Unlock()
	mustMatch(t, len(lines), 1)
	mustMatch(t, strings.Contains(lines[0], "404 Not Found"), true)
	mustMatch(t, strings.Contains(lines[0], "composes no stream through that API"), true)
}

// A sibling that appears reports once more, and the wait starts over,
// because the next failure is a new fault.
func TestTrustStoreReportsASiblingThatAppears(t *testing.T) {
	api := newCoreAPI()
	store := newTrustStore(testAPIClient(t, api.handler()), "liken-system", displayCAConfigMapName)
	store.retry = time.Millisecond
	mustSucceed(t, store.load())

	var lines []string
	var said sync.Mutex
	store.report = func(line string) {
		said.Lock()
		defer said.Unlock()
		lines = append(lines, line)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	attempts := 0
	authority := testAuthority(t)
	store.pause = func(context.Context, time.Duration) bool {
		attempts++
		if attempts == 2 {
			api.seed(t, "configmaps/"+displayCAConfigMapName,
				caConfigMap(displayCAConfigMapName, authority.certPEM))
		}
		return attempts < 4
	}

	store.follow(ctx, displayCAConfigMapName)

	said.Lock()
	defer said.Unlock()
	mustMatch(t, len(lines), 2)
	mustMatch(t, strings.Contains(lines[0], "404 Not Found"), true)
	mustMatch(t, strings.Contains(lines[1], "the object is present"), true)
	mustSucceed(t, chainsTo(t, authority, store.pool()))
}
