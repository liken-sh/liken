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
)

// The API looks at its own certificate twice a day, which is often
// enough for a leaf that lives a year and is re-minted with four
// months left. The sidecar looks for the files the optional Secret
// volume delivers every fifteen seconds, because the volume changes
// with no event a process can wait on, and a fresh install waits
// this long at most for its first leaf.
const (
	renewalInterval = 12 * time.Hour
	reloadInterval  = 15 * time.Second
)

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
func startAuthority(ctx context.Context, c *Client, namespace string,
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
	// and it takes up the leaf an owner's cert-manager rotated, so
	// neither one waits for a restart.
	go keepCertificates(ctx, c, namespace, ca, holder, readings)
	return anchor, nil
}

// Twice a day the API re-mints a leaf that has less than a third of
// its life left, or takes up a leaf an owner rotated, and sets the
// expiry gauge to the date a person reads on a dashboard.
func keepCertificates(ctx context.Context, c *Client, namespace string,
	ca *certificateAuthority, holder *certificateHolder, readings *apiMetrics) {
	tick := time.NewTicker(renewalInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			material, err := servingMaterialNow(c, namespace, ca, now)
			if err != nil {
				fmt.Fprintf(os.Stderr, "reading the serving certificate: %v\n", err)
				continue
			}
			if material == nil {
				continue
			}
			if err := holder.set(material.CertPEM, material.KeyPEM, false); err != nil {
				fmt.Fprintf(os.Stderr, "loading the serving certificate: %v\n", err)
				continue
			}
			readings.certificateExpires(material.Expires)
			if ca == nil {
				continue
			}
			if err := ensureSidecarLeaf(c, namespace, sidecarTLSSecret, ca, sidecarName, now); err != nil {
				fmt.Fprintf(os.Stderr, "re-minting the sidecar certificate: %v\n", err)
			}
		}
	}
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

// The sidecar watches the files on a tick, because a Secret volume
// changes with no event a process can wait on: the kubelet swaps a
// symlink under the directory.
func watchCaptureLeaf(ctx context.Context, dir string, holder *certificateHolder, readings *captureMetrics) {
	tick := time.NewTicker(reloadInterval)
	defer tick.Stop()
	for {
		if err := loadCaptureLeaf(dir, holder, time.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "loading the capture certificate: %v\n", err)
		}
		readings.ready(holder.held())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// One pass takes one of two shapes. With a CA of its own, the API
// re-mints the leaf it signed once less than a third of its life
// remains, and answers nil while it stands. With an owner's Secret,
// it re-reads the leaf every pass, which is the only way a leaf
// cert-manager rotated reaches the listener without a restart.
func servingMaterialNow(c *Client, namespace string, ca *certificateAuthority, now time.Time) (*servingMaterial, error) {
	if ca != nil {
		return renewServingMaterial(c, namespace, apiTLSSecret, ca, serviceNames(namespace), now)
	}
	material, _, err := ensureServingMaterial(c, namespace, apiTLSSecret, serviceNames(namespace), now)
	return material, err
}
