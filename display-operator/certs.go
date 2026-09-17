package main

// This file mints the one CA the display domain serves under and the
// two leaves it signs: the API's own, for the Service's names, and
// the sidecar's, for the name display-capture. The API mints its own
// material at first start, so an install works with no cert-manager
// and no step before the first request. An owner who runs
// cert-manager writes the Secret display-api-tls instead, and the API
// serves what it holds and mints nothing.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"time"
)

// The CA lives ten years and a leaf lives one. A leaf is re-minted
// once less than a third of its life remains, so a renewal has months
// of margin and a pod that was down for a while still comes up with
// a leaf that stands.
const (
	authorityLife = 10 * 365 * 24 * time.Hour
	leafLife      = 365 * 24 * time.Hour
)

// The one SAN on the sidecar's leaf. The two leaves are told apart by
// their SANs, and the API verifies a sidecar under this name because
// a pod IP is not a name a certificate can carry across restarts.
const sidecarName = captureAudience

// The Secret type Kubernetes defines for a serving certificate,
// which requires tls.crt and tls.key. The API adds ca.crt beside
// them, and ca.key in its own Secret alone, so a restart signs with
// the CA it minted.
const (
	tlsSecretType = "kubernetes.io/tls"
	tlsCertKey    = "tls.crt"
	tlsKeyKey     = "tls.key"
	caCertKey     = "ca.crt"
	caKeyKey      = "ca.key"
)

// Secret and ConfigMap hold only the fields this program reads and
// writes, the way displays.go holds Display.
type objectMeta struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// A Secret's data is base64 on the wire, which is what a []byte
// marshals to, so the PEM goes in and comes out as bytes.
type Secret struct {
	APIVersion string            `json:"apiVersion,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Metadata   objectMeta        `json:"metadata"`
	Type       string            `json:"type,omitempty"`
	Data       map[string][]byte `json:"data,omitempty"`
}

// A ConfigMap's data is plain text, so a client reads the trust
// anchor with an ordinary get and never touches a Secret.
type ConfigMap struct {
	APIVersion string            `json:"apiVersion,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Metadata   objectMeta        `json:"metadata"`
	Data       map[string]string `json:"data,omitempty"`
}

func secretsPath(namespace string) string {
	return "/api/v1/namespaces/" + namespace + "/secrets"
}

func configMapsPath(namespace string) string {
	return "/api/v1/namespaces/" + namespace + "/configmaps"
}

// The authority holds the parsed certificate and key the API signs
// with, and the PEM of both, which is what the Secret carries.
type certificateAuthority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	certPEM     []byte
	keyPEM      []byte
}

// The CA is minted once, at the API's first start, and lives ten
// years.
func mintAuthority(now time.Time) (*certificateAuthority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating the authority's key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: apiAudience + "-ca"},
		NotBefore:             now,
		NotAfter:              now.Add(authorityLife),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, fmt.Errorf("signing the authority: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("reading the authority back: %w", err)
	}
	keyPEM, err := privateKeyPEM(key)
	if err != nil {
		return nil, err
	}
	return &certificateAuthority{
		certificate: certificate,
		key:         key,
		certPEM:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		keyPEM:      keyPEM,
	}, nil
}

// A leaf is a serving certificate for the names it is given, and it
// lives one year.
func (ca *certificateAuthority) mintLeaf(commonName string, dnsNames []string, now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating the leaf's key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		DNSNames:              dnsNames,
		NotBefore:             now,
		NotAfter:              now.Add(leafLife),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.certificate, key.Public(), ca.key)
	if err != nil {
		return nil, nil, fmt.Errorf("signing the leaf: %w", err)
	}
	keyPEM, err = privateKeyPEM(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), keyPEM, nil
}

// needsRenewal reports whether a leaf has less than a third of its
// life left.
func needsRenewal(leaf *x509.Certificate, now time.Time) bool {
	life := leaf.NotAfter.Sub(leaf.NotBefore)
	return 3*leaf.NotAfter.Sub(now) < life
}

// RFC 5280 asks for a serial number that is positive and unique
// within one CA. A random 128-bit number plus one is both.
func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("drawing a serial number: %w", err)
	}
	return serial.Add(serial, big.NewInt(1)), nil
}

func privateKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encoding a private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// What the API serves from: the leaf, its key, the CA a client
// trusts, and the expiry date the certificate gauge publishes.
type servingMaterial struct {
	CertPEM, KeyPEM []byte
	CAPEM           []byte
	Expires         time.Time
}

// ensureServingMaterial reads the Secret the API serves from and
// mints it when it is absent.
//
// A Secret that carries no ca.key is an owner's, written by
// cert-manager or by hand. The API serves what it holds, mints
// nothing, and answers with no authority, and the caller decides
// what a sidecar leaf means without one.
func ensureServingMaterial(c *Client, namespace, secretName string, names []string, now time.Time) (*servingMaterial, *certificateAuthority, error) {
	held, err := get[Secret](c, secretsPath(namespace)+"/"+secretName)
	if err == nil {
		return readServingMaterial(held)
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, nil, err
	}

	ca, err := mintAuthority(now)
	if err != nil {
		return nil, nil, err
	}
	certPEM, keyPEM, err := ca.mintLeaf(apiAudience, names, now)
	if err != nil {
		return nil, nil, err
	}
	secret := &Secret{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata:   objectMeta{Name: secretName, Namespace: namespace},
		Type:       tlsSecretType,
		Data: map[string][]byte{
			tlsCertKey: certPEM,
			tlsKeyKey:  keyPEM,
			caCertKey:  ca.certPEM,
			caKeyKey:   ca.keyPEM,
		},
	}

	// A create that loses the race reads the winner, because two
	// replicas that each minted a CA would serve two, and a client
	// trusting one would refuse the other.
	err = writeObject(c, http.MethodPost, secretsPath(namespace), secret)
	if errors.Is(err, ErrConflict) {
		winner, readErr := get[Secret](c, secretsPath(namespace)+"/"+secretName)
		if readErr != nil {
			return nil, nil, readErr
		}
		return readServingMaterial(winner)
	}
	if err != nil {
		return nil, nil, err
	}
	return readServingMaterial(secret)
}

// The material and the authority are read out of one Secret,
// whichever party wrote it.
func readServingMaterial(secret *Secret) (*servingMaterial, *certificateAuthority, error) {
	leaf, err := leafOf(secret)
	if err != nil {
		return nil, nil, err
	}
	material := &servingMaterial{
		CertPEM: secret.Data[tlsCertKey],
		KeyPEM:  secret.Data[tlsKeyKey],
		CAPEM:   secret.Data[caCertKey],
		Expires: leaf.NotAfter,
	}
	if len(secret.Data[caKeyKey]) == 0 {
		return material, nil, nil
	}
	ca, err := readAuthority(secret.Data[caCertKey], secret.Data[caKeyKey])
	if err != nil {
		return nil, nil, err
	}
	return material, ca, nil
}

// A restarted API reads the CA back out of the Secret that holds it,
// which is how it signs with the CA it minted.
func readAuthority(certPEM, keyPEM []byte) (*certificateAuthority, error) {
	certificate, err := parsePEM(certPEM)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("the authority's key holds no PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("reading the authority's key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the authority's key is %T, not ECDSA", parsed)
	}
	return &certificateAuthority{certificate: certificate, key: key, certPEM: certPEM, keyPEM: keyPEM}, nil
}

func leafOf(secret *Secret) (*x509.Certificate, error) {
	return parsePEM(secret.Data[tlsCertKey])
}

func parsePEM(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("the certificate holds no PEM block")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("reading a certificate: %w", err)
	}
	return certificate, nil
}

// ensureSidecarLeaf puts the sidecar's leaf, whose one SAN tells it
// from the public leaf, into its own Secret, which every node's pod
// mounts as an optional volume.
func ensureSidecarLeaf(c *Client, namespace, secretName string, ca *certificateAuthority, sanName string, now time.Time) error {
	path := secretsPath(namespace) + "/" + secretName
	held, err := get[Secret](c, path)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err == nil && sidecarLeafStands(held, sanName, now) {
		return nil
	}

	certPEM, keyPEM, err := ca.mintLeaf(sanName, []string{sanName}, now)
	if err != nil {
		return err
	}
	secret := &Secret{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata:   objectMeta{Name: secretName, Namespace: namespace},
		Type:       tlsSecretType,
		Data: map[string][]byte{
			tlsCertKey: certPEM,
			tlsKeyKey:  keyPEM,
			caCertKey:  ca.certPEM,
		},
	}
	if held == nil {
		err = writeObject(c, http.MethodPost, secretsPath(namespace), secret)
		if errors.Is(err, ErrConflict) {
			return nil
		}
		return err
	}
	secret.Metadata.ResourceVersion = held.Metadata.ResourceVersion
	return writeObject(c, http.MethodPut, path, secret)
}

// renewServingMaterial re-mints the public leaf once less than a
// third of its life remains. While the leaf stands it answers nil,
// so the caller writes nothing on an ordinary pass.
func renewServingMaterial(c *Client, namespace, secretName string,
	ca *certificateAuthority, names []string, now time.Time) (*servingMaterial, error) {
	path := secretsPath(namespace) + "/" + secretName
	held, err := get[Secret](c, path)
	if err != nil {
		return nil, err
	}
	leaf, err := leafOf(held)
	if err != nil {
		return nil, err
	}
	if !needsRenewal(leaf, now) {
		return nil, nil
	}
	certPEM, keyPEM, err := ca.mintLeaf(apiAudience, names, now)
	if err != nil {
		return nil, err
	}
	held.Data[tlsCertKey] = certPEM
	held.Data[tlsKeyKey] = keyPEM
	if err := writeObject(c, http.MethodPut, path, held); err != nil {
		return nil, err
	}
	material, _, err := readServingMaterial(held)
	return material, err
}

// A leaf stands while its one SAN names the sidecar and it keeps
// more than a third of its life, so a steady pass writes nothing.
func sidecarLeafStands(secret *Secret, sanName string, now time.Time) bool {
	leaf, err := leafOf(secret)
	if err != nil {
		return false
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != sanName {
		return false
	}
	return !needsRenewal(leaf, now)
}

// ensureTrustAnchor publishes the CA certificate alone in a
// ConfigMap, so a client reads it with an ordinary get and never
// touches a Secret.
func ensureTrustAnchor(c *Client, namespace, configMapName string, caPEM []byte) error {
	path := configMapsPath(namespace) + "/" + configMapName
	held, err := get[ConfigMap](c, path)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	anchor := &ConfigMap{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata:   objectMeta{Name: configMapName, Namespace: namespace},
		Data:       map[string]string{caCertKey: string(caPEM)},
	}
	if held == nil {
		err = writeObject(c, http.MethodPost, configMapsPath(namespace), anchor)
		// A create that lost the race reads the winner once and takes
		// it when it holds the same anchor. A winner that holds
		// another anchor is overwritten with this one, and a second
		// race has nothing new to answer.
		if errors.Is(err, ErrConflict) {
			winner, readErr := get[ConfigMap](c, path)
			if readErr != nil {
				return readErr
			}
			if winner.Data[caCertKey] == string(caPEM) {
				return nil
			}
			anchor.Metadata.ResourceVersion = winner.Metadata.ResourceVersion
			return writeObject(c, http.MethodPut, path, anchor)
		}
		return err
	}
	if held.Data[caCertKey] == string(caPEM) {
		return nil
	}
	anchor.Metadata.ResourceVersion = held.Metadata.ResourceVersion
	return writeObject(c, http.MethodPut, path, anchor)
}

// A write reads nothing back, because the API holds what it sent and
// the answer carries no new fact.
func writeObject(c *Client, method, path string, object any) error {
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	return c.RequestJSON(method, path, body, nil)
}
