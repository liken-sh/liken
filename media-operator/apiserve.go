package main

// The api role uses no hardware, so it runs no pod informer and does
// not look up its node. It reads the Player of each request from the
// API server. It calls the display and audio APIs with a Bearer token
// of its own ServiceAccount, like any other client of those APIs.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
	"k8s.io/client-go/dynamic"
)

// The redirects build their Location header from the display and audio
// URLs. MEDIA_API_PUBLIC_BASE replaces the request's own origin as the
// server in the OpenAPI document, for callers that reach the API under
// a public name. The memory limit in deploy/api.yaml is sized for the
// default of four compositions, one ffmpeg process each.
const (
	apiAddressVariable         = "MEDIA_API_ADDRESS"
	apiDisplayURLVariable      = "MEDIA_API_DISPLAY_URL"
	apiAudioURLVariable        = "MEDIA_API_AUDIO_URL"
	apiMaxCompositionsVariable = "MEDIA_API_MAX_COMPOSITIONS"
	apiPublicBaseVariable      = "MEDIA_API_PUBLIC_BASE"
)

const (
	defaultAPIAddress      = ":8443"
	defaultDisplayURL      = "https://display-api.liken-system.svc"
	defaultAudioURL        = "https://audio-api.liken-system.svc"
	defaultMaxCompositions = 4
	defaultAPINamespace    = "liken-system"
)

// The ConfigMaps in which the display and audio APIs publish their CA
// certificates. This API trusts those certificates and watches the
// ConfigMaps for changes.
const (
	displayCAConfigMapName = "display-api-ca"
	audioCAConfigMapName   = "audio-api-ca"
)

// apiContainerName is this role's container in deploy/api.yaml. The
// role reads its release version from that container's image tag.
const apiContainerName = "api"

// serverReadHeaderTimeout bounds how long this API waits for a
// caller's request headers. The server has no write timeout, because a
// capture runs for as long as the caller keeps reading it.
const serverReadHeaderTimeout = 10 * time.Second

// certificateReview is how often reviewCertificate checks the serving
// certificate. The certificate is valid for a year and is renewed when
// a third of that is left, so an hourly check leaves many chances to
// renew it before it expires.
const certificateReview = time.Hour

// apiServer is the state that all requests share. The certificate has
// its own mutex because a renewal replaces it while requests are being
// served.
type apiServer struct {
	client   *apiclient.Client
	auth     *authorizer
	upstream *upstreamClient
	metrics  *apiMetrics
	// recorder posts the Captured Event on a Player. A nil recorder
	// posts nothing.
	recorder *events.Recorder

	display    string
	audio      string
	version    string
	publicBase string
	ffmpeg     string

	maxCompositions int
	compositions    chan struct{}

	ready atomic.Bool
	now   func() time.Time
	log   func(apiLogLine)

	certificates sync.Mutex
	certificate  *tls.Certificate

	// The cluster's client certificate authority. It is a value, not a
	// pointer, so a handshake never dereferences nil.
	anchors clientAnchors
}

func (s *apiServer) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// takeComposition reserves one of the maxCompositions places for an
// ffmpeg process and returns the function that releases it, or false
// when every place is taken. It does not wait. The caller responds 503
// with Retry-After, because a client that waited would keep its
// connection open for a capture that might never start.
func (s *apiServer) takeComposition() (func(), bool) {
	select {
	case s.compositions <- struct{}{}:
		return func() { <-s.compositions }, true
	default:
		return nil, false
	}
}

// origin is the server URL that the OpenAPI document gives: the
// configured public base, or else the origin that the request used.
func (s *apiServer) origin(e *apiExchange) string {
	if s.publicBase != "" {
		return s.publicBase
	}
	return "https://" + e.request.Host
}

func (s *apiServer) setCertificate(certificate *tls.Certificate) {
	s.certificates.Lock()
	defer s.certificates.Unlock()
	s.certificate = certificate
}

func (s *apiServer) servingCertificate() (*tls.Certificate, error) {
	s.certificates.Lock()
	defer s.certificates.Unlock()
	if s.certificate == nil {
		return nil, fmt.Errorf("no serving certificate is loaded")
	}
	return s.certificate, nil
}

// apiLogLine is the JSON line that each request writes to standard
// output. It never includes the caller's token or a hash of it.
type apiLogLine struct {
	ID            string   `json:"id"`
	Route         string   `json:"route"`
	Method        string   `json:"method"`
	Subject       string   `json:"subject,omitempty"`
	Status        int      `json:"status"`
	Bytes         int64    `json:"bytes"`
	HeaderSeconds float64  `json:"headerSeconds"`
	StreamSeconds float64  `json:"streamSeconds"`
	Upstreams     []string `json:"upstreams,omitempty"`
	OffsetSeconds float64  `json:"offsetSeconds,omitempty"`
	FFmpeg        string   `json:"ffmpeg,omitempty"`
}

func (e *apiExchange) finish() {
	if e.status == 0 {
		e.status = http.StatusOK
	}
	headerAt := e.headerAt
	if headerAt.IsZero() {
		headerAt = e.server.clock()
	}
	header := headerAt.Sub(e.started)
	e.server.metrics.observeRequest(e.route, e.request.Method, e.status, header)
	if e.server.log == nil {
		return
	}
	e.server.log(apiLogLine{
		ID:            e.id,
		Route:         e.route,
		Method:        e.request.Method,
		Subject:       e.subject,
		Status:        e.status,
		Bytes:         e.bytes,
		HeaderSeconds: header.Seconds(),
		StreamSeconds: e.server.clock().Sub(e.started).Seconds(),
		Upstreams:     e.upstreams,
		OffsetSeconds: e.offset,
		FFmpeg:        e.ffmpeg,
	})
}

func writeLogLine(line apiLogLine) {
	body, err := json.Marshal(line)
	if err != nil {
		return
	}
	fmt.Fprintln(os.Stdout, string(body))
}

// siblingClient is the HTTP client for requests to the display and
// audio APIs, which this file calls the siblings. It builds the TLS
// configuration at each dial from the trust store's current pool, so a
// connection made after a sibling's CA rotates trusts the new CA
// without a restart.
func siblingClient(trust *trustStore) *http.Client {
	return siblingClientOver(trust, (&net.Dialer{}).DialContext)
}

// siblingClientOver takes the dial function of the TLS connections, so
// a test can run the handshake over an in-memory pipe.
func siblingClientOver(trust *trustStore, dial func(ctx context.Context, network, address string) (net.Conn, error)) *http.Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 10 * time.Second}).DialContext,
		DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			raw, err := dial(ctx, network, address)
			if err != nil {
				return nil, err
			}
			conn := tls.Client(raw, &tls.Config{RootCAs: trust.pool(), ServerName: host, MinVersion: tls.VersionTLS12})
			if err := conn.HandshakeContext(ctx); err != nil {
				_ = raw.Close()
				return nil, err
			}
			return conn, nil
		},
		IdleConnTimeout: 30 * time.Second,
	}
	return &http.Client{Transport: transport}
}

// Each sibling accepts only tokens whose audience is its own, so the
// Deployment projects one token for each sibling.
var siblingTokenPaths = map[string]string{
	upstreamDisplay: "/var/run/secrets/liken.sh/display-api/token",
	upstreamAudio:   "/var/run/secrets/liken.sh/audio-api/token",
}

// siblingToken reads the token file on every call, because the kubelet
// rewrites a projected token before it expires and a token read once
// at start-up would expire.
func siblingToken(upstream string) (string, error) {
	path, named := siblingTokenPaths[upstream]
	if !named {
		return "", fmt.Errorf("no token is projected for the %s API", upstream)
	}
	token, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading the %s token: %w", upstream, err)
	}
	return string(token), nil
}

// serviceAccountToken reads this API's own token. apiServerResponds
// sends it in a TokenReview.
func serviceAccountToken() (string, error) {
	token, err := os.ReadFile(serviceAccountDir + "/token")
	if err != nil {
		return "", fmt.Errorf("reading the service account token: %w", err)
	}
	return string(token), nil
}

// firstReadWait is how long runAPI waits for the first read of the CA
// ConfigMaps before it starts to listen.
const firstReadWait = 10 * time.Second

func writeReport(line string) { fmt.Fprintln(os.Stderr, line) }

// runAPI starts the api role. It starts the metrics before the steps
// that can fail, so those failures show in the metrics. It exits when
// it has no API client or cannot get a serving certificate, because it
// cannot serve without them. A sibling that is not installed leaves
// that sibling's trust empty until its ConfigMap appears.
func runAPI() {
	client, err := inClusterClient(0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the in-cluster configuration: %v\n", err)
		os.Exit(1)
	}
	namespace := os.Getenv(podNamespaceVariable)
	if namespace == "" {
		namespace = defaultAPINamespace
	}
	version := containerVersion(client, apiContainerName)
	metrics := newAPIMetrics(version)
	metrics.serve(os.Getenv(metricsAddressVariable))

	watcher, err := informer.InCluster()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the in-cluster configuration for the watches: %v\n", err)
		os.Exit(1)
	}
	trust := newTrustStore(namespace, apiCAConfigMapName, displayCAConfigMapName, audioCAConfigMapName)
	trustRead := trust.follow(context.Background(), watcher)

	keeper := newCertificateKeeper(client, namespace)
	certificate, err := keeper.ensure()
	if err != nil {
		fmt.Fprintf(os.Stderr, "minting the serving certificate: %v\n", err)
		os.Exit(1)
	}

	auth := newAuthorizer(client)
	server := &apiServer{
		client:   client,
		auth:     auth,
		metrics:  metrics,
		recorder: events.New(context.Background(), client, apiComponent, events.Options{}),
		upstream: &upstreamClient{
			http:    siblingClient(trust),
			token:   siblingToken,
			metrics: metrics,
			clock:   func(string) time.Time { return time.Now() },
		},
		display:         environmentOr(apiDisplayURLVariable, defaultDisplayURL),
		audio:           environmentOr(apiAudioURLVariable, defaultAudioURL),
		version:         version,
		publicBase:      os.Getenv(apiPublicBaseVariable),
		ffmpeg:          "ffmpeg",
		maxCompositions: compositionLimit(),
		now:             time.Now,
		log:             writeLogLine,
	}
	server.compositions = make(chan struct{}, server.maxCompositions)
	server.setCertificate(certificate)
	metrics.observeExpiry(keeper.expirySeconds())
	go reviewCertificate(server, keeper, metrics)
	go watchServingPair(context.Background(), watcher, namespace, server, keeper, metrics)

	// A first read of the CA ConfigMaps that times out is not fatal.
	// Without them this API still serves every caller that sends a token
	// and still redirects captures, and each watch loads its ConfigMap
	// when the API server or the permission to read it comes back.
	anchorRead := make(chan struct{})
	go keepClientAnchors(context.Background(), watcher, &server.anchors, writeReport, anchorRead)
	firstRead, cancelFirstRead := context.WithTimeout(context.Background(), firstReadWait)
	if !awaitSynced(firstRead, append(trustRead, anchorRead)...) {
		fmt.Fprintf(os.Stderr, "the CA ConfigMaps were not read within %s, so media-api starts without them and loads them when they appear\n",
			firstReadWait)
	}
	cancelFirstRead()

	if apiServerResponds(auth) {
		server.ready.Store(true)
	}

	listener := server.listener(environmentOr(apiAddressVariable, defaultAPIAddress))
	if err := listener.ListenAndServeTLS("", ""); err != nil {
		fmt.Fprintf(os.Stderr, "serving on %s: %v\n", listener.Addr, err)
		os.Exit(1)
	}
}

// listener sets only a timeout on reading request headers. A capture
// has no known length, so a write timeout would cut every stream at the
// deadline. An idle timeout would cut a capture that is discarding data
// until its begin offset.
func (s *apiServer) listener(address string) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           s.handler(),
		ReadHeaderTimeout: serverReadHeaderTimeout,
		TLSConfig:         s.tlsConfig(),
	}
}

// tlsConfig returns the TLS configuration for the listener. The
// serving certificate and the client CA pool are read on every
// handshake, so a new certificate or a rotated client CA applies to the
// next connection without a restart. GetConfigForClient returns a
// new tls.Config each time because a tls.Config that a handshake is
// reading must not be modified.
//
// VerifyClientCertIfGiven means that a caller with no client
// certificate is still checked by its token, and a caller that sends
// one has it verified before any route runs. NextProtos lists the
// protocols that net/http would have negotiated, because this
// configuration replaces the one that net/http builds.
func (s *apiServer) tlsConfig() *tls.Config {
	certificate := func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return s.servingCertificate()
	}
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: certificate,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			return &tls.Config{
				MinVersion:     tls.VersionTLS12,
				GetCertificate: certificate,
				NextProtos:     []string{"h2", "http/1.1"},
				ClientAuth:     tls.VerifyClientCertIfGiven,
				ClientCAs:      s.anchors.current(),
			}, nil
		},
	}
}

// apiServerResponds returns true when a TokenReview of this API's own
// token gets a response from the API server, whether or not the review
// accepts the token. The token's audience is the API server and not
// media-api, so the review normally refuses it. The check tests only
// that TokenReview requests reach the API server and return, because
// every request this API serves needs that.
func apiServerResponds(auth *authorizer) bool {
	token, err := serviceAccountToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the service account token of media-api: %v\n", err)
		return false
	}
	_, err = auth.authenticate(token)
	if err == nil {
		return true
	}
	var refused unauthenticatedError
	if errors.As(err, &refused) {
		return true
	}
	fmt.Fprintf(os.Stderr, "reviewing the service account token of media-api with a TokenReview: %v\n", err)
	return false
}

// reviewCertificate checks the serving certificate every
// certificateReview, so the certificate is renewed before it expires
// and no request waits for a renewal. No event reports that a
// certificate is near expiry, so this is a timer.
func reviewCertificate(server *apiServer, keeper *certificateKeeper, metrics *apiMetrics) {
	for range time.Tick(certificateReview) {
		if err := refreshCertificate(server, keeper, metrics); err != nil {
			fmt.Fprintf(os.Stderr, "renewing the serving certificate: %v\n", err)
		}
	}
}

// watchServingPair watches the Secret media-api-tls, so a certificate
// and key that another writer puts there serve the next connection
// without a restart. The other writer is a cluster owner who replaces
// the pair with one from their own CA, or a second media-api pod that
// renewed the certificate first. When the Secret is deleted, the
// keeper creates a new pair at once, because the listener cannot serve
// without one. The keeper's own writes also arrive as events, and each
// costs one read that changes nothing.
func watchServingPair(ctx context.Context, watcher dynamic.Interface, namespace string, server *apiServer,
	keeper *certificateKeeper, metrics *apiMetrics) {
	pair := informer.One{Resource: secretResource, Namespace: namespace, Name: apiTLSSecretName,
		What: "Secret " + namespace + "/" + apiTLSSecretName}
	<-informer.WatchOne(ctx, watcher, pair, func(*Secret) {
		if err := refreshCertificate(server, keeper, metrics); err != nil {
			fmt.Fprintf(os.Stderr, "renewing the serving certificate after the Secret changed: %v\n", err)
		}
	}, nil).Done()
}

// refreshCertificate renews the certificate when it is near expiry and
// makes the result the serving certificate.
func refreshCertificate(server *apiServer, keeper *certificateKeeper, metrics *apiMetrics) error {
	certificate, err := keeper.ensure()
	if err != nil {
		return err
	}
	server.setCertificate(certificate)
	metrics.observeExpiry(keeper.expirySeconds())
	return nil
}

func environmentOr(variable, fallback string) string {
	if value := os.Getenv(variable); value != "" {
		return value
	}
	return fallback
}

// compositionLimit returns the default for a value that is not an
// integer or is under one, and does not stop start-up, because the rest
// of the API does not depend on the limit.
func compositionLimit() int {
	value, err := strconv.Atoi(os.Getenv(apiMaxCompositionsVariable))
	if err != nil || value < 1 {
		return defaultMaxCompositions
	}
	return value
}
