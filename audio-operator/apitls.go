package main

// Minting the domain's one certificate authority and the two leaves
// it signs.
//
// The CA and the public leaf are in the Secret audio-api-tls, in the
// kubernetes.io/tls shape. The CA certificate alone goes in the
// ConfigMap audio-api-ca, so a client reads the trust anchor with an
// ordinary get and never touches the Secret. The capture container's
// leaf, signed by the same CA with the SAN audio-capture, goes in the
// Secret audio-capture-server, which the DaemonSet mounts as an
// optional volume.
//
// The CA is valid for ten years and a leaf for one, and the API mints a
// leaf again when under a third of its life remains.
// audio_api_certificate_expiry_seconds reports the nearest expiry.
//
// A create that loses the race reads the winner, so two API pods
// during a rollout settle on one CA. The Deployment is strategy
// Recreate for the same reason.
//
// Rotation of the CA is two steps: publish the new CA appended to
// ca.crt in the ConfigMap, wait for every client to read it, then
// switch the leaves. A cert-manager owner replaces audio-api-tls with
// a Certificate of the same name.

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// The three objects this API owns. The Role in deploy/api.yaml names
// them by these exact names, so a rename here is a rename there.
const (
	apiTLSSecret     = "audio-api-tls"
	apiCAConfigMap   = "audio-api-ca"
	captureTLSSecret = "audio-capture-server"
)

// The lifetimes, and the share of a leaf's life below which the API
// mints it again: a leaf with under a third of its year left is
// replaced.
const (
	caLifetime       = 10 * 365 * 24 * time.Hour
	leafLifetime     = 365 * 24 * time.Hour
	leafRenewalShare = 3
)

// certificateBits is the RSA key size. 2048 bits is enough for a CA
// that never leaves the cluster, and every client in this cluster
// reads it.
const certificateBits = 2048

// keyPair is one certificate and its key, in the PEM the Kubernetes
// tls shape carries.
type keyPair struct {
	Certificate []byte
	Key         []byte
}

// authority is the CA this domain signs with.
type authority struct {
	Pair        keyPair
	certificate *x509.Certificate
	key         *rsa.PrivateKey
}

// mintAuthority makes the domain's CA.
func mintAuthority(now time.Time) (authority, error) {
	key, err := rsa.GenerateKey(rand.Reader, certificateBits)
	if err != nil {
		return authority{}, fmt.Errorf("making the certificate authority's key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return authority{}, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: DriverName + " capture CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	body, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return authority{}, fmt.Errorf("signing the certificate authority: %w", err)
	}
	certificate, err := x509.ParseCertificate(body)
	if err != nil {
		return authority{}, err
	}
	return authority{
		Pair:        keyPair{Certificate: encodeCertificate(body), Key: encodeKey(key)},
		certificate: certificate,
		key:         key,
	}, nil
}

// readAuthority reads a CA back out of the PEM a Secret holds.
func readAuthority(pair keyPair) (authority, error) {
	certificate, err := parseCertificate(pair.Certificate)
	if err != nil {
		return authority{}, err
	}
	key, err := parseKey(pair.Key)
	if err != nil {
		return authority{}, err
	}
	return authority{Pair: pair, certificate: certificate, key: key}, nil
}

// mintLeaf signs one server certificate for the names given.
func (a authority) mintLeaf(commonName string, names []string, now time.Time) (keyPair, error) {
	key, err := rsa.GenerateKey(rand.Reader, certificateBits)
	if err != nil {
		return keyPair{}, fmt.Errorf("making the leaf's key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return keyPair{}, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(leafLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     names,
	}
	body, err := x509.CreateCertificate(rand.Reader, template, a.certificate, &key.PublicKey, a.key)
	if err != nil {
		return keyPair{}, fmt.Errorf("signing the leaf for %s: %w", commonName, err)
	}
	return keyPair{Certificate: encodeCertificate(body), Key: encodeKey(key)}, nil
}

// selfSigned makes a certificate of this process's own, signed by a
// CA it mints and discards.
//
// The capture listener serves it while the Secret the API mints is
// absent, so /healthz answers and the liveness probe passes rather
// than restarting the container every three minutes. No client in
// this cluster trusts it: the API anchors on the domain's CA, so a
// tap against this certificate fails verification and is a 503, and
// audio_capture_ready stays 0.
func selfSigned(name string, now time.Time) (tls.Certificate, error) {
	authority, err := mintAuthority(now)
	if err != nil {
		return tls.Certificate{}, err
	}
	pair, err := authority.mintLeaf(name, []string{name}, now)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(pair.Certificate, pair.Key)
}

// expiring says whether a leaf has less than a third of its life left,
// which is when the API mints it again.
func expiring(pair keyPair, now time.Time) bool {
	certificate, err := parseCertificate(pair.Certificate)
	if err != nil {
		return true
	}
	life := certificate.NotAfter.Sub(certificate.NotBefore)
	return certificate.NotAfter.Sub(now) < life/leafRenewalShare
}

// expiryOf reports when a certificate runs out, which is what the
// gauge carries.
func expiryOf(pair keyPair) (time.Time, error) {
	certificate, err := parseCertificate(pair.Certificate)
	if err != nil {
		return time.Time{}, err
	}
	return certificate.NotAfter, nil
}

// serviceNames are the DNS names the public leaf carries: the four
// spellings a client in this cluster reaches the Service by.
func serviceNames(service, namespace string) []string {
	return []string{
		service,
		service + "." + namespace,
		service + "." + namespace + ".svc",
		service + "." + namespace + ".svc.cluster.local",
	}
}

func newSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("making a serial number: %w", err)
	}
	return serial, nil
}

func encodeCertificate(body []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: body})
}

func encodeKey(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func parseCertificate(body []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, errors.New("the certificate is not PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseKey(body []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, errors.New("the key is not PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the key is not an RSA key")
	}
	return key, nil
}
