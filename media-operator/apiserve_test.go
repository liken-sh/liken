package main

// These tests cover the role's own process: the settings it reads and
// their defaults, the composition slots, the certificate it serves,
// the trust it dials a sibling with, the tokens it sends, the log
// line, and the readiness latch.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

func TestASettingFallsBackToItsDefault(t *testing.T) {
	t.Setenv(apiDisplayURLVariable, "")
	mustMatch(t, environmentOr(apiDisplayURLVariable, defaultDisplayURL), defaultDisplayURL)

	t.Setenv(apiDisplayURLVariable, "https://display.example")
	mustMatch(t, environmentOr(apiDisplayURLVariable, defaultDisplayURL), "https://display.example")
}

func TestTheCompositionLimitReadsTheEnvironment(t *testing.T) {
	rows := []struct {
		value string
		want  int
	}{
		{"", defaultMaxCompositions},
		{"8", 8},
		{"0", defaultMaxCompositions},
		{"many", defaultMaxCompositions},
	}
	for _, row := range rows {
		t.Run(row.value, func(t *testing.T) {
			t.Setenv(apiMaxCompositionsVariable, row.value)

			mustMatch(t, compositionLimit(), row.want)
		})
	}
}

func TestOnlyOneCompositionRunsPerSlot(t *testing.T) {
	server := &apiServer{compositions: make(chan struct{}, 1)}

	release, taken := server.takeComposition()
	_, second := server.takeComposition()
	release()
	_, third := server.takeComposition()

	mustMatch(t, taken, true)
	mustMatch(t, second, false)
	mustMatch(t, third, true)
}

func TestTheServingCertificateIsTheOneLastHeld(t *testing.T) {
	server := &apiServer{}

	_, err := server.servingCertificate()
	mustFail(t, err)

	held := &tls.Certificate{}
	server.holdCertificate(held)
	loaded, err := server.servingCertificate()

	mustSucceed(t, err)
	mustMatch(t, loaded, held)
}

func TestTheOriginIsTheConfiguredBaseOrTheRequestsOwn(t *testing.T) {
	fixture := newAPIFixture(t)

	recorder := fixture.get(apiBasePath + "/openapi.json")
	mustMatch(t, serverListed(t, recorder.Body.Bytes()), "https://example.com")

	fixture.server.publicBase = "https://media.example"
	recorder = fixture.get(apiBasePath + "/openapi.json")
	mustMatch(t, serverListed(t, recorder.Body.Bytes()), "https://media.example")
}

func serverListed(t *testing.T, body []byte) string {
	t.Helper()
	var document struct {
		Servers []openAPIServer `json:"servers"`
	}
	mustSucceed(t, json.Unmarshal(body, &document))
	return document.Servers[0].URL
}

// An API server that will not answer a review is a 503 with
// Retry-After, because the fault is not in this API and a retry
// clears it.
func TestAnApiServerThatWillNotAnswerIsAServiceUnavailable(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.client = apiclient.New("http://127.0.0.1:1", http.DefaultClient, "")

	recorder := fixture.get(playerPathFor("screen.png"))

	mustMatch(t, recorder.Code, http.StatusServiceUnavailable)
	mustMatch(t, recorder.Header().Get("Retry-After"), retryAfterSeconds)
}

// The log line is one JSON object per request on standard output, so
// a log collector reads it without a parser of its own.
func TestTheLogLineIsOneJSONObject(t *testing.T) {
	read, written, err := os.Pipe()
	mustSucceed(t, err)
	held := os.Stdout
	os.Stdout = written
	defer func() { os.Stdout = held }()

	writeLogLine(apiLogLine{ID: "7f3c2a19", Route: apiPlayerTemplate, Status: 200})
	mustSucceed(t, written.Close())

	body := make([]byte, 512)
	count, _ := read.Read(body)
	var line apiLogLine
	mustSucceed(t, json.Unmarshal(body[:count], &line))
	mustMatch(t, line.ID, "7f3c2a19")
	mustMatch(t, line.Route, apiPlayerTemplate)
}

// This API's own token is read from the path the kubelet mounts, and a
// missing file is an error rather than an empty token.
func TestTheServiceAccountTokenIsReadFromDisk(t *testing.T) {
	directory := t.TempDir()
	held := serviceAccountDir
	serviceAccountDir = directory
	defer func() { serviceAccountDir = held }()

	_, err := serviceAccountToken()
	mustFail(t, err)

	mustSucceed(t, os.WriteFile(filepath.Join(directory, "token"), []byte("own-token"), 0o600))
	token, err := serviceAccountToken()

	mustSucceed(t, err)
	mustMatch(t, token, "own-token")
}

// Readiness latches on one answer from the API server, whether or not
// that answer accepts this API's own token, and stays down while the
// API server does not answer at all.
func TestReadinessLatchesOnAnAnsweringApiServer(t *testing.T) {
	directory := t.TempDir()
	held := serviceAccountDir
	serviceAccountDir = directory
	defer func() { serviceAccountDir = held }()
	mustSucceed(t, os.WriteFile(filepath.Join(directory, "token"), []byte("own-token"), 0o600))

	plane := newControlPlane()
	plane.authenticated = false
	plane.words = "the audience media-api is not one of this token's"
	answering := httptest.NewServer(plane.handler())
	defer answering.Close()

	mustMatch(t, reviewsAnswer(newAuthorizer(apiclient.New(answering.URL, answering.Client(), ""))), true)
	mustMatch(t, reviewsAnswer(newAuthorizer(apiclient.New("http://127.0.0.1:1", http.DefaultClient, ""))), false)
}

// A sibling is reached only through an anchor the trust store holds:
// with the sibling's CA in a ConfigMap the dial succeeds, and with no
// anchor it fails.
func TestASiblingIsReachedOnlyThroughATrustedAnchor(t *testing.T) {
	sibling := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer sibling.Close()
	anchor := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: sibling.Certificate().Raw})

	empty := newTrustStore(defaultAPINamespace, displayCAConfigMapName)
	followTrust(t, clusterHolding(t, ""), empty)
	_, err := siblingClient(empty).Get(sibling.URL)
	mustFail(t, err)

	trusted := newTrustStore(defaultAPINamespace, displayCAConfigMapName)
	followTrust(t, clusterHolding(t, string(anchor)), trusted)
	response, err := siblingClient(trusted).Get(sibling.URL)

	mustSucceed(t, err)
	drain(response.Body)
	mustMatch(t, response.StatusCode, http.StatusOK)
}

// clusterHolding stands in for an API server that holds one CA
// ConfigMap with the given anchor, or nothing when the anchor is
// empty.
func clusterHolding(t *testing.T, anchor string) *coreAPI {
	t.Helper()
	api := newCoreAPI()
	if anchor != "" {
		api.seed(t, "configmaps/"+displayCAConfigMapName, &ConfigMap{
			Metadata: ObjectMeta{Name: displayCAConfigMapName, ResourceVersion: "1"},
			Data:     map[string]string{apiCACertKey: anchor},
		})
	}
	return api
}

// A body that goes quiet past the idle timeout ends the composition
// and the upstream request, so the client reads a truncated stream
// instead of waiting forever.
func TestABodyThatGoesQuietEndsTheComposition(t *testing.T) {
	held := upstreamIdleTimeout
	upstreamIdleTimeout = 50 * time.Millisecond
	defer func() { upstreamIdleTimeout = held }()

	fixture := newAPIFixture(t)
	fixture.server.now = time.Now
	fixture.display.stall = true
	fixture.server.ffmpeg = recordingFFmpeg(t, filepath.Join(t.TempDir(), "arguments"))

	recorder := fixture.get(playerPathFor("media.mp4"))

	mustMatch(t, recorder.Code, http.StatusOK)
	mustMatch(t, fixture.display.ended(), true)
	mustMatch(t, len(fixture.lines), 1)
}

// Each sibling is called with the token projected for its own
// audience, because each public API reviews the audience it is named
// for and one token cannot open both.
func TestEachSiblingIsCalledWithItsOwnToken(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.now = time.Now
	fixture.server.ffmpeg = recordingFFmpeg(t, filepath.Join(t.TempDir(), "arguments"))

	fixture.get(playerPathFor("media.mp4"))

	mustMatchAll(t, fixture.display.sent(), []string{"Bearer token-for-display-api"})
	mustMatchAll(t, fixture.audio.sent(), []string{"Bearer token-for-audio-api"})
}

// A sibling with no projected token is a 503 whose detail names the
// file this API could not read, so the owner sees which volume is
// missing.
func TestASiblingWithNoProjectedTokenIsAServiceUnavailable(t *testing.T) {
	fixture := newAPIFixture(t)
	siblingTokenPaths[upstreamAudio] = filepath.Join(t.TempDir(), "absent")

	recorder := fixture.get(playerPathFor("media.mp4"))

	mustMatch(t, recorder.Code, http.StatusServiceUnavailable)
	mustMatch(t, strings.Contains(problemOf(t, recorder).Detail, "reading the audio token"), true)
}

// A cluster may run this API before it runs the siblings. Every route
// this API answers for itself still answers, and only a composition
// through the absent sibling fails, with that sibling's own reason.
func TestTheApiServesWhileASiblingAnchorIsAbsent(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.display = testDisplayBase
	fixture.server.audio = testAudioBase

	mustMatch(t, fixture.get(apiBasePath).Code, http.StatusOK)
	mustMatch(t, fixture.get(playerPathFor("")).Code, http.StatusOK)
	redirect := fixture.get(playerPathFor("screen.png"))
	mustMatch(t, redirect.Code, http.StatusTemporaryRedirect)
	mustMatch(t, redirect.Header().Get("Location"),
		testDisplayBase+"/v1/display/displays/boe-1080/screen.png")

	refused := fixture.get(playerPathFor("media.mp4"))

	mustMatch(t, refused.Code, http.StatusServiceUnavailable)
	mustMatch(t, refused.Header().Get("Retry-After"), retryAfterSeconds)
	mustMatch(t, problemOf(t, refused).Detail != "", true)
}

// A pair that another writer puts in the Secret serves the next
// connection when the watch delivers it, with no wait for the hourly
// review.
func TestTheServingPairFollowsTheSecret(t *testing.T) {
	api := newCoreAPI()
	client := testAPIClient(t, api.handler())
	keeper := newCertificateKeeper(client, "liken-system")
	server := &apiServer{}
	mustSucceed(t, refreshCertificate(server, keeper, newAPIMetrics("test")))

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	defer func() {
		stop()
		<-done
	}()
	watcher := testWatcher(t, api.handler())
	go func() {
		defer close(done)
		watchServingPair(ctx, watcher, "liken-system", server, keeper, newAPIMetrics("test"))
	}()
	until(t, "the watch never opened", func() bool { return api.opened() > 0 })

	certPEM, keyPEM, caPEM := ownersPair(t, time.Now())
	owners := &Secret{
		Metadata: ObjectMeta{Name: apiTLSSecretName, Namespace: "liken-system", ResourceVersion: "30"},
		Data:     map[string][]byte{apiTLSCertKey: certPEM, apiTLSKeyKey: keyPEM, apiCACertKey: caPEM},
	}
	api.seed(t, "secrets/"+apiTLSSecretName, owners)

	until(t, "the listener never served the pair the watch delivered", func() bool {
		serving, err := server.servingCertificate()
		return err == nil && string(serving.Certificate[0]) == string(derOf(t, certPEM))
	})
	for _, request := range api.requested() {
		mustMatch(t, request, "/api/v1/namespaces/liken-system/secrets?fieldSelector=metadata.name="+apiTLSSecretName)
	}
}
