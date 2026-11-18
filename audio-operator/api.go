package main

// audio-api is the Deployment in liken-system that serves the capture
// API. For each request it authenticates and authorizes the caller,
// reads the Sink or Source, finds the pod of the capture container on
// the endpoint's node, and relays the stream from that pod. audio-api
// keeps no state, and only the capture container touches PipeWire.
//
// The public listener serves HTTPS, because anything on the network
// path can replay a bearer token sent over plain HTTP, and because Go
// serves HTTP/2 only over TLS.
//
// Requests through the API server's services/proxy are not supported,
// because the proxy strips the caller's identity. Version 1 of the API
// sends no CORS headers, so a browser can reach it only from the same
// origin, through a port-forward.

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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// Environment variables of this Deployment. API_ADDRESS is the address
// the public listener binds. PUBLIC_BASE is the origin that the served
// OpenAPI document names, for an ingress that puts the API under a
// different host name.
const (
	apiAddressVariable = "API_ADDRESS"
	publicBaseVariable = "PUBLIC_BASE"
)

// apiService is the name of the audio-api Service, and the common name
// on the public leaf certificate.
const apiService = "audio-api"

// certificateCheck is the interval between checks of the certificates'
// remaining life. No object changes when a certificate nears its
// expiry, so a timer is the only trigger. A leaf is renewed months
// before it expires (leafRenewalShare), so an hourly check is far more
// often than needed.
//
// Each check also reads the Secret audio-api-tls, the ConfigMap
// audio-api-ca, and the capture Secret. No watch follows the first two,
// so the check is also their only repair: a person who deletes or edits
// one sees it written again at the next check. followCertificateObjects
// watches the capture Secret and the client CA ConfigMap.
const certificateCheck = time.Hour

// apiServer is the http.Handler of the public listener, with the
// clients and caches behind it.
type apiServer struct {
	client   *apiclient.Client
	review   *reviewer
	access   *authorizer
	pods     *podIndex
	relay    *forwarder
	certs    *certificates
	anchors  *clientAnchors
	readings *apiMetrics

	// publicBase is the origin the served OpenAPI document names. When
	// it is empty, the document names the request's own origin.
	publicBase string

	// now is the clock that times each request and each stream for the
	// metrics and the log line.
	now func() time.Time

	// recorder posts the Captured Events (apievents.go).
	recorder *events.Recorder

	// log receives the line that each request writes. When it is nil,
	// the line goes to standard output. A test sets it to read the lines.
	log func(string)
}

// serveAPI runs the audio-api mode until SIGINT or SIGTERM.
func serveAPI() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		fatal("reading the in-cluster config: %v", err)
	}
	namespace := podNamespace()
	server := newAPIServer(client, namespace)
	server.recorder = events.New(ctx, client, apiComponent, events.Options{})

	// The certificates must exist before the listener starts, because
	// the listener serves the public leaf, and the capture container's
	// leaf must be in its Secret before the first capture request
	// reaches a node.
	leaf, err := server.certs.ensure()
	if err != nil {
		fatal("preparing the API's certificates: %v", err)
	}
	server.readings.certificateExpiry(server.certs.nearestExpiry())
	held := &servedLeaf{}
	held.set(leaf)
	go server.keepCertificates(ctx, held)

	// The cluster's client CA loads before the listener starts, so the
	// first handshake verifies against it. A failed load is logged and
	// not fatal, because a caller that sends a bearer token does not need
	// the CA, and the watch below loads it when the RBAC grant or the API
	// server is back.
	if err := server.anchors.load(client); err != nil {
		fmt.Fprintf(os.Stderr, "reading the cluster's client authority: %v\n", err)
	}
	watcher, err := informer.InCluster()
	if err != nil {
		fatal("reading the in-cluster config for the watches: %v", err)
	}
	server.followCertificateObjects(ctx, watcher, func(err error) {
		fmt.Fprintf(os.Stderr, "%s\n", err)
	})
	watchPods(ctx, watcher, namespace, server.pods)

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
		ReadHeaderTimeout: readHeaderTimeout,
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

func newAPIServer(client *apiclient.Client, namespace string) *apiServer {
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
	}
}

// keepCertificates mints a leaf again when it is expiring, and reports
// the nearest expiry after each check that succeeds.
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

// followCertificateObjects watches the capture Secret and the ConfigMap
// of the cluster's client CA, so a change to either takes effect before
// the next hourly check.
//
// The capture containers mount the capture Secret. A capture container
// with no valid leaf serves a certificate that no client trusts, so
// each capture request for an endpoint on its node fails with 503. On
// each change or delete, keepCaptureLeaf mints a new leaf when the
// Secret is missing, expiring, or holds a leaf that another CA signed.
// It reads the Secret from the API server and ignores the watch's copy,
// because it writes the Secret itself: a check that read the watch's
// copy before the watch delivered that write would mint a second leaf.
//
// The API never writes the client CA ConfigMap, so anchors.adopt uses
// the watch's copy, and a rotation of the CA takes effect without a
// restart. The callback converts the copy itself, so a copy that fails
// to convert is reported through complain.
//
// WatchOne calls the callback with nil when the object does not exist
// at the first read, so a capture Secret that is missing at startup is
// minted then.
func (s *apiServer) followCertificateObjects(ctx context.Context, watcher dynamic.Interface, complain func(error)) {
	leaf := informer.One{Resource: secretResource, Namespace: s.certs.namespace, Name: captureTLSSecret,
		What: "the Secret " + captureTLSSecret}
	informer.WatchOne(ctx, watcher, leaf, func(*unstructured.Unstructured) {
		if err := s.certs.keepCaptureLeaf(); err != nil {
			complain(fmt.Errorf("keeping the capture container's leaf: %w", err))
		}
	}, nil)
	authority := informer.One{Resource: configMapResource, Namespace: clientCANamespace, Name: clientCAConfigMap,
		What: "the ConfigMap " + clientCAConfigMap}
	informer.WatchOne(ctx, watcher, authority, func(held *unstructured.Unstructured) {
		var published *configMap
		if held != nil {
			converted, err := informer.Convert[configMap](held)
			if err != nil {
				complain(fmt.Errorf("reading the cluster's client authority: %w", err))
				return
			}
			published = &converted
		}
		if err := s.anchors.adopt(published); err != nil {
			complain(fmt.Errorf("reading the cluster's client authority: %w", err))
		}
	}, nil)
}

// servedLeaf is the certificate that the public listener serves. A new
// leaf replaces it atomically, so a handshake reads it without a lock
// and a mint never blocks a caller.
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

// serveAPIMetrics serves the metrics registry and the health probes on
// the metrics port until ctx ends.
func serveAPIMetrics(ctx context.Context, listener net.Listener, readings *apiMetrics, ready func() bool) {
	serving := &http.Server{
		Handler:           apiRegistryHandler(readings, ready),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	go func() {
		<-ctx.Done()
		_ = serving.Close()
	}()
	if err := serving.Serve(listener); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "the metrics listener stopped: %v\n", err)
	}
}
