package main

// This file is the api role's process: its configuration, its
// start-up, and the server every request runs under. The role holds
// no hardware and has no sidecar, so it runs no pod informer and finds
// no node. It reads one object per request, the Player, and calls the
// two sibling APIs as an ordinary Bearer client under its own
// ServiceAccount, the pattern for one liken API calling another.

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
)

// The api role's own environment. The address is where HTTPS is
// served, the two URLs are the sibling APIs every Location is built
// from, the limit bounds the ffmpeg processes, and the public base is
// the origin the served OpenAPI names when the API is reached under a
// public name. Each default states the in-cluster case: the .svc
// names, and four compositions, the number the memory limit in
// deploy/api.yaml is sized for.
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

// The two ConfigMaps the siblings publish their CA certificates in,
// which this API trusts and watches.
const (
	displayCAConfigMapName = "display-api-ca"
	audioCAConfigMapName   = "audio-api-ca"
)

// apiContainerName is this role's container in deploy/api.yaml. The
// release the role runs is read off that container's image tag.
const apiContainerName = "api"

// serverReadHeaderTimeout bounds how long this API waits for a
// caller's own request headers. It is this server's bound and not the
// upstream one, although both are ten seconds: a composed response has
// no write timeout at all, because a capture runs for as long as the
// caller reads it.
const serverReadHeaderTimeout = 10 * time.Second

// certificateReview is how often the keeper looks at the leaf's
// remaining life. The leaf lives a year and re-mints with a third
// left, so an hour is many chances before the deadline.
const certificateReview = time.Hour

// apiServer is what every request shares for the life of the process:
// the API client and the authorizer, the upstream client, the
// metrics, the configuration, the composition slots, the readiness
// latch, and the serving certificate under its own lock, because the
// keeper replaces it while requests are being served.
type apiServer struct {
	client   *Client
	auth     *authorizer
	upstream *upstreamClient
	metrics  *apiMetrics

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

	// The cluster's client certificate authority. It is a value rather
	// than a pointer, so every apiServer has one and a handshake never
	// reads through a nil pointer.
	anchors clientAnchors
}

func (s *apiServer) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// takeComposition takes one of the composition slots, a buffered
// channel with one place per allowed ffmpeg process, and answers the
// release. A full set is a 503 rather than a wait, because a client
// that waits holds a connection for a capture that may never start,
// and a Retry-After tells it when to ask again.
func (s *apiServer) takeComposition() (func(), bool) {
	select {
	case s.compositions <- struct{}{}:
		return func() { <-s.compositions }, true
	default:
		return nil, false
	}
}

// origin is the server the served OpenAPI names: the configured
// public base, or else the origin the request reached.
func (s *apiServer) origin(e *apiExchange) string {
	if s.publicBase != "" {
		return s.publicBase
	}
	return "https://" + e.request.Host
}

func (s *apiServer) holdCertificate(certificate *tls.Certificate) {
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

// apiLogLine is the one structured line each request writes, the
// detail record beside the metrics: the request id, the route
// template, the method, the subject, the status, the bytes, the time
// to the headers and to the end, and on a composition the upstream
// URLs, the offset, and what ffmpeg wrote to stderr. It never carries
// the token or any hash of it.
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

// siblingClient is the HTTP client the upstream requests use. It
// builds the TLS configuration at dial time from the trust store's
// current pool, rather than once, so a rotated sibling CA reaches the
// next connection with no restart.
func siblingClient(trust *trustStore) *http.Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 10 * time.Second}).DialContext,
		DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			dialer := &tls.Dialer{Config: &tls.Config{RootCAs: trust.pool(), ServerName: host, MinVersion: tls.VersionTLS12}}
			return dialer.DialContext(ctx, network, address)
		},
		IdleConnTimeout: 30 * time.Second,
	}
	return &http.Client{Transport: transport}
}

// The projected token per sibling. Each public API reviews the
// audience it is named for, so one token cannot open both, and the
// Deployment projects one token per audience.
var siblingTokenPaths = map[string]string{
	upstreamDisplay: "/var/run/secrets/liken.sh/display-api/token",
	upstreamAudio:   "/var/run/secrets/liken.sh/audio-api/token",
}

// siblingToken reads the token file on every call rather than once,
// because the kubelet rewrites a projected token as it nears its
// expiry and a token read at start would stop working.
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

// serviceAccountToken is this API's own token, read from the path the
// kubelet mounts. The readiness latch reviews it to prove the API
// server answers.
func serviceAccountToken() (string, error) {
	token, err := os.ReadFile(serviceAccountDir + "/token")
	if err != nil {
		return "", fmt.Errorf("reading service account token: %w", err)
	}
	return string(token), nil
}

// runAPI is the api role's start-up, in the order a failure matters:
// the API client, which nothing works without; the metrics, so a
// failure later is visible; the sibling trust, which a missing sibling
// leaves empty and the watch fills later; the serving certificate,
// which the process cannot listen without; then the readiness latch
// and the listener.
func runAPI() {
	client, err := InClusterClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "in-cluster config: %v\n", err)
		os.Exit(1)
	}
	namespace := os.Getenv(podNamespaceVariable)
	if namespace == "" {
		namespace = defaultAPINamespace
	}
	version := containerVersion(client, apiContainerName)
	metrics := newAPIMetrics(version)
	metrics.serve(os.Getenv(metricsAddressVariable))

	trust := newTrustStore(client, namespace, apiCAConfigMapName, displayCAConfigMapName, audioCAConfigMapName)
	if err := trust.load(); err != nil {
		fmt.Fprintf(os.Stderr, "reading the sibling certificate authorities: %v\n", err)
	}
	go trust.watch(context.Background())

	keeper := newCertificateKeeper(client, namespace)
	certificate, err := keeper.ensure()
	if err != nil {
		fmt.Fprintf(os.Stderr, "minting the serving certificate: %v\n", err)
		os.Exit(1)
	}

	auth := newAuthorizer(client)
	server := &apiServer{
		client:  client,
		auth:    auth,
		metrics: metrics,
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
	server.holdCertificate(certificate)
	metrics.observeExpiry(keeper.expirySeconds())
	go reviewCertificate(server, keeper, metrics)

	// The cluster's client authority is read before the listener
	// starts, and again every minute. A read that fails is reported
	// and never fatal: an API that cannot read the ConfigMap still
	// answers every caller that sends a token, and the next pass
	// loads the authority once the grant or the API server is back.
	if err := server.anchors.load(client); err != nil {
		fmt.Fprintf(os.Stderr, "reading the cluster's client certificate authority: %v\n", err)
	}
	go keepClientAnchors(context.Background(), client, &server.anchors)

	if reviewsAnswer(auth) {
		server.ready.Store(true)
	}

	listener := server.listener(environmentOr(apiAddressVariable, defaultAPIAddress))
	if err := listener.ListenAndServeTLS("", ""); err != nil {
		fmt.Fprintf(os.Stderr, "serving on %s: %v\n", listener.Addr, err)
		os.Exit(1)
	}
}

// listener is the HTTPS server this role runs. It bounds the wait for
// a caller's request headers and nothing else: a capture has no length
// in advance, so a write deadline would cut every stream on schedule,
// and an idle deadline would cut a tap that is discarding up to its
// begin. The certificate comes from the keeper on every handshake, so
// a re-minted leaf serves the next connection with no restart.
func (s *apiServer) listener(address string) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           s.handler(),
		ReadHeaderTimeout: serverReadHeaderTimeout,
		TLSConfig:         s.tlsConfig(),
	}
}

// tlsConfig is what the listener handshakes with. The certificate and
// the anchors are both read on every handshake, so a re-minted leaf
// and a rotated client authority each reach the next connection with
// no restart. GetConfigForClient answers a whole configuration rather
// than one field written in place, because a tls.Config a handshake
// is reading must not be written to.
//
// VerifyClientCertIfGiven is the policy. A caller that offers no
// certificate still reaches the token, and a caller that offers one
// has it verified before any route runs. NextProtos names the two
// protocols net/http would have negotiated, because the configuration
// returned here replaces the one net/http built.
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
				ClientCAs:      s.anchors.held(),
			}, nil
		},
	}
}

// reviewsAnswer latches readiness on one answer from the API server,
// not on a review that says yes. This API's own token is minted for
// the API server's audience and not for media-api, so the review
// refuses it; what the latch proves is that a TokenReview reaches the
// API server and comes back, which is what every request needs.
func reviewsAnswer(auth *authorizer) bool {
	token, err := serviceAccountToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading this API's own token: %v\n", err)
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
	fmt.Fprintf(os.Stderr, "reviewing this API's own token: %v\n", err)
	return false
}

// reviewCertificate looks at the leaf on a clock and not on a
// request, so a quiet API still re-mints before its leaf expires and
// no request pays for a mint.
func reviewCertificate(server *apiServer, keeper *certificateKeeper, metrics *apiMetrics) {
	for range time.Tick(certificateReview) {
		certificate, err := keeper.ensure()
		if err != nil {
			fmt.Fprintf(os.Stderr, "re-minting the serving certificate: %v\n", err)
			continue
		}
		server.holdCertificate(certificate)
		metrics.observeExpiry(keeper.expirySeconds())
	}
}

func environmentOr(variable, fallback string) string {
	if value := os.Getenv(variable); value != "" {
		return value
	}
	return fallback
}

// compositionLimit reads the limit. A value that will not parse, or
// is under one, is the default rather than a failure to start,
// because the limit only bounds compositions and an API that serves
// redirects is worth more than one that refuses to start.
func compositionLimit() int {
	value, err := strconv.Atoi(os.Getenv(apiMaxCompositionsVariable))
	if err != nil || value < 1 {
		return defaultMaxCompositions
	}
	return value
}
