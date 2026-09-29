package main

// This file holds the serving certificate over time: the API loads
// and re-mints its own, and the sidecar loads and reloads the one
// the API minted for it. Encryption comes from the certificate and
// identity from Kubernetes. The certificate keeps a ServiceAccount
// token and a picture of a room off the pod network in the clear,
// and the TokenReview says who is asking, so no client certificate
// has to exist before a capture works.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The API re-mints the leaves it signs on a twelve-hour clock, which is
// often enough for a leaf that lives a year and is re-minted with four
// months left. The sidecar reads the files the optional Secret
// volume delivers when the kubelet updates the volume (see
// watchCaptureLeaf). It also reads them once an hour, and that pass is
// a clock and not a wait for a change: it re-mints the sidecar's own
// leaf when that leaf expires, and a leaf that lives a year needs no
// finer clock.
const (
	renewalInterval = 12 * time.Hour
	reloadFallback  = time.Hour
)

// The link the kubelet moves into a Secret volume's directory each
// time it updates the volume, the first time the Secret exists
// included.
const secretVolumeData = "..data"

// The two files of a kubernetes.io/tls Secret volume that the sidecar
// reads, and the directory the manifest mounts the volume at. The
// ca.crt and ca.key that ride in the API's own Secret are keys of an
// object, not files a process opens, and certs.go names them.
const (
	captureTLSDir = "/var/run/display-capture-tls"
	tlsCertFile   = "tls.crt"
	tlsKeyFile    = "tls.key"
)

// The holder is what the TLS listener asks on every handshake, so a
// certificate that changed is served without a restart.
type certificateHolder struct {
	mu      sync.RWMutex
	pair    *tls.Certificate
	expires time.Time
	minted  bool
}

func (h *certificateHolder) set(certPEM, keyPEM []byte, minted bool) error {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("the serving certificate does not load: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("the serving certificate does not parse: %w", err)
	}
	pair.Leaf = leaf
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pair, h.expires, h.minted = &pair, leaf.NotAfter, minted
	return nil
}

func (h *certificateHolder) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.pair == nil {
		return nil, fmt.Errorf("this process holds no serving certificate yet")
	}
	return h.pair, nil
}

// held is the readiness answer. A sidecar that serves a certificate
// of its own making is not ready, because nothing verifies it: the
// API refuses that leaf and answers 503 until the minted one lands.
func (h *certificateHolder) held() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.pair != nil && !h.minted
}

func (h *certificateHolder) expiry() time.Time {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.expires
}

// The four names the Service answers on, from the bare name to the
// fully qualified one, and localhost, the name a port-forward
// reaches it by.
func serviceNames(namespace string) []string {
	return []string{
		apiServiceName,
		apiServiceName + "." + namespace,
		apiServiceName + "." + namespace + ".svc",
		apiServiceName + "." + namespace + ".svc.cluster.local",
		"localhost",
	}
}

// At its first start the API reads the Secret display-api-tls, mints
// a CA and its own leaf when the Secret is absent, signs the sidecar
// leaf with that CA, and publishes the CA certificate in the
// ConfigMap. An owner whose cert-manager owns the Secret gets none of
// the minting and all of the reloading.
func startAuthority(ctx context.Context, c *apiclient.Client, watcher dynamic.Interface, namespace string,
	holder *certificateHolder, readings *apiMetrics) (*x509.CertPool, error) {
	now := time.Now()
	material, ca, err := ensureServingMaterial(c, namespace, apiTLSSecret, serviceNames(namespace), now)
	if err != nil {
		return nil, err
	}
	if err := holder.set(material.CertPEM, material.KeyPEM, false); err != nil {
		return nil, err
	}
	readings.certificateExpires(material.Expires)

	anchor := x509.NewCertPool()
	if !anchor.AppendCertsFromPEM(material.CAPEM) {
		return nil, fmt.Errorf("the trust anchor in the Secret %s holds no certificate", apiTLSSecret)
	}
	if ca != nil {
		if err := ensureSidecarLeaf(c, namespace, sidecarTLSSecret, ca, sidecarName, now); err != nil {
			return nil, err
		}
		if err := ensureTrustAnchor(c, namespace, trustAnchorMap, material.CAPEM); err != nil {
			return nil, err
		}
	} else {
		// An owner who brought their own certificate also brings the
		// sidecar's, because this process holds no key to sign one
		// with.
		fmt.Printf("%s: the Secret %s carries no certificate authority key, so this API mints nothing\n",
			apiComponent, apiTLSSecret)
	}
	// The loop runs either way. It re-mints the leaf this API minted,
	// or it watches for the leaf an owner's cert-manager rotated, so
	// neither one waits for a restart.
	go keepCertificates(ctx, c, watcher, namespace, ca, holder, readings)
	return anchor, nil
}

// keepCertificates keeps the listener on a current leaf, in one of two
// ways.
//
// An API with a CA of its own re-mints its leaf and the sidecar's once
// less than a third of a leaf's life remains. That is a clock and not
// a wait for a change: nothing changes in the cluster when a leaf ages.
// Twice a day is often enough for a leaf that lives a year.
//
// An API that serves an owner's Secret watches that Secret, so a leaf
// the owner's cert-manager rotated reaches the listener as soon as it
// lands, with no restart.
func keepCertificates(ctx context.Context, c *apiclient.Client, watcher dynamic.Interface, namespace string,
	ca *certificateAuthority, holder *certificateHolder, readings *apiMetrics) {
	if ca == nil {
		watchNamed(ctx, watcher, secretResource, namespace, apiTLSSecret, "the Secret "+apiTLSSecret,
			func(held *Secret) { takeOwnersLeaf(held, holder, readings) })
		return
	}
	go keepSidecarLeaf(ctx, c, watcher, namespace, ca)
	tick := time.NewTicker(renewalInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			renewCertificates(c, namespace, ca, holder, readings, now)
		}
	}
}

// One pass of the renewal clock. A leaf that stands writes nothing.
func renewCertificates(c *apiclient.Client, namespace string, ca *certificateAuthority,
	holder *certificateHolder, readings *apiMetrics, now time.Time) {
	if err := ensureSidecarLeaf(c, namespace, sidecarTLSSecret, ca, sidecarName, now); err != nil {
		fmt.Fprintf(os.Stderr, "minting the sidecar certificate: %v\n", err)
	}
	material, err := renewServingMaterial(c, namespace, apiTLSSecret, ca, serviceNames(namespace), now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "renewing the serving certificate: %v\n", err)
		return
	}
	if material == nil {
		return
	}
	if err := holder.set(material.CertPEM, material.KeyPEM, false); err != nil {
		fmt.Fprintf(os.Stderr, "loading the serving certificate: %v\n", err)
		return
	}
	readings.certificateExpires(material.Expires)
}

// Serve the leaf one copy of the owner's Secret holds. A Secret that is
// gone or holds no valid leaf is reported, and the listener keeps the
// leaf it serves, which is still a leaf the owner issued.
func takeOwnersLeaf(held *Secret, holder *certificateHolder, readings *apiMetrics) {
	if held == nil {
		fmt.Fprintf(os.Stderr, "the Secret %s is gone, and the API keeps serving the certificate it holds\n", apiTLSSecret)
		return
	}
	material, _, err := readServingMaterial(held)
	if err == nil {
		err = holder.set(material.CertPEM, material.KeyPEM, false)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading the serving certificate from the Secret %s: %v\n", apiTLSSecret, err)
		return
	}
	readings.certificateExpires(material.Expires)
}

// The API watches the sidecar's Secret and mints it again when it goes
// or holds a leaf that no longer stands. Nothing else puts that Secret
// back: an owner who deletes it leaves every node's sidecar serving a
// certificate of its own making, which the API refuses to verify, so
// every capture in the cluster answers 503 until the Secret returns.
// The API's own write arrives on the watch too, and a leaf that stands
// costs one get and writes nothing.
func keepSidecarLeaf(ctx context.Context, c *apiclient.Client, watcher dynamic.Interface, namespace string, ca *certificateAuthority) {
	watchNamed(ctx, watcher, secretResource, namespace, sidecarTLSSecret, "the Secret "+sidecarTLSSecret,
		func(held *Secret) {
			if held != nil && sidecarLeafStands(held, sidecarName, time.Now()) {
				return
			}
			if err := ensureSidecarLeaf(c, namespace, sidecarTLSSecret, ca, sidecarName, time.Now()); err != nil {
				fmt.Fprintf(os.Stderr, "minting the sidecar certificate: %v\n", err)
			}
		})
}

// The sidecar's certificate arrives as an optional Secret volume the
// API fills later, so the pod starts before the API exists and the
// DaemonSet's ServiceAccount needs no grant on the Secret. Until the
// files exist the sidecar serves a self-signed leaf of its own
// making, so the liveness probe's handshake succeeds and the kubelet
// does not restart it. That leaf leaves display_capture_ready at 0,
// because the API trusts only the CA it published.
func loadCaptureLeaf(dir string, holder *certificateHolder, now time.Time) error {
	certPEM, certErr := os.ReadFile(dir + "/" + tlsCertFile)
	keyPEM, keyErr := os.ReadFile(dir + "/" + tlsKeyFile)
	if certErr == nil && keyErr == nil {
		return holder.set(certPEM, keyPEM, false)
	}
	if holder.held() {
		return fmt.Errorf("reading %s: %v", dir+"/"+tlsCertFile, certErr)
	}
	if holder.expiry().After(now) {
		return nil
	}
	ca, err := mintAuthority(now)
	if err != nil {
		return err
	}
	ownCert, ownKey, err := ca.mintLeaf(sidecarName, []string{sidecarName, "localhost"}, now)
	if err != nil {
		return err
	}
	return holder.set(ownCert, ownKey, true)
}

// The sidecar reads the files each time the kubelet updates the
// volume (arrivals.go). The kubelet writes a Secret volume's files
// into a new directory and then renames a link over ..data, so that
// rename says the files changed. On the volume's first write the
// kubelet makes each key's link, such as tls.crt, after the rename, so
// the watch wakes on those links too, and the read after the last one
// finds both files. The
// fallback timer is the clock that re-mints the sidecar's own leaf
// when that leaf expires.
func watchCaptureLeaf(ctx context.Context, dir string, holder *certificateHolder, readings *captureMetrics, fallback time.Duration) {
	watch := newArrivalsIn(dir, secretVolumeData, tlsCertFile, tlsKeyFile)
	defer watch.close()
	for {
		watch.ready()
		if err := loadCaptureLeaf(dir, holder, time.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "loading the capture certificate: %v\n", err)
		}
		readings.ready(holder.held())
		watch.wait(ctx, fallback)
		if ctx.Err() != nil {
			return
		}
	}
}
