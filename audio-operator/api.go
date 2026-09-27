package main

// audio-api: the Deployment in liken-system that identifies the
// target of a request and forwards the stream.
//
// It authenticates, authorizes, reads the object, finds the node's
// pod, and relays. Every fact about PipeWire is in the capture
// container. Nothing is stored on either side.
//
// The public leg is HTTPS because a bearer token on a plain listener
// is replayable by anything on the path, and because Go speaks HTTP/2
// only over TLS. Over HTTP/1.1 a stream is chunked with no
// Content-Length (RFC 9112 sections 7.1 and 6.2); over HTTP/2 it is
// DATA frames (RFC 9113 section 8.1).
//
// The API server's services/proxy door strips the caller's identity
// and is not supported. v1 has no CORS: a browser reaches this API
// only on the same origin through a port-forward.

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

// The settings this Deployment reads from its environment: where the
// public listener binds, and the origin the served OpenAPI document
// names when an ingress puts the API behind another host name.
const (
	apiAddressVariable = "API_ADDRESS"
	publicBaseVariable = "PUBLIC_BASE"
)

// apiService is the Service's own name, which is also the common name
// on the public leaf.
const apiService = "audio-api"

// certificateCheck is how often the API looks at the lives of the
// certificates it holds and mints a leaf again when one is nearing its
// end. No object changes when a certificate nears its end, so this is
// a clock and not a watch. A year of life needs no closer look than
// this.
//
// The pass reads the three objects again, which costs three gets an
// hour. That read is also the backstop for the Secret audio-api-tls
// and the ConfigMap audio-api-ca, which no watch follows: a person
// who deletes or edits one sees it written back on the next pass. The
// capture Secret and the client authority have watches of their own.
const certificateCheck = time.Hour

// apiServer is the whole of this mode's state.
type apiServer struct {
	client   *Client
	review   *reviewer
	access   *authorizer
	pods     *podIndex
	relay    *forwarder
	certs    *certificates
	anchors  *clientAnchors
	readings *apiMetrics

	// publicBase is the origin the served OpenAPI document names. An
	// empty value means the request's own origin.
	publicBase string

	// now is a field so a test reads a fixed accept instant off a save
	// name instead of a moving one.
	now func() time.Time

	// event writes the Captured record. A field so a test reads what
	// was written with no API server behind it.
	event func(kind, name, uid, aspect, format, who string, at time.Time) error

	// log is where the one line per request goes. It is a field for
	// the same reason.
	log func(string)
}

// serveAPI is the mode's entry point.
func serveAPI() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := InClusterClient()
	if err != nil {
		fatal("in-cluster config: %v", err)
	}
	namespace := podNamespace()
	server := newAPIServer(client, namespace)

	// The certificates come before the listener: the API serves the
	// public leaf, and the container's leaf has to exist before the
	// first tap reaches a node.
	leaf, err := server.certs.ensure()
	if err != nil {
		fatal("preparing the API's certificates: %v", err)
	}
	server.readings.certificateExpiry(server.certs.nearestExpiry())
	held := &servedLeaf{}
	held.set(leaf)
	go server.keepCertificates(ctx, held)

	// The cluster's client authority is read before the listener
	// starts, so the first handshake verifies against it, and again on
	// every change the watch reports. A read that fails is reported and
	// never fatal: an API that cannot read the ConfigMap still answers
	// every caller that sends a Bearer token, and the watch loads the
	// authority once the grant or the API server is back.
	if err := server.anchors.load(client); err != nil {
		fmt.Fprintf(os.Stderr, "reading the cluster's client authority: %v\n", err)
	}
	server.followCertificateObjects(ctx, func(err error) {
		fmt.Fprintf(os.Stderr, "%s\n", err)
	})

	watchPods(ctx, client, namespace, server.pods, func(err error) {
		fmt.Fprintf(os.Stderr, "%s\n", err)
	})

	if address := os.Getenv("METRICS_ADDRESS"); address != "" {
		metrics, err := net.Listen("tcp", address)
		if err != nil {
			fatal("listening for metrics: %v", err)
		}
		fmt.Printf("%s: serving metrics on %s\n", apiComponent, metrics.Addr())
		go serveAPIMetrics(ctx, metrics, server.readings, held.ready)
	}

	address := os.Getenv(apiAddressVariable)
	if address == "" {
		address = ":8443"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fatal("listening for requests on %s: %v", address, err)
	}
	fmt.Printf("%s: serving the capture API on %s\n", apiComponent, listener.Addr())

	serving := &http.Server{
		Handler:           server,
		ReadHeaderTimeout: metricsDeadline,
		TLSConfig:         apiTLSConfig(held, server.anchors),
	}
	go func() {
		<-ctx.Done()
		_ = serving.Close()
	}()
	if err := serving.ServeTLS(listener, "", ""); err != nil && ctx.Err() == nil {
		fatal("the capture API stopped: %v", err)
	}
}

func newAPIServer(client *Client, namespace string) *apiServer {
	certs := newCertificates(client, namespace, apiService)
	return &apiServer{
		client:     client,
		review:     newReviewer(client, apiAudience),
		access:     newAuthorizer(client),
		pods:       newPodIndex(),
		relay:      newForwarder(certs.anchor),
		certs:      certs,
		anchors:    &clientAnchors{},
		readings:   newAPIMetrics(version),
		publicBase: os.Getenv(publicBaseVariable),
		now:        time.Now,
		event: func(kind, name, uid, aspect, format, who string, at time.Time) error {
			return recordCapture(client, kind, name, uid, aspect, format, who, at)
		},
	}
}

// keepCertificates mints a leaf again when under a third of its life
// remains, and reports the nearest expiry on every pass.
func (s *apiServer) keepCertificates(ctx context.Context, held *servedLeaf) {
	lifetimes := time.NewTicker(certificateCheck)
	defer lifetimes.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-lifetimes.C:
		}
		leaf, err := s.certs.ensure()
		if err != nil {
			fmt.Fprintf(os.Stderr, "keeping the API's certificates: %v\n", err)
			continue
		}
		held.set(leaf)
		s.readings.certificateExpiry(s.certs.nearestExpiry())
	}
}

// followCertificateObjects watches the two objects whose changes the
// API acts on at once, not on the hourly pass.
//
// The first is the Secret the capture containers mount. A capture
// container with no leaf serves a certificate no client trusts, so
// every tap on its node is a 503 until the Secret is back. A change or
// a delete of the Secret runs the capture leaf check, which mints the
// leaf again when the Secret is gone, is expiring, or holds a leaf
// another CA signed, and writes nothing when the Secret is in order.
//
// The second is the ConfigMap that holds the cluster's client
// certificate authority, so a rotation of that authority takes effect
// with no restart.
func (s *apiServer) followCertificateObjects(ctx context.Context, complain func(error)) {
	followObject(ctx, s.client, "Secret", secretsPath(s.certs.namespace), captureTLSSecret,
		func() {
			if err := s.certs.keepCaptureLeaf(); err != nil {
				complain(fmt.Errorf("keeping the capture container's leaf: %w", err))
			}
		}, complain)
	followObject(ctx, s.client, "ConfigMap", configMapsPath(clientCANamespace), clientCAConfigMap,
		func() {
			if err := s.anchors.load(s.client); err != nil {
				complain(fmt.Errorf("reading the cluster's client authority: %w", err))
			}
		}, complain)
}

// servedLeaf is the certificate the public listener serves, swapped in
// place when the API mints a new one. A handshake reads it without a
// lock, so a mint never blocks a caller.
type servedLeaf struct {
	held atomic.Pointer[tls.Certificate]
}

func (l *servedLeaf) set(pair tls.Certificate) { l.held.Store(&pair) }

func (l *servedLeaf) certificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	if held := l.held.Load(); held != nil {
		return held, nil
	}
	return nil, ErrNoCertificate
}

func (l *servedLeaf) ready() bool { return l.held.Load() != nil }

// serveAPIMetrics answers the registry and the two probes on the
// metrics port until the run ends.
func serveAPIMetrics(ctx context.Context, listener net.Listener, readings *apiMetrics, ready func() bool) {
	serving := &http.Server{
		Handler:           apiRegistryHandler(readings, ready),
		ReadHeaderTimeout: metricsDeadline,
	}
	go func() {
		<-ctx.Done()
		_ = serving.Close()
	}()
	if err := serving.Serve(listener); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "the metrics listener stopped: %v\n", err)
	}
}
