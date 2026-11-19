package main

// This file keeps the TLS serving certificates current. The API serves
// its own certificate and mints it again before it expires, and the
// sidecar reloads the certificate that the API minted for it.
//
// TLS keeps ServiceAccount tokens and captured screen images from
// crossing the pod network in plaintext. The API authenticates clients
// with a TokenReview of their token, so a capture needs no client
// certificate.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// renewalInterval is how often the API checks whether a leaf it signed
// needs renewal. A leaf lasts a year and is renewed when less than a
// third of its life remains, so two checks a day are plenty.
//
// reloadFallback is the longest the sidecar goes between reads of its
// certificate files. A change to the files wakes it sooner (see
// watchCaptureLeaf). The timer is for the case where the files are
// still missing: the sidecar then serves a self-signed leaf, and the
// timer lets it mint a new one when that leaf expires.
const (
	renewalInterval = 12 * time.Hour
	reloadFallback  = time.Hour
)

// secretVolumeData is the symlink that the kubelet replaces with a
// rename each time it writes a Secret volume, the first write included.
const secretVolumeData = "..data"

// captureTLSDir is where the sidecar's manifest mounts its
// kubernetes.io/tls Secret volume.
const (
	captureTLSDir = "/var/run/display-capture-tls"
	tlsCertFile   = "tls.crt"
	tlsKeyFile    = "tls.key"
)

// certificateHolder is the certificate that the TLS listener serves.
// The listener calls get on every handshake, so a certificate that set
// replaces is served without a restart.
type certificateHolder struct {
	mu         sync.RWMutex
	pair       *tls.Certificate
	expires    time.Time
	selfSigned bool
}

func (h *certificateHolder) set(certPEM, keyPEM []byte, selfSigned bool) error {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("loading the serving key pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("parsing the serving certificate: %w", err)
	}
	pair.Leaf = leaf
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pair, h.expires, h.selfSigned = &pair, leaf.NotAfter, selfSigned
	return nil
}

func (h *certificateHolder) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.pair == nil {
		return nil, errors.New("the process has no serving certificate yet")
	}
	return h.pair, nil
}

// verifiable reports whether the process serves a certificate that the API
// can verify. A self-signed leaf does not count, because the API does
// not trust it and responds 503 to a capture through that sidecar.
func (h *certificateHolder) verifiable() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.pair != nil && !h.selfSigned
}

func (h *certificateHolder) expiry() time.Time {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.expires
}

// serviceNames returns the DNS names of the API's Service, and
// localhost for a client that connects through a port-forward.
func serviceNames(namespace string) []string {
	return []string{
		apiServiceName,
		apiServiceName + "." + namespace,
		apiServiceName + "." + namespace + ".svc",
		apiServiceName + "." + namespace + ".svc.cluster.local",
		"localhost",
	}
}

// startAuthority loads the API's serving certificate from the Secret
// display-api-tls and returns the CA pool that verifies the sidecars.
// When the Secret does not exist, it mints a CA and the API's leaf,
// signs the sidecar's leaf with that CA, and publishes the CA
// certificate in a ConfigMap. When the Secret holds a certificate with
// no CA key, such as one that cert-manager issued, the API mints
// nothing and reloads that certificate each time the Secret changes.
func startAuthority(ctx context.Context, c *apiclient.Client, watcher dynamic.Interface, namespace string,
	holder *certificateHolder, metrics *apiMetrics) (*x509.CertPool, error) {
	now := time.Now()
	material, ca, err := ensureServingMaterial(c, namespace, apiTLSSecret, serviceNames(namespace), now)
	if err != nil {
		return nil, err
	}
	if err := holder.set(material.CertPEM, material.KeyPEM, false); err != nil {
		return nil, err
	}
	metrics.certificateExpires(material.Expires)

	anchor := x509.NewCertPool()
	if !anchor.AppendCertsFromPEM(material.CAPEM) {
		return nil, fmt.Errorf("the trust anchor in the Secret %s has no PEM certificate", apiTLSSecret)
	}
	if ca != nil {
		if err := ensureSidecarLeaf(c, namespace, sidecarTLSSecret, ca, sidecarName, now); err != nil {
			return nil, err
		}
		if err := ensureTrustAnchor(c, namespace, trustAnchorMap, material.CAPEM); err != nil {
			return nil, err
		}
	} else {
		// The API has no CA key to sign the sidecar's leaf with, so
		// whoever supplied this certificate supplies the sidecar's too.
		fmt.Printf("%s: the Secret %s has no certificate authority key, so this API does not mint certificates\n",
			apiComponent, apiTLSSecret)
	}
	go keepCertificates(ctx, c, watcher, namespace, ca, holder, metrics)
	return anchor, nil
}

// keepCertificates keeps the listener on a current leaf. With the CA
// key, the API renews its leaf and the sidecar's on a timer, because
// nothing in the cluster changes when a leaf ages. Without the CA key,
// it watches the Secret, so a leaf that the Secret's owner rotates is
// served as soon as it is written.
func keepCertificates(ctx context.Context, c *apiclient.Client, watcher dynamic.Interface, namespace string,
	ca *certificateAuthority, holder *certificateHolder, metrics *apiMetrics) {
	if ca == nil {
		ownerSecret := informer.One{Resource: secretResource, Namespace: namespace, Name: apiTLSSecret, What: "the Secret " + apiTLSSecret}
		<-informer.WatchOne(ctx, watcher, ownerSecret, func(secret *Secret) { loadOwnersLeaf(secret, holder, metrics) }, nil).Done()
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
			renewCertificates(c, namespace, ca, holder, metrics, now)
		}
	}
}

// renewCertificates mints the sidecar's leaf and the API's leaf again
// when each is due for renewal.
func renewCertificates(c *apiclient.Client, namespace string, ca *certificateAuthority,
	holder *certificateHolder, metrics *apiMetrics, now time.Time) {
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
	metrics.certificateExpires(material.Expires)
}

// loadOwnersLeaf serves the leaf in the Secret that the API does not
// own. When the Secret is deleted or holds no valid leaf, it logs the
// problem and the listener keeps serving the last good leaf.
func loadOwnersLeaf(secret *Secret, holder *certificateHolder, metrics *apiMetrics) {
	if secret == nil {
		fmt.Fprintf(os.Stderr, "the Secret %s was deleted, and the API keeps serving the certificate it already loaded\n", apiTLSSecret)
		return
	}
	material, _, err := readServingMaterial(secret)
	if err == nil {
		err = holder.set(material.CertPEM, material.KeyPEM, false)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading the serving certificate from the Secret %s: %v\n", apiTLSSecret, err)
		return
	}
	metrics.certificateExpires(material.Expires)
}

// keepSidecarLeaf watches the sidecar's Secret and writes it again
// when it is deleted or its leaf is invalid or due for renewal. Nothing
// else restores it, and while it is missing every capture in the
// cluster fails with a 503. The API's own writes arrive on the watch
// too, so the handler skips a leaf that is still good.
func keepSidecarLeaf(ctx context.Context, c *apiclient.Client, watcher dynamic.Interface, namespace string, ca *certificateAuthority) {
	sidecarSecret := informer.One{Resource: secretResource, Namespace: namespace, Name: sidecarTLSSecret, What: "the Secret " + sidecarTLSSecret}
	<-informer.WatchOne(ctx, watcher, sidecarSecret, func(secret *Secret) {
		if secret != nil && sidecarLeafUsable(secret, ca, sidecarName, time.Now()) {
			return
		}
		if err := ensureSidecarLeaf(c, namespace, sidecarTLSSecret, ca, sidecarName, time.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "minting the sidecar certificate: %v\n", err)
		}
	}, nil).Done()
}

// readFailures returns the error of each read that failed, on one
// line, because the log prints the error as one line.
func readFailures(certErr, keyErr error) error {
	switch {
	case certErr != nil && keyErr != nil:
		return fmt.Errorf("%w, and %w", certErr, keyErr)
	case certErr != nil:
		return certErr
	default:
		return keyErr
	}
}

// loadCaptureLeaf loads the sidecar's certificate from the files in
// dir. The files come from an optional Secret volume, which the API can
// create after the pod starts, so the DaemonSet's ServiceAccount needs
// no permission on the Secret.
//
// While the files are missing, the sidecar serves a self-signed leaf,
// so the liveness probe's TLS handshake succeeds and the kubelet does
// not restart the pod. display_capture_ready stays 0 until the API's
// leaf loads. When the files go missing after the API's leaf has
// loaded, the sidecar keeps serving that leaf and returns the read
// error.
func loadCaptureLeaf(dir string, holder *certificateHolder, now time.Time) error {
	certPEM, certErr := os.ReadFile(dir + "/" + tlsCertFile)
	keyPEM, keyErr := os.ReadFile(dir + "/" + tlsKeyFile)
	if certErr == nil && keyErr == nil {
		return holder.set(certPEM, keyPEM, false)
	}
	if holder.verifiable() {
		// Either file can fail alone, so the error names each read
		// that failed.
		return fmt.Errorf("reloading the API's leaf, which stays in use: %w", readFailures(certErr, keyErr))
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

// watchCaptureLeaf loads the sidecar's certificate files, then loads
// them again each time they change. The kubelet writes a Secret
// volume's files into a new directory and renames a symlink over
// ..data, so the watch wakes on that rename. On the first write the
// kubelet creates the tls.crt and tls.key links after the rename, so
// the watch also wakes on those names. With no change, it loads again
// after fallback.
func watchCaptureLeaf(ctx context.Context, dir string, holder *certificateHolder, metrics *captureMetrics, fallback time.Duration) {
	watch := newArrivalsIn(dir, secretVolumeData, tlsCertFile, tlsKeyFile)
	defer watch.close()
	for {
		watch.ready()
		if err := loadCaptureLeaf(dir, holder, time.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "loading the capture certificate: %v\n", err)
		}
		metrics.ready(holder.verifiable())
		watch.wait(ctx, fallback)
		if ctx.Err() != nil {
			return
		}
	}
}
