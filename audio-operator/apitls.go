package main

// The capture API mints its own certificate authority (CA). The
// kubernetes.io/tls Secret audio-api-tls holds the CA certificate and
// key, and the leaf that audio-api serves. The ConfigMap audio-api-ca
// holds a copy of the CA certificate, so a client can read the trust
// anchor without access to the Secret. The same CA signs the capture
// container's leaf, which has the SAN audio-capture and is stored in
// the Secret audio-capture-server. The DaemonSet mounts that Secret as
// an optional volume.
//
// The API mints a leaf again before it expires (leafRenewalShare), and
// audio_api_certificate_expiry_seconds reports the nearest expiry.
//
// Every key the API mints is ECDSA on the P-256 curve, stored as PKCS#8
// under the PEM type "PRIVATE KEY", because P-256 is the default for a
// new TLS certificate and generates much faster than an RSA-2048 key.
// The API still reads RSA keys, in PKCS#1 or PKCS#8, because a Secret
// from an earlier release can hold one. An RSA CA stays in use until it
// is rotated, and it signs ECDSA leaves. The API serves an RSA leaf
// until the leaf is due for renewal, and then mints an ECDSA leaf in
// its place. A Secret that cert-manager issues can hold any key
// type that crypto/tls reads, and the API serves it unchanged.
//
// Two API pods that run at once must use one CA, or no single CA
// certificate verifies the leaves of both. The Deployment's Recreate
// strategy keeps a rollout from running two pods at once. When two pods
// still create the Secret at once, the create that loses reads the
// winner's Secret.
//
// To rotate the CA by hand, append the new CA to ca.crt in the
// ConfigMap, wait until every client has read it, and then write the
// new CA's certificate and key into ca.crt and ca.key of the Secret
// audio-api-tls. The API then mints a capture leaf from the new CA, and
// mints its own leaf from it when the old leaf is due for renewal. If
// you use cert-manager, replace audio-api-tls with a Certificate of the
// same name.

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// The objects that the API owns. The Role in deploy/api.yaml names
// each of them, so a rename here needs the same rename there.
const (
	apiTLSSecret     = "audio-api-tls"
	apiCAConfigMap   = "audio-api-ca"
	captureTLSSecret = "audio-capture-server"
)

// The lifetimes of the certificates. The API mints a leaf again when
// less than 1/leafRenewalShare of its life remains.
const (
	caLifetime       = 10 * 365 * 24 * time.Hour
	leafLifetime     = 365 * 24 * time.Hour
	leafRenewalShare = 3
)

// keyPair is one certificate and its key, PEM-encoded as a
// kubernetes.io/tls Secret stores them.
type keyPair struct {
	Certificate []byte
	Key         []byte
}

// authority is the CA that signs the capture API's leaves.
type authority struct {
	Pair        keyPair
	certificate *x509.Certificate
	key         crypto.Signer
}

func mintAuthority(now time.Time) (authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return authority{}, fmt.Errorf("making the certificate authority's key: %w", err)
	}
	keyPEM, err := encodeKey(key)
	if err != nil {
		return authority{}, err
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
		Pair:        keyPair{Certificate: encodeCertificate(body), Key: keyPEM},
		certificate: certificate,
		key:         key,
	}, nil
}

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

// mintLeaf signs a server certificate that lists names as DNS SANs.
// The key usage is digital signature only, because an ECDSA key signs
// the TLS handshake and does not encrypt a key exchange. The CA's key
// type sets the signature algorithm: ECDSA with SHA-256 from an ECDSA
// CA, and RSA with SHA-256 from an RSA CA.
func (a authority) mintLeaf(commonName string, names []string, now time.Time) (keyPair, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
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
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     names,
	}
	body, err := x509.CreateCertificate(rand.Reader, template, a.certificate, &key.PublicKey, a.key)
	if err != nil {
		return keyPair{}, fmt.Errorf("signing the leaf for %s: %w", commonName, err)
	}
	keyPEM, err := encodeKey(key)
	if err != nil {
		return keyPair{}, err
	}
	return keyPair{Certificate: encodeCertificate(body), Key: keyPEM}, nil
}

// selfSigned returns a certificate for name, signed by a CA that it
// mints and discards. The capture listener serves it until the
// audio-capture-server Secret exists, so /healthz responds over HTTPS
// and the kubelet does not restart the container for a failed liveness
// probe. No client trusts this certificate, because the API verifies
// against its own CA, so a tap fails with a 503 and
// audio_capture_ready stays 0 until the Secret arrives.
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

// expiring reports whether a leaf is due for renewal. A certificate
// that does not parse counts as expiring.
func expiring(pair keyPair, now time.Time) bool {
	certificate, err := parseCertificate(pair.Certificate)
	if err != nil {
		return true
	}
	life := certificate.NotAfter.Sub(certificate.NotBefore)
	return certificate.NotAfter.Sub(now) < life/leafRenewalShare
}

func expiryOf(pair keyPair) (time.Time, error) {
	certificate, err := parseCertificate(pair.Certificate)
	if err != nil {
		return time.Time{}, err
	}
	return certificate.NotAfter, nil
}

// serviceNames returns the names that a client in the cluster can use
// to reach the Service, which the public leaf lists as DNS SANs.
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

func encodeKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encoding the key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func parseCertificate(body []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, errors.New("the certificate is not PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

// parseKey reads a CA key in any form that a Secret can hold: PKCS#8,
// which this code writes, PKCS#1 "RSA PRIVATE KEY" from an earlier
// release, and SEC1 "EC PRIVATE KEY" from a CA made with openssl.
func parseKey(body []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, errors.New("the key is not PEM")
	}
	var parsed any
	var err error
	switch block.Type {
	case "RSA PRIVATE KEY":
		parsed, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		parsed, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	}
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("the key is %T, which cannot sign", parsed)
	}
	return key, nil
}
