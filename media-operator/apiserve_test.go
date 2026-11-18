package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
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

func TestACompositionSlotIsFreeOnlyAfterItIsReleased(t *testing.T) {
	server := &apiServer{compositions: make(chan struct{}, 1)}

	release, taken := server.takeComposition()
	_, second := server.takeComposition()
	release()
	_, third := server.takeComposition()

	mustMatch(t, taken, true)
	mustMatch(t, second, false)
	mustMatch(t, third, true)
}

func TestTheServingCertificateIsTheLastOneSet(t *testing.T) {
	server := &apiServer{}

	_, err := server.servingCertificate()
	mustFail(t, err)

	held := &tls.Certificate{}
	server.setCertificate(held)
	loaded, err := server.servingCertificate()

	mustSucceed(t, err)
	mustMatch(t, loaded, held)
}

func TestTheOriginIsTheConfiguredBaseOrTheRequestsOwn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)

		recorder := fixture.get(apiBasePath + "/openapi.json")
		mustMatch(t, serverListed(t, recorder.Body.Bytes()), "https://example.com")

		fixture.server.publicBase = "https://media.example"
		recorder = fixture.get(apiBasePath + "/openapi.json")
		mustMatch(t, serverListed(t, recorder.Body.Bytes()), "https://media.example")
	})
}

func serverListed(t *testing.T, body []byte) string {
	t.Helper()
	var document struct {
		Servers []openAPIServer `json:"servers"`
	}
	mustSucceed(t, json.Unmarshal(body, &document))
	return document.Servers[0].URL
}

// downServer refuses every connection, like a port with nothing
// listening.
func downServer(t *testing.T) *apiservertest.Server {
	t.Helper()
	server := apiservertest.Start(t, http.NotFoundHandler())
	server.SetDown(true)
	return server
}

// When the API server does not respond to a TokenReview, the response
// is 503 with Retry-After, because the fault is outside this API and a
// retry can succeed.
func TestAnApiServerThatDoesNotRespondIsAServiceUnavailable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.client = apiclient.New(apiservertest.Host, downServer(t).Client(), "")

		recorder := fixture.get(playerPathFor("screen.png"))

		mustMatch(t, recorder.Code, http.StatusServiceUnavailable)
		mustMatch(t, recorder.Header().Get("Retry-After"), retryAfterSeconds)
	})
}

// A log collector can read the line without a parser of its own.
func TestTheLogLineIsOneJSONObject(t *testing.T) {
	read, written, err := os.Pipe()
	mustSucceed(t, err)
	saved := os.Stdout
	os.Stdout = written
	defer func() { os.Stdout = saved }()

	writeLogLine(apiLogLine{ID: "7f3c2a19", Route: apiPlayerTemplate, Status: 200})
	mustSucceed(t, written.Close())

	body := make([]byte, 512)
	count, _ := read.Read(body)
	var line apiLogLine
	mustSucceed(t, json.Unmarshal(body[:count], &line))
	mustMatch(t, line.ID, "7f3c2a19")
	mustMatch(t, line.Route, apiPlayerTemplate)
}

// A missing token file is an error, not an empty token.
func TestTheServiceAccountTokenIsReadFromDisk(t *testing.T) {
	directory := t.TempDir()
	saved := serviceAccountDir
	serviceAccountDir = directory
	defer func() { serviceAccountDir = saved }()

	_, err := serviceAccountToken()
	mustFail(t, err)

	mustSucceed(t, os.WriteFile(filepath.Join(directory, "token"), []byte("own-token"), 0o600))
	token, err := serviceAccountToken()

	mustSucceed(t, err)
	mustMatch(t, token, "own-token")
}

// The API is ready once the API server responds to a TokenReview,
// whether or not the review accepts this API's own token.
func TestReadyOnceTheApiServerResponds(t *testing.T) {
	directory := t.TempDir()
	saved := serviceAccountDir
	serviceAccountDir = directory
	defer func() { serviceAccountDir = saved }()
	mustSucceed(t, os.WriteFile(filepath.Join(directory, "token"), []byte("own-token"), 0o600))

	plane := newControlPlane()
	plane.authenticated = false
	plane.message = "the audience media-api is not one of this token's"
	responding := apiservertest.Start(t, plane.handler()).Client()

	mustMatch(t, apiServerResponds(newAuthorizer(apiclient.New(apiservertest.Host, responding, ""))), true)
	mustMatch(t, apiServerResponds(newAuthorizer(apiclient.New(apiservertest.Host, downServer(t).Client(), ""))), false)
}

// The sibling serves TLS on a pipeListener, so the handshake runs
// inside the synctest bubble.
func TestASiblingIsReachedOnlyWithItsCAInTheTrustStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		authority, err := mintAuthority(time.Now())
		mustSucceed(t, err)
		certPEM, keyPEM, err := authority.mintLeaf(time.Now(), "localhost")
		mustSucceed(t, err)
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		mustSucceed(t, err)
		socket := newPipeListener()
		sibling := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("hello"))
			}),
			TLSConfig: &tls.Config{Certificates: []tls.Certificate{pair}},
			ErrorLog:  log.New(io.Discard, "", 0),
		}
		go func() { _ = sibling.ServeTLS(socket, "", "") }()
		t.Cleanup(func() { _ = sibling.Close() })

		empty := newTrustStore(defaultAPINamespace, displayCAConfigMapName)
		trusted := newTrustStore(defaultAPINamespace, displayCAConfigMapName)
		followedTrust(t, clusterHolding(t, ""), empty)
		followedTrust(t, clusterHolding(t, string(authority.certPEM)), trusted)

		_, err = siblingClientOver(empty, socket.dial).Get("https://localhost/")
		mustFail(t, err)
		response, err := siblingClientOver(trusted, socket.dial).Get("https://localhost/")

		mustSucceed(t, err)
		drain(response.Body)
		mustMatch(t, response.StatusCode, http.StatusOK)
	})
}

// clusterHolding returns a fake API server with a CA ConfigMap that
// contains the given certificate, or with no ConfigMap when the
// certificate is empty.
func clusterHolding(t *testing.T, certificate string) *coreAPI {
	t.Helper()
	api := newCoreAPI()
	if certificate != "" {
		api.seed(t, "configmaps/"+displayCAConfigMapName, &ConfigMap{
			Metadata: ObjectMeta{Name: displayCAConfigMapName, ResourceVersion: "1"},
			Data:     map[string]string{apiCACertKey: certificate},
		})
	}
	return api
}

// When an upstream body sends nothing for longer than
// upstreamIdleTimeout, the composition and the upstream request end, so
// the client receives a truncated stream and does not wait forever.
//
// The test runs on the real clock, outside a synctest bubble, because
// the composition reads a fake ffmpeg's output from an OS pipe. A read
// on an OS pipe is not durably blocked, so the bubble's fake clock
// would never reach the timeout.
func TestABodyThatStopsSendingEndsTheComposition(t *testing.T) {
	saved := upstreamIdleTimeout
	upstreamIdleTimeout = 50 * time.Millisecond
	defer func() { upstreamIdleTimeout = saved }()

	fixture := newAPIFixture(t)
	fixture.server.now = time.Now
	fixture.display.stall = true
	fixture.server.ffmpeg = recordingFFmpeg(t, filepath.Join(t.TempDir(), "arguments"))

	recorder := fixture.get(playerPathFor("media.mp4"))

	mustMatch(t, recorder.Code, http.StatusOK)
	mustMatch(t, fixture.display.ended(), true)
	mustMatch(t, len(fixture.lines), 1)
}

func TestEachSiblingIsCalledWithItsOwnToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.now = time.Now
		fixture.server.ffmpeg = recordingFFmpeg(t, filepath.Join(t.TempDir(), "arguments"))

		fixture.get(playerPathFor("media.mp4"))

		mustMatchAll(t, fixture.display.sent(), []string{"Bearer token-for-display-api"})
		mustMatchAll(t, fixture.audio.sent(), []string{"Bearer token-for-audio-api"})
	})
}

// The problem detail names the sibling whose token could not be read,
// so the cluster owner can see which projected volume is missing.
func TestASiblingWithNoProjectedTokenIsAServiceUnavailable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		siblingTokenPaths[upstreamAudio] = filepath.Join(t.TempDir(), "absent")

		recorder := fixture.get(playerPathFor("media.mp4"))

		mustMatch(t, recorder.Code, http.StatusServiceUnavailable)
		mustMatch(t, strings.Contains(problemOf(t, recorder).Detail, "reading the audio token"), true)
	})
}

// A cluster can run this API before it runs the display and audio
// APIs. Every route that this API serves by itself still works. Only a
// composition that needs the missing sibling fails, with a problem
// detail.
func TestTheApiServesWhileASiblingCAIsAbsent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

// The watch selects the Secret by name, so it lists and watches no
// other Secret of the namespace.
func TestTheServingPairFollowsTheSecret(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := newCoreAPI()
		client := testAPIClient(t, api.handler())
		keeper := newCertificateKeeper(client, "liken-system")
		server := &apiServer{}
		mustSucceed(t, refreshCertificate(server, keeper, newAPIMetrics("test")))
		watcher := testWatcher(t, api.handler())
		ctx, stop := context.WithCancel(context.Background())
		done := make(chan struct{})
		t.Cleanup(func() {
			stop()
			<-done
		})
		go func() {
			defer close(done)
			watchServingPair(ctx, watcher, "liken-system", server, keeper, newAPIMetrics("test"))
		}()
		synctest.Wait()
		mustMatch(t, api.opened() > 0, true)

		certPEM, keyPEM, caPEM := ownersPair(t, time.Now())
		owners := &Secret{
			Metadata: ObjectMeta{Name: apiTLSSecretName, Namespace: "liken-system", ResourceVersion: "30"},
			Data:     map[string][]byte{apiTLSCertKey: certPEM, apiTLSKeyKey: keyPEM, apiCACertKey: caPEM},
		}
		api.seed(t, "secrets/"+apiTLSSecretName, owners)

		synctest.Wait()

		serving, err := server.servingCertificate()
		mustSucceed(t, err)
		mustMatch(t, string(serving.Certificate[0]), string(derOf(t, certPEM)))
		for _, request := range api.requested() {
			mustMatch(t, request, "/api/v1/namespaces/liken-system/secrets?fieldSelector=metadata.name="+apiTLSSecretName)
		}
	})
}
